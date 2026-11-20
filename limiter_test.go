package audioprofile

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func runLim(t *testing.T, in []byte, ch int, s LimiterSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(Limiter(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func samples(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return out
}

func pcm16(v []int16) []byte {
	b := make([]byte, 2*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(x))
	}
	return b
}

func maxAbs(v []int16) float64 {
	m := 0.0
	for _, x := range v {
		m = math.Max(m, math.Abs(float64(x)))
	}
	return m
}

// ceilingAmp est le plafond d'échantillon (échelle int16) pour un plafond en dBFS.
func ceilingAmp(dB float64) float64 { return math.Min(32767, ampOf(dB)) }

// Sous le plafond, le signal ressort bit à bit identique, avec la même
// longueur : le retard interne est entièrement rattrapé.
func TestLimiterBelowCeilingIsBitExact(t *testing.T) {
	const fs = 44100
	in := tone(440, ampOf(-6), fs, fs)
	out := runLim(t, in, 1, LimiterSettings{CeilingDB: -1})
	if !bytes.Equal(in, out) {
		t.Error("un signal sous le plafond doit rester intact")
	}
	st := stereoPCM(fs, sineFn(300, ampOf(-8), fs), sineFn(900, ampOf(-3), fs))
	if out := runLim(t, st, 2, LimiterSettings{CeilingDB: -1}); !bytes.Equal(st, out) {
		t.Error("stéréo sous le plafond : le signal doit rester intact")
	}
}

// Une impulsion est restituée à la trame où elle a été reçue, et les entrées
// plus courtes que l'anticipation gardent leur longueur.
func TestLimiterKeepsAlignmentAndLength(t *testing.T) {
	for _, n := range []int{1, 7, 50, 220, 221, 222, 1000} {
		v := make([]int16, n)
		k := n / 2
		v[k] = 12345
		out := samples(runLim(t, pcm16(v), 1, LimiterSettings{CeilingDB: -1}))
		if len(out) != n {
			t.Fatalf("n=%d : longueur %d", n, len(out))
		}
		if out[k] != 12345 {
			t.Errorf("n=%d : impulsion à la trame %d = %d, attendu 12345", n, k, out[k])
		}
	}
	if out := runLim(t, nil, 1, LimiterSettings{}); len(out) != 0 {
		t.Errorf("entrée vide : %d octets", len(out))
	}
}

// Aucune crête ne dépasse le plafond, même sur un signal à pleine échelle dès
// le premier échantillon, un bruit fort ou une salve soudaine.
func TestLimiterNeverExceedsCeiling(t *testing.T) {
	const fs = 44100
	burst := make([]int16, fs)
	for i := range burst {
		if i >= fs/2 && i < fs/2+300 {
			burst[i] = int16(32000 * math.Sin(2*math.Pi*3000*float64(i)/fs)) // salve soudaine
		} else {
			burst[i] = int16(1000 * math.Sin(2*math.Pi*200*float64(i)/fs))
		}
	}
	noise := make([]int16, fs)
	seed := uint32(1)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = int16(seed >> 16) // bruit blanc à pleine échelle
	}
	for name, in := range map[string][]int16{
		"sinus fort dès le début": samples(tone(1000, 32767, fs, fs)),
		"salve soudaine":          burst,
		"bruit pleine échelle":    noise,
	} {
		for _, c := range []float64{0, -1, -6, -20} {
			out := samples(runLim(t, pcm16(in), 1, LimiterSettings{CeilingDB: c}))
			if got, max := maxAbs(out), ceilingAmp(c)+0.5; got > max {
				t.Errorf("%s, plafond %g dB : crête %.0f > %.1f", name, c, got, max)
			}
		}
	}
}

// L'anticipation : la réduction commence avant la salve, et le niveau revient
// après le relâchement.
func TestLimiterLooksAhead(t *testing.T) {
	const fs = 44100
	const onset = fs / 2
	in := make([]int16, 2*fs)
	for i := range in {
		in[i] = int16(4000 * math.Sin(2*math.Pi*500*float64(i)/fs))
		if i >= onset && i < onset+2000 {
			in[i] = int16(30000 * math.Sin(2*math.Pi*500*float64(i)/fs))
		}
	}
	lim := LimiterSettings{CeilingDB: -6, LookaheadMs: 5, ReleaseMs: 100}
	out := samples(runLim(t, pcm16(in), 1, lim))

	peakIn := func(v []int16, a, b int) float64 { return maxAbs(v[a:b]) }
	before := peakIn(out, onset-100, onset) / peakIn(in, onset-100, onset) // 2 ms avant la salve
	if before > 0.95 {
		t.Errorf("la réduction doit commencer avant la salve : gain %.2f 2 ms avant", before)
	}
	if got, max := maxAbs(out), ceilingAmp(-6)+0.5; got > max {
		t.Errorf("crête %.0f > %.1f", got, max)
	}
	// 5 constantes de relâchement après la fin de la salve : niveau revenu à ±0,5 dB
	end := onset + 2000 + 5*fs/10
	after := peakIn(out, end, end+400) / peakIn(in, end, end+400)
	if math.Abs(20*math.Log10(after)) > 0.5 {
		t.Errorf("après le relâchement, gain %.2f dB", 20*math.Log10(after))
	}
	// et la rampe ne ronge pas la salve plus que nécessaire : la crête de sortie atteint le plafond
	if got := maxAbs(out[onset : onset+2000]); got < ceilingAmp(-6)*0.9 {
		t.Errorf("salve trop atténuée : crête %.0f pour un plafond de %.0f", got, ceilingAmp(-6))
	}
}

// Détection liée : un pic sur un seul canal réduit les deux de la même façon.
func TestLimiterLinkedStereo(t *testing.T) {
	const fs = 44100
	in := stereoPCM(fs, sineFn(500, 30000, fs), sineFn(500, 3000, fs))
	out := runLim(t, in, 2, LimiterSettings{CeilingDB: -6})
	l, r := channel(out, 0), channel(out, 1)
	li, ri := channel(in, 0), channel(in, 1)
	a := maxAbs(l[fs/2:]) / maxAbs(li[fs/2:])
	b := maxAbs(r[fs/2:]) / maxAbs(ri[fs/2:])
	if a > 0.6 || math.Abs(a-b) > 0.01 {
		t.Errorf("gains gauche %.3f et droit %.3f : réduction attendue forte et identique", a, b)
	}
}

// Lecture octet par octet, et source qui rend données et EOF ensemble : le vidage
// final et les trames incomplètes se comportent comme avec une lecture simple.
func TestLimiterStreaming(t *testing.T) {
	const fs = 44100
	in := stereoPCM(6000, sineFn(700, 32000, fs), sineFn(1100, 20000, fs))
	s := LimiterSettings{CeilingDB: -3, LookaheadMs: 2}
	want := runLim(t, in, 2, s)
	if len(want) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(want), len(in))
	}
	if bytes.Equal(want, in) {
		t.Fatal("le limiteur doit agir sur ce signal")
	}
	for name, r := range map[string]io.Reader{
		"octet par octet":   iotest.OneByteReader(Limiter(bytes.NewReader(in), fs, 2, s)),
		"données + EOF":     Limiter(iotest.DataErrReader(bytes.NewReader(in)), fs, 2, s),
		"source fragmentée": Limiter(iotest.OneByteReader(bytes.NewReader(in)), fs, 2, s),
	} {
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s : résultat différent (err=%v, %d octets)", name, err, len(got))
		}
	}
	// une trame incomplète en fin de flux est ignorée, comme pour les autres effets
	got := runLim(t, append(append([]byte{}, in...), 1, 2, 3), 2, s)
	if !bytes.Equal(got, want) {
		t.Error("octets en trop en fin de flux : la sortie doit rester celle des trames complètes")
	}
}

func TestLimiterSilenceAndInvalid(t *testing.T) {
	const fs = 44100
	silence := make([]byte, 4*fs)
	if out := runLim(t, silence, 2, LimiterSettings{CeilingDB: -1}); !bytes.Equal(out, silence) {
		t.Error("le silence doit rester exactement silencieux")
	}
	hot := tone(1000, 32000, fs, 4000)
	for name, s := range map[string]LimiterSettings{
		"plafond NaN":      {CeilingDB: math.NaN()},
		"anticipation NaN": {LookaheadMs: math.NaN()},
		"relâchement NaN":  {ReleaseMs: math.NaN()},
	} {
		if out := runLim(t, hot, 1, s); !bytes.Equal(out, hot) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Limiter(bytes.NewReader(hot), 0, 1, LimiterSettings{})); !bytes.Equal(out, hot) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
	// valeurs hors bornes : ramenées dans les limites, sans panique
	out := samples(runLim(t, hot, 1, LimiterSettings{CeilingDB: -100, LookaheadMs: 9999, ReleaseMs: -5}))
	if got, max := maxAbs(out), ceilingAmp(minCeilingDB)+0.5; got > max {
		t.Errorf("plafond ramené à %g dB : crête %.0f > %.1f", minCeilingDB, got, max)
	}
}

// Sur une longue durée, la moyenne glissante ne dérive pas : après plusieurs
// périodes de recalcul, le plafond tient toujours et le signal faible reste intact.
func TestLimiterLongRunStaysExact(t *testing.T) {
	const fs = 44100
	n := 3 * limiterResync
	v := make([]int16, n)
	for i := range v {
		v[i] = int16(2000 * math.Sin(2*math.Pi*300*float64(i)/fs))
		if i%20000 < 50 { // un pic bref régulier
			v[i] = 32000
		}
	}
	out := samples(runLim(t, pcm16(v), 1, LimiterSettings{CeilingDB: -3}))
	if got, max := maxAbs(out), ceilingAmp(-3)+0.5; got > max {
		t.Errorf("crête %.0f > %.1f", got, max)
	}
	// loin des pics (10 000 trames après), le signal est revenu à ±0,3 dB
	i := n - 5000
	a, b := maxAbs(out[i:i+200]), maxAbs(v[i:i+200])
	if math.Abs(20*math.Log10(a/b)) > 0.3 {
		t.Errorf("niveau final %.2f dB", 20*math.Log10(a/b))
	}
}

// La réduction est une rampe étalée sur l'anticipation, pas une marche : sur un
// échelon continu, le gain ne change jamais de plus de (1 − gain final) / D par trame.
func TestLimiterRampIsGradual(t *testing.T) {
	const fs = 44100
	const d = 220 // 5 ms à 44,1 kHz
	const step = 6000
	in := make([]int16, 2000)
	for i := range in {
		in[i] = 4000
		if i >= step/10 {
			in[i] = 20000
		}
	}
	out := samples(runLim(t, pcm16(in), 1, LimiterSettings{CeilingDB: -6, LookaheadMs: 5, ReleaseMs: 100}))
	final := ceilingAmp(-6) / 20000 // gain nécessaire sur l'échelon
	worst, steps := 0.0, 0
	prev := float64(out[0]) / float64(in[0])
	for i := 1; i < len(out); i++ {
		g := float64(out[i]) / float64(in[i])
		worst = math.Max(worst, math.Abs(g-prev))
		if g != prev {
			steps++
		}
		prev = g
	}
	if limit := (1 - final) / d * 1.5; worst > limit {
		t.Errorf("variation de gain maximale %.4f par trame > %.4f : la réduction doit être une rampe", worst, limit)
	}
	if steps < d/2 {
		t.Errorf("la rampe ne s'étale que sur %d trames, attendu ≈ %d", steps, d)
	}
	if got := float64(out[len(out)-1]); math.Abs(got-ceilingAmp(-6)) > 2 {
		t.Errorf("niveau final %.0f, attendu le plafond %.0f", got, ceilingAmp(-6))
	}
}
