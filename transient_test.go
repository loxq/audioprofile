package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func shape(t *testing.T, in []byte, ch int, s TransientSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(TransientShaper(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// pluck : un son qui part d'un coup (attaque brutale) et s'éteint en exponentielle, comme un
// coup de caisse claire. La silence avant et après laisse les enveloppes retomber. Niveau crête levelDB.
func pluck(fs int, levelDB, freq, decayMs float64) []float64 {
	n := fs * 2
	out := make([]float64, n)
	start := fs / 2
	for i := start; i < n; i++ {
		tt := float64(i-start) / float64(fs)
		out[i] = math.Round(ampOf(levelDB) * math.Exp(-tt*1000/decayMs) * math.Sin(2*math.Pi*freq*tt))
	}
	return out
}

func rmsRange(v []float64, from, to int) float64 {
	var s float64
	for _, x := range v[from:to] {
		s += x * x
	}
	return math.Sqrt(s / float64(to-from))
}

func floats(pcm []byte) []float64 { return sampleFloats(pcm) }

// gainDB mesure, en dB, le rapport des niveaux efficaces sur une fenêtre.
func windowGainDB(in, out []float64, from, to int) float64 {
	return 20 * math.Log10(rmsRange(out, from, to)/rmsRange(in, from, to))
}

func TestTransientNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]TransientSettings{
		"zéro":   {},
		"NaN":    {Attack: math.NaN(), Sustain: math.NaN()},
		"zéro 2": {Attack: 0, Sustain: math.Copysign(0, -1)},
	} {
		if out := shape(t, in, 2, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(TransientShaper(bytes.NewReader(in), 0, 2, TransientSettings{Attack: 1})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Le test qui décide du bon fonctionnement : un son stable n'est pas touché, même aux réglages
// extrêmes et même dans les basses, où l'ondulation de l'enveloppe se transformerait en
// modulation. Niveau à ±0,2 dB et distorsion sous −45 dB, à deux niveaux d'entrée.
func TestTransientSteadyTonesAreUntouched(t *testing.T) {
	const fs = 44100
	for _, f := range []float64{40, 50, 100, 1000, 8000} {
		for _, lv := range []float64{-20, -6} {
			in := tone(f, ampOf(lv), fs, 3*fs)
			for _, s := range []TransientSettings{{Attack: 1, Sustain: 1}, {Attack: -1, Sustain: -1}, {Attack: 1}, {Sustain: -1}} {
				out := shape(t, in, 1, s)
				a, o := floats(in)[fs:], floats(out)[fs:] // on ignore la première seconde
				// meilleur gain g tel que o ≈ g·a, puis énergie de l'écart
				var ao, aa float64
				for i := range a {
					ao += a[i] * o[i]
					aa += a[i] * a[i]
				}
				g := ao / aa
				var res float64
				for i := range a {
					d := o[i] - g*a[i]
					res += d * d
				}
				gDB, resDB := 20*math.Log10(g), 10*math.Log10(res/(g*g*aa))
				if math.Abs(gDB) > 0.2 || resDB > -45 {
					t.Errorf("%g Hz à %g dBFS, réglage %+v : gain %.3f dB, écart résiduel %.1f dB (attendu ±0,2 et < −45)", f, lv, s, gDB, resDB)
				}
			}
		}
	}
}

// Attack agit sur les premières millisecondes, pas sur la queue ; Sustain l'inverse.
func TestTransientAttackAndSustainActOnTheirOwnPart(t *testing.T) {
	const fs = 44100
	const onset = fs / 2
	in := pluck(fs, -14, 180, 90)
	pcm := interleave(in)
	first := [2]int{onset, onset + fs/100}               // 10 premières ms
	tail := [2]int{onset + fs*15/100, onset + fs*40/100} // de 150 à 400 ms après l'attaque
	g := func(s TransientSettings) (head, queue float64) {
		out := floats(shape(t, pcm, 1, s))
		return windowGainDB(in, out, first[0], first[1]), windowGainDB(in, out, tail[0], tail[1])
	}
	for _, tc := range []struct {
		name             string
		s                TransientSettings
		headMin, headMax float64
		tailMin, tailMax float64
	}{
		{"attaque +1", TransientSettings{Attack: 1}, 3, 12.1, -1, 1},
		{"attaque −1", TransientSettings{Attack: -1}, -12.1, -3, -1, 1},
		{"tenue +1", TransientSettings{Sustain: 1}, -1.5, 1.5, 3, 12.1},
		{"tenue −1", TransientSettings{Sustain: -1}, -1.5, 1.5, -12.1, -3},
	} {
		head, queue := g(tc.s)
		t.Logf("%-10s : 10 premières ms %+.1f dB, queue %+.1f dB", tc.name, head, queue)
		if head < tc.headMin || head > tc.headMax {
			t.Errorf("%s : début %+.1f dB, attendu entre %+.1f et %+.1f", tc.name, head, tc.headMin, tc.headMax)
		}
		if queue < tc.tailMin || queue > tc.tailMax {
			t.Errorf("%s : queue %+.1f dB, attendu entre %+.1f et %+.1f", tc.name, queue, tc.tailMin, tc.tailMax)
		}
	}
	// l'effet croît avec le réglage
	h1, _ := g(TransientSettings{Attack: 0.3})
	h2, _ := g(TransientSettings{Attack: 1})
	if !(h1 > 0.5 && h2 > h1+1) {
		t.Errorf("attaque : %+.1f dB à 0,3 puis %+.1f dB à 1, l'effet doit croître", h1, h2)
	}
}

// Le même son à deux niveaux est traité de la même façon : l'effet suit la forme, pas le niveau.
func TestTransientIsLevelIndependent(t *testing.T) {
	const fs = 44100
	const onset = fs / 2
	var heads []float64
	for _, lv := range []float64{-40, -30, -20} { // sous le plafond doux, qui comprimerait les pics les plus forts
		in := pluck(fs, lv, 180, 90)
		out := floats(shape(t, interleave(in), 1, TransientSettings{Attack: 1}))
		heads = append(heads, windowGainDB(in, out, onset, onset+fs/100))
	}
	t.Logf("gain sur l'attaque à −40 / −30 / −20 dBFS : %+.2f / %+.2f / %+.2f dB", heads[0], heads[1], heads[2])
	for _, h := range heads {
		if math.Abs(h-heads[1]) > 1 {
			t.Errorf("gains %v : écart > 1 dB entre niveaux", heads)
		}
	}
}

// Détection liée : un seul gain pour les deux canaux, dicté par le plus fort.
func TestTransientLinkedStereo(t *testing.T) {
	const fs = 44100
	left := pluck(fs, -12, 200, 90)
	right := make([]float64, len(left))
	for i := range right {
		right[i] = math.Round(ampOf(-30) * math.Sin(2*math.Pi*500*float64(i)/fs)) // sinus faible et stable
	}
	out := shape(t, interleave(left, right), 2, TransientSettings{Attack: 1, Sustain: 1})
	lo, ro := channel(out, 0), channel(out, 1)
	var worst float64
	for i := fs / 2; i < fs; i++ {
		if math.Abs(right[i]) < 300 || math.Abs(left[i]) < 100 {
			continue
		}
		gl := float64(lo[i]) / left[i]
		gr := float64(ro[i]) / right[i]
		worst = math.Max(worst, math.Abs(20*math.Log10(math.Abs(gl)/math.Abs(gr))))
	}
	if worst > 0.1 {
		t.Errorf("gains gauche et droite différents de %.3f dB", worst)
	}
	// et le sinus stable de droite a bien été modifié par le gain dicté par la gauche
	if windowGainDB(right, floats(channelsToPCM(ro)), fs/2, fs/2+fs/100) < 2 {
		t.Error("le canal droit doit suivre le gain de l'attaque du canal gauche")
	}
}

func channelsToPCM(c []int16) []byte { return pcm16(c) }

// Sous −70 dBFS rien n'est touché (pas de pompage du bruit de fond), et le silence reste silence.
func TestTransientLeavesQuietNoiseAndSilence(t *testing.T) {
	const fs = 44100
	noise := make([]float64, fs)
	seed := uint32(7)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = float64(int16(seed>>16)) / 32768 * 5 // ≈ ±5 LSB : −76 dBFS
	}
	for i := range noise {
		noise[i] = math.Round(noise[i] * 32768 / 5 * 3 / 32768 * 5) // quelques LSB
	}
	in := interleave(noise)
	if out := shape(t, in, 1, TransientSettings{Attack: 1, Sustain: 1}); !bytes.Equal(in, out) {
		t.Error("le bruit sous −70 dBFS doit rester intact")
	}
	silence := make([]byte, 2*fs)
	if out := shape(t, silence, 1, TransientSettings{Attack: 1, Sustain: 1}); !bytes.Equal(silence, out) {
		t.Error("le silence doit rester silence")
	}
}

// Le plafond doux : une attaque renforcée sur un signal fort ne fait jamais écrêter.
func TestTransientNoHardClip(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -0.5, 150, 100), pluck(fs, -0.5, 250, 100))
	out := shape(t, in, 2, TransientSettings{Attack: 1})
	for _, v := range samples(out) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
}

func TestTransientClampsSettings(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 180, 90))
	if !bytes.Equal(shape(t, in, 1, TransientSettings{Attack: 7, Sustain: -9}), shape(t, in, 1, TransientSettings{Attack: 1, Sustain: -1})) {
		t.Error("les réglages hors de [-1, 1] doivent être ramenés dedans")
	}
	if !bytes.Equal(shape(t, in, 1, TransientSettings{Attack: 0.5, Sustain: math.NaN()}), shape(t, in, 1, TransientSettings{Attack: 0.5})) {
		t.Error("NaN doit compter pour 0")
	}
}

// Longueur conservée, lecture fragmentée identique, états propres à chaque flux.
func TestTransientStreaming(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 180, 90)[:30000], pluck(fs, -16, 260, 70)[:30000])
	s := TransientSettings{Attack: 0.8, Sustain: -0.4}
	want := shape(t, in, 2, s)
	if len(want) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(want), len(in))
	}
	if bytes.Equal(want, in) {
		t.Fatal("le traitement doit modifier le signal")
	}
	got, err := io.ReadAll(iotest.OneByteReader(TransientShaper(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}

// Sur une boucle de batterie synthétique, Attack > 0 augmente le facteur de crête
// (crête / efficace) et Attack < 0 le diminue.
func TestTransientChangesCrestFactor(t *testing.T) {
	const fs = 44100
	n := 4 * fs
	loop := make([]float64, n)
	for beat := 0; beat < 8; beat++ {
		start := beat * fs / 2
		for i := start; i < start+fs/2 && i < n; i++ {
			tt := float64(i-start) / float64(fs)
			loop[i] = math.Round(ampOf(-10) * math.Exp(-tt*1000/70) * math.Sin(2*math.Pi*120*tt))
		}
	}
	crest := func(v []float64) float64 {
		peak := 0.0
		for _, x := range v {
			peak = math.Max(peak, math.Abs(x))
		}
		return 20 * math.Log10(peak/rmsRange(v, 0, len(v)))
	}
	base := crest(loop)
	up := crest(floats(shape(t, interleave(loop), 1, TransientSettings{Attack: 0.8})))
	down := crest(floats(shape(t, interleave(loop), 1, TransientSettings{Attack: -0.8})))
	t.Logf("facteur de crête : %.1f dB (original), %.1f dB (attaque +0,8), %.1f dB (attaque −0,8)", base, up, down)
	if !(up > base+0.5 && down < base-0.5) {
		t.Errorf("crête %.1f → %.1f / %.1f dB : Attack doit l'augmenter ou la diminuer", base, up, down)
	}
}
