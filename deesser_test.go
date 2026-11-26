package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func deEss(t *testing.T, in []byte, ch int, s DeEsserSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(DeEsser(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// mixTones : somme de sinus (freq, niveau dBFS), mono, avec un début et une fin en trames.
func burstTones(fs, n int, tones ...[3]float64) []float64 { // {freq, dBFS, ...}
	out := make([]float64, n)
	for _, tn := range tones {
		for i := range out {
			out[i] += ampOf(tn[1]) * math.Sin(2*math.Pi*tn[0]*float64(i)/float64(fs))
		}
	}
	for i := range out {
		out[i] = math.Round(out[i])
	}
	return out
}

func levelAt(pcm []byte, freq float64, fs int) float64 {
	return 20 * math.Log10(goertzel(pcm, freq, fs)/32768)
}

func TestDeEsserNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, fs, [3]float64{7000, -12, 0}))
	for name, s := range map[string]DeEsserSettings{
		"rapport 1":          {ThresholdDB: -30, Ratio: 1},
		"rapport < 1":        {ThresholdDB: -30, Ratio: 0.5},
		"rapport nul":        {ThresholdDB: -30},
		"rapport NaN":        {ThresholdDB: -30, Ratio: math.NaN()},
		"seuil NaN":          {ThresholdDB: math.NaN(), Ratio: 4},
		"séparation Nyquist": {FreqHz: 22050, ThresholdDB: -30, Ratio: 4},
		"séparation NaN":     {FreqHz: math.NaN(), ThresholdDB: -30, Ratio: 4},
	} {
		if out := deEss(t, in, 1, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(DeEsser(bytes.NewReader(in), 0, 1, DeEsserSettings{ThresholdDB: -30, Ratio: 4})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Une sifflante (7 kHz, forte) est réduite ; une note grave qui joue en dessous n'est pas touchée,
// ni avant, ni pendant, ni après la sifflante.
func TestDeEsserReducesSibilanceOnly(t *testing.T) {
	const fs = 44100
	n := 3 * fs
	bass := burstTones(fs, n, [3]float64{500, -20, 0})
	sib := burstTones(fs, n, [3]float64{7000, -12, 0})
	mix := make([]float64, n)
	for i := range mix {
		mix[i] = bass[i]
		if i >= fs && i < fs+fs/2 { // sifflante de 0,5 s au milieu
			mix[i] += sib[i]
		}
	}
	in := interleave(mix)
	out := deEss(t, in, 1, DeEsserSettings{ThresholdDB: -30, Ratio: 8})
	win := func(pcm []byte, from, to int) []byte { return pcm[2*from : 2*to] }
	during := [2]int{fs + fs/10, fs + fs/2 - fs/20}
	redu := levelAt(win(out, during[0], during[1]), 7000, fs) - levelAt(win(in, during[0], during[1]), 7000, fs)
	if redu > -6 {
		t.Errorf("sifflante : %+.1f dB, attendu une réduction de plus de 6 dB", redu)
	}
	for name, w := range map[string][2]int{"avant": {fs / 5, fs - fs/5}, "pendant": during, "après": {fs + fs, 3*fs - fs/10}} {
		g := levelAt(win(out, w[0], w[1]), 500, fs) - levelAt(win(in, w[0], w[1]), 500, fs)
		if math.Abs(g) > 0.3 {
			t.Errorf("note de 500 Hz %s la sifflante : %+.2f dB, attendu inchangée (±0,3)", name, g)
		}
	}
	// le gain revient après la sifflante : 7 kHz sous le seuil (−45 dBFS) traversé à ±0,3 dB
	quiet := interleave(burstTones(fs, fs, [3]float64{7000, -45, 0}))
	qo := deEss(t, quiet, 1, DeEsserSettings{ThresholdDB: -30, Ratio: 8})
	if g := levelAt(win(qo, fs/4, fs), 7000, fs) - levelAt(win(quiet, fs/4, fs), 7000, fs); math.Abs(g) > 0.3 {
		t.Errorf("7 kHz sous le seuil : %+.2f dB, attendu inchangé (±0,3)", g)
	}
	t.Logf("sifflante : %+.1f dB", redu)
}

// Le seuil et le rapport règlent la réduction ; la fréquence de séparation décide de ce qui est surveillé.
func TestDeEsserSettings(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, 2*fs, [3]float64{7000, -14, 0}))
	level := func(s DeEsserSettings) float64 {
		return levelAt(deEss(t, in, 1, s)[2*fs/2:], 7000, fs) - levelAt(in[2*fs/2:], 7000, fs)
	}
	// ratio plus fort → plus de réduction ; seuil plus haut → moins de réduction
	r2, r8 := level(DeEsserSettings{ThresholdDB: -30, Ratio: 2}), level(DeEsserSettings{ThresholdDB: -30, Ratio: 8})
	lo, hi := level(DeEsserSettings{ThresholdDB: -40, Ratio: 4}), level(DeEsserSettings{ThresholdDB: -20, Ratio: 4})
	if !(r8 < r2-2) || !(lo < hi-2) {
		t.Errorf("ratio 2 / 8 : %+.1f / %+.1f dB ; seuil −40 / −20 : %+.1f / %+.1f dB", r2, r8, lo, hi)
	}
	// séparation au-dessus de 7 kHz : la sifflante est dans la bande basse, donc intacte
	if g := level(DeEsserSettings{FreqHz: 12000, ThresholdDB: -40, Ratio: 8}); math.Abs(g) > 0.5 {
		t.Errorf("séparation à 12 kHz : 7 kHz modifié de %+.2f dB, attendu inchangé", g)
	}
	// valeur par défaut de la fréquence : 5 kHz
	if a, b := level(DeEsserSettings{ThresholdDB: -30, Ratio: 4}), level(DeEsserSettings{FreqHz: 5000, ThresholdDB: -30, Ratio: 4}); a != b {
		t.Errorf("FreqHz 0 doit valoir 5000 : %+.3f contre %+.3f", a, b)
	}
}

// Détection liée : une sifflante sur un seul canal réduit les deux canaux de la même façon.
func TestDeEsserLinkedStereo(t *testing.T) {
	const fs = 44100
	n := fs
	left := burstTones(fs, n, [3]float64{7000, -12, 0})
	right := burstTones(fs, n, [3]float64{7000, -40, 0}) // même fréquence, bien plus faible
	in := interleave(left, right)
	out := deEss(t, in, 2, DeEsserSettings{ThresholdDB: -30, Ratio: 8})
	l, r := monoOf(out, 0), monoOf(out, 1)
	li, ri := monoOf(in, 0), monoOf(in, 1)
	gl := levelAt(l[2*fs/2:], 7000, fs) - levelAt(li[2*fs/2:], 7000, fs)
	gr := levelAt(r[2*fs/2:], 7000, fs) - levelAt(ri[2*fs/2:], 7000, fs)
	if gl > -4 || math.Abs(gl-gr) > 0.3 {
		t.Errorf("gains gauche %.2f dB, droit %.2f dB : réduction attendue forte et identique", gl, gr)
	}
}

func TestDeEsserStreaming(t *testing.T) {
	const fs = 44100
	s := DeEsserSettings{ThresholdDB: -30, Ratio: 5}
	in := interleave(burstTones(fs, 20000, [3]float64{7000, -12, 0}, [3]float64{400, -22, 0}),
		burstTones(fs, 20000, [3]float64{6500, -14, 0}, [3]float64{300, -20, 0}))
	want := deEss(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(DeEsser(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
