package bassboost

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

// tone génère n échantillons mono d'un sinus d'amplitude amp (échelle int16).
func tone(freq, amp float64, fs, n int) []byte {
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		binary.Write(buf, binary.LittleEndian, int16(amp*math.Sin(2*math.Pi*freq*float64(i)/float64(fs))))
	}
	return buf.Bytes()
}

func dbfs(amp float64) float64 { return 20 * math.Log10(amp/32768) }
func ampOf(dB float64) float64 { return 32768 * math.Pow(10, dB/20) }

// Courbe statique : sous le seuil rien ne change, au-dessus la pente suit le ratio.
func TestCompressorStaticCurve(t *testing.T) {
	const fs = 44100
	c := CompressorSettings{ThresholdDB: -20, Ratio: 4, AttackMs: 1, ReleaseMs: 50}
	for _, tc := range []struct{ inDB, wantGainDB float64 }{
		{-30, 0},  // sous le seuil
		{-20, 0},  // au seuil
		{-12, -6}, // 8 dB au-dessus → -8*(1-1/4)
		{-4, -12}, // 16 dB au-dessus → -16*(3/4)
	} {
		in := tone(1000, ampOf(tc.inDB), fs, fs)
		out, err := io.ReadAll(Compressor(bytes.NewReader(in), fs, 1, c))
		if err != nil || len(out) != len(in) {
			t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
		}
		got := 20 * math.Log10(rms(out)/rms(in))
		t.Logf("entrée %+.0f dBFS : gain %+.2f dB (attendu %+.1f)", tc.inDB, got, tc.wantGainDB)
		// tolérance : le lissage laisse un peu de réduction en moins entre les crêtes
		if got > tc.wantGainDB+1.5 || got < tc.wantGainDB-0.7 {
			t.Errorf("%+.0f dBFS : gain %+.2f dB, attendu %+.1f", tc.inDB, got, tc.wantGainDB)
		}
	}
}

func TestCompressorKneeIsSofter(t *testing.T) {
	c := newCompressor(44100, &CompressorSettings{ThresholdDB: -20, Ratio: 4, KneeDB: 10})
	hard := newCompressor(44100, &CompressorSettings{ThresholdDB: -20, Ratio: 4})
	// au seuil, un genou doux commence déjà à réduire
	if !(c.reduction(-20) < hard.reduction(-20)-0.5) {
		t.Errorf("genou doux : %.2f dB au seuil, coude franc : %.2f dB", c.reduction(-20), hard.reduction(-20))
	}
	// la courbe est continue et décroissante autour du genou
	prev := 1.0
	for l := -40.0; l <= 0; l += 0.25 {
		r := c.reduction(l)
		if r > prev+1e-9 {
			t.Fatalf("réduction non décroissante en %.2f dB", l)
		}
		if math.Abs(r-prev) > 0.3 && prev != 1.0 {
			t.Fatalf("saut de réduction en %.2f dB", l)
		}
		prev = r
	}
	// loin au-dessus du genou : pente du ratio
	if got := c.reduction(0); math.Abs(got-(-20*0.75)) > 1e-9 {
		t.Errorf("réduction à 0 dBFS : %.2f, attendu -15", got)
	}
}

// Attaque : un fort signal est d'abord peu réduit puis la réduction s'installe ;
// relâchement : après la fin du fort signal, le gain remonte progressivement.
func TestCompressorAttackRelease(t *testing.T) {
	const fs = 44100
	c := CompressorSettings{ThresholdDB: -30, Ratio: 10, AttackMs: 20, ReleaseMs: 200}
	loud := tone(1000, ampOf(-6), fs, fs/2)
	quiet := tone(1000, ampOf(-40), fs, fs/2)
	in := append(append([]byte{}, loud...), quiet...)
	out, _ := io.ReadAll(Compressor(bytes.NewReader(in), fs, 1, c))

	win := func(b, ref []byte, from, to int) float64 { // gain moyen (dB) sur [from, to) échantillons
		var so, si float64
		for i := from; i < to; i++ {
			o := float64(int16(binary.LittleEndian.Uint16(b[2*i:])))
			x := float64(int16(binary.LittleEndian.Uint16(ref[2*i:])))
			so += o * o
			si += x * x
		}
		return 10 * math.Log10(so/si)
	}
	ms := func(m float64) int { return int(m / 1000 * fs) }
	start := win(out, in, 0, ms(2))
	settled := win(out, in, ms(300), ms(400))
	tail0 := win(out, in, len(loud)/2, len(loud)/2+ms(20)) // juste après la coupure
	tail1 := win(out, in, len(loud)/2+ms(400), len(loud)/2+ms(480))
	t.Logf("début %.1f dB, établi %.1f dB ; relâchement : %.1f dB puis %.1f dB", start, settled, tail0, tail1)
	if !(start > settled+6) {
		t.Errorf("l'attaque doit être progressive (début %.1f, établi %.1f)", start, settled)
	}
	if !(tail0 < tail1-6) {
		t.Errorf("le relâchement doit être progressif (%.1f puis %.1f)", tail0, tail1)
	}
}

// Détection liée : un canal fort réduit aussi l'autre, le rapport G/D est conservé.
func TestCompressorStereoLinked(t *testing.T) {
	const fs = 44100
	l := tone(1000, ampOf(-6), fs, fs)
	r := tone(1000, ampOf(-36), fs, fs)
	in := make([]byte, 0, 2*len(l))
	for i := 0; i < len(l); i += 2 {
		in = append(in, l[i], l[i+1], r[i], r[i+1])
	}
	out, _ := io.ReadAll(Compressor(bytes.NewReader(in), fs, 2, CompressorSettings{ThresholdDB: -20, Ratio: 6, AttackMs: 1}))
	chan2 := func(b []byte, ch int) []byte {
		o := make([]byte, 0, len(b)/2)
		for i := 0; i+3 < len(b); i += 4 {
			o = append(o, b[i+2*ch], b[i+2*ch+1])
		}
		return o
	}
	inRatio := rms(chan2(in, 0)) / rms(chan2(in, 1))
	outRatio := rms(chan2(out, 0)) / rms(chan2(out, 1))
	t.Logf("rapport G/D : %.2f en entrée, %.2f en sortie", inRatio, outRatio)
	if math.Abs(outRatio-inRatio) > 0.03*inRatio {
		t.Errorf("l'image stéréo doit être conservée (%.2f → %.2f)", inRatio, outRatio)
	}
	if rms(chan2(out, 1)) > 0.8*rms(chan2(in, 1)) {
		t.Error("le canal faible doit aussi être réduit (détection liée)")
	}
}

func TestCompressorMakeupAndIdentity(t *testing.T) {
	const fs = 44100
	in := tone(1000, 3000, fs, 2000)
	out, _ := io.ReadAll(Compressor(bytes.NewReader(in), fs, 1, CompressorSettings{ThresholdDB: -20, Ratio: 1}))
	if !bytes.Equal(in, out) {
		t.Error("ratio 1 sans compensation : signal intact attendu")
	}
	out, _ = io.ReadAll(Compressor(bytes.NewReader(in), 0, 1, CompressorSettings{ThresholdDB: -20, Ratio: 4}))
	if !bytes.Equal(in, out) {
		t.Error("sampleRate <= 0 : signal intact attendu")
	}
	// compensation seule : gain constant
	out, _ = io.ReadAll(Compressor(bytes.NewReader(in), fs, 1, CompressorSettings{Ratio: 1, MakeupDB: 6}))
	got := 20 * math.Log10(rms(out)/rms(in))
	if math.Abs(got-6) > 0.05 {
		t.Errorf("compensation : %+.2f dB, attendu +6", got)
	}
	if newCompressor(fs, nil) != nil || newCompressor(fs, &CompressorSettings{Ratio: math.NaN()}) != nil {
		t.Error("réglage invalide ou absent : nil attendu")
	}
}

func TestCompressorOddChunks(t *testing.T) {
	in := tone(60, 20000, 44100, 2*3001)
	c := CompressorSettings{ThresholdDB: -20, Ratio: 3, KneeDB: 6}
	out, err := io.ReadAll(Compressor(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), 44100, 2, c))
	ref, _ := io.ReadAll(Compressor(bytes.NewReader(in), 44100, 2, c))
	if err != nil || !bytes.Equal(out, ref) {
		t.Fatalf("err=%v : la lecture fragmentée doit donner le même résultat", err)
	}
}

func TestScaleCompression(t *testing.T) {
	base := CompressorSettings{ThresholdDB: -20, Ratio: 3, MakeupDB: 4}
	if got := scaleCompression(base, 0); got.Ratio != 1 || got.MakeupDB != 0 {
		t.Errorf("intensité 0 : %+v", got)
	}
	if got := scaleCompression(base, 1); got != base {
		t.Errorf("intensité 1 : %+v", got)
	}
	if got := scaleCompression(base, 0.5); got.Ratio != 2 || got.MakeupDB != 2 || got.ThresholdDB != -20 {
		t.Errorf("intensité 0.5 : %+v", got)
	}
}

// Coude franc : aucun NaN, y compris pile au seuil (0/0 dans une formule de genou naïve).
func TestCompressorHardKneeNoNaN(t *testing.T) {
	c := newCompressor(44100, &CompressorSettings{ThresholdDB: -20, Ratio: 4})
	for _, l := range []float64{-120, -30, -20.000001, -20, -19.999999, -10, 0} {
		if r := c.reduction(l); math.IsNaN(r) || r > 0 {
			t.Errorf("reduction(%v) = %v", l, r)
		}
	}
	if c.reduction(-20) != 0 || c.reduction(-30) != 0 {
		t.Error("pas de réduction au seuil ou en dessous avec un coude franc")
	}
	// un flux dont le niveau passe exactement par le seuil reste sain
	frame := []float64{ampOf(-20)}
	for i := 0; i < 1000; i++ {
		c.apply(frame[:1])
		frame[0] = ampOf(-20)
	}
	if math.IsNaN(c.gr) {
		t.Error("l'état du compresseur est devenu NaN")
	}
}

// Rock compresse : par rapport à Rock sans compresseur, l'écart entre signaux
// forts et faibles se réduit.
func TestRockCompressesDynamics(t *testing.T) {
	const fs = 44100
	gap := func() float64 { // gain(faible) - gain(fort), en dB
		level := func(amp float64) float64 {
			in := tone(1000, amp, fs, fs)
			out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, Rock, 1))
			return 20 * math.Log10(rms(out)/rms(in))
		}
		return level(ampOf(-40)) - level(ampOf(-6))
	}
	with := gap()
	saved := profileCompression[Rock]
	profileCompression[Rock] = nil
	defer func() { profileCompression[Rock] = saved }()
	without := gap()
	t.Logf("écart faible/fort : %.1f dB avec compresseur, %.1f dB sans", with, without)
	if !(with > without+2) {
		t.Errorf("le compresseur doit creuser l'écart de gain (%.1f vs %.1f)", with, without)
	}
}
