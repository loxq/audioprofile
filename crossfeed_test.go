package audioprofile

import (
	"bytes"
	"io"
	"math"
	"math/cmplx"
	"testing"
	"testing/iotest"
)

func cross(t *testing.T, in []byte, ch int, s CrossfeedSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(Crossfeed(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCrossfeedNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	st := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]CrossfeedSettings{
		"force nulle":     {},
		"force NaN":       {Strength: math.NaN()},
		"force négative":  {Strength: -1},
		"coupure Nyquist": {Strength: 0.6, CutoffHz: 22050},
		"coupure NaN":     {Strength: 0.6, CutoffHz: math.NaN()},
	} {
		if out := cross(t, st, 2, s); !bytes.Equal(st, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Crossfeed(bytes.NewReader(st), 0, 2, CrossfeedSettings{Strength: 0.6})); !bytes.Equal(st, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
	// seuls les flux stéréo sont traités
	mono := tone(300, 8000, fs, 4000)
	if out := cross(t, mono, 1, CrossfeedSettings{Strength: 1}); !bytes.Equal(mono, out) {
		t.Error("mono : le signal doit rester intact")
	}
	three := interleave(pluck(fs, -12, 200, 80)[:3000], pluck(fs, -14, 300, 60)[:3000], pluck(fs, -16, 400, 60)[:3000])
	if out := cross(t, three, 3, CrossfeedSettings{Strength: 1}); !bytes.Equal(three, out) {
		t.Error("trois canaux : le signal doit rester intact")
	}
}

// Un son au centre ressort à l'identique (bit à bit), et le milieu (G + D) de n'importe quel signal est
// conservé à 1 LSB près : l'effet est compatible mono.
func TestCrossfeedKeepsCenterAndMono(t *testing.T) {
	const fs = 44100
	centered := interleave(burstTones(fs, fs, [3]float64{300, -12, 0}, [3]float64{4000, -20, 0}),
		burstTones(fs, fs, [3]float64{300, -12, 0}, [3]float64{4000, -20, 0}))
	for _, st := range []float64{0.3, 0.6, 1} {
		if out := cross(t, centered, 2, CrossfeedSettings{Strength: st}); !bytes.Equal(centered, out) {
			t.Errorf("force %g : un son au centre doit rester identique", st)
		}
	}
	in := interleave(burstTones(fs, fs, [3]float64{220, -10, 0}, [3]float64{3100, -18, 0}),
		burstTones(fs, fs, [3]float64{330, -12, 0}, [3]float64{5300, -16, 0}))
	li, ri := channel(in, 0), channel(in, 1)
	for _, st := range []float64{0.3, 0.6, 1} {
		out := cross(t, in, 2, CrossfeedSettings{Strength: st})
		lo, ro := channel(out, 0), channel(out, 1)
		worst := 0
		for i := range lo {
			d := (int(lo[i]) + int(ro[i])) - (int(li[i]) + int(ri[i]))
			if d < 0 {
				d = -d
			}
			if d > worst {
				worst = d
			}
		}
		if worst > 1 {
			t.Errorf("force %g : G+D modifié de %d LSB", st, worst)
		}
	}
}

// Le côté (G − D) est réduit sous la coupure de (1 − Strength) et laissé presque intact bien au-dessus.
func TestCrossfeedReducesSideBelowCutoff(t *testing.T) {
	const fs = 44100
	side := func(freq float64, s CrossfeedSettings) float64 {
		in := interleave(burstTones(fs, 2*fs, [3]float64{freq, -12, 0}), burstTones(fs, 2*fs, [3]float64{freq, -12 - 6.0206*0, 0}))
		// côté pur : D = −G
		l := burstTones(fs, 2*fs, [3]float64{freq, -12, 0})
		r := make([]float64, len(l))
		for i := range l {
			r[i] = -l[i]
		}
		in = interleave(l, r)
		out := cross(t, in, 2, s)
		_, so := midSide(t, out, freq, fs)
		_, si := midSide(t, in, freq, fs)
		return 20 * math.Log10(so/si)
	}
	// côté en sortie / côté en entrée = |1 − Strength × H|, H étant la réponse (complexe) du passe-bas du premier
	// ordre : à 100 Hz, H vaut presque 1, mais sa phase empêche l'annulation exacte
	lp := onePole(fs, defaultCrossfeedHz, false)
	for _, st := range []float64{0.3, 0.6, 0.8, 1} {
		z := cmplx.Rect(1, -2*math.Pi*100/fs)
		h := (complex(lp.b0, 0) + complex(lp.b1, 0)*z) / (1 + complex(lp.a1, 0)*z)
		want := 20 * math.Log10(cmplx.Abs(1-complex(st, 0)*h))
		if g := side(100, CrossfeedSettings{Strength: st}); math.Abs(g-want) > 0.4 {
			t.Errorf("force %g à 100 Hz : côté %+.2f dB, attendu %+.2f", st, g, want)
		}
	}
	if g := side(100, CrossfeedSettings{Strength: 1}); g > -15 {
		t.Errorf("force 1 à 100 Hz : côté %+.1f dB, attendu au moins −15 dB", g)
	}
	// bien au-dessus de la coupure : presque rien (8 kHz, passe-bas du premier ordre à 700 Hz)
	if g := side(8000, CrossfeedSettings{Strength: 1}); g < -1 {
		t.Errorf("8 kHz : côté %+.2f dB, attendu presque intact", g)
	}
	// la coupure se règle : à 3 kHz, une coupure de 5 kHz agit, une coupure de 300 Hz non
	if hi, lo := side(3000, CrossfeedSettings{Strength: 1, CutoffHz: 5000}), side(3000, CrossfeedSettings{Strength: 1, CutoffHz: 300}); hi > lo-3 {
		t.Errorf("3 kHz : côté %+.1f dB (coupure 5 kHz) contre %+.1f dB (coupure 300 Hz) : la coupure haute doit réduire bien plus", hi, lo)
	}
}

// Un son entièrement à gauche passe un peu à droite, aux graves seulement : −10,5 dB à 100 Hz pour Strength = 0,6.
func TestCrossfeedBleedsHardLeftIntoRight(t *testing.T) {
	const fs = 44100
	bleed := func(freq float64) float64 {
		in := interleave(burstTones(fs, 2*fs, [3]float64{freq, -10, 0}), make([]float64, 2*fs))
		out := cross(t, in, 2, CrossfeedSettings{Strength: 0.6})
		return levelAt(monoOf(out, 1), freq, fs) - levelAt(monoOf(in, 0), freq, fs)
	}
	if g := bleed(100); math.Abs(g+10.5) > 0.6 {
		t.Errorf("100 Hz : fuite de %+.1f dB à droite, attendu ≈ −10,5", g)
	}
	if g := bleed(8000); g > -30 {
		t.Errorf("8 kHz : fuite de %+.1f dB à droite, attendu < −30 (les aigus restent séparés)", g)
	}
}

// Le retard décale le signal croisé, et colore un peu le centre ; sans retard, aucune coloration.
func TestCrossfeedDelay(t *testing.T) {
	const fs = 44100
	// l'impulsion à gauche arrive à droite après le retard
	for _, ms := range []float64{0, 0.25, 0.5} {
		d := int(math.Round(ms / 1000 * fs))
		l := make([]float64, 400)
		l[100] = 20000
		out := cross(t, interleave(l, make([]float64, 400)), 2, CrossfeedSettings{Strength: 1, DelayMs: ms})
		first := -1
		for i, v := range channel(out, 1) {
			if v != 0 {
				first = i
				break
			}
		}
		if first != 100+d {
			t.Errorf("retard de %g ms : la fuite à droite commence à la trame %d, attendu %d", ms, first, 100+d)
		}
	}
	// coloration du centre : de l'ordre de 2 dB entre 300 Hz et 1 kHz avec 0,25 ms
	worst := 0.0
	for _, f := range []float64{200, 300, 500, 700, 1000, 2000} {
		in := interleave(burstTones(fs, 2*fs, [3]float64{f, -12, 0}), burstTones(fs, 2*fs, [3]float64{f, -12, 0}))
		out := cross(t, in, 2, CrossfeedSettings{Strength: 0.6, DelayMs: 0.25})
		worst = math.Max(worst, math.Abs(levelAt(monoOf(out, 0), f, fs)-levelAt(monoOf(in, 0), f, fs)))
	}
	t.Logf("coloration maximale du centre avec 0,25 ms de retard : %.2f dB", worst)
	if worst > 3 || worst < 0.3 {
		t.Errorf("coloration %.2f dB, attendue entre 0,3 et 3 dB", worst)
	}
}

func TestCrossfeedChannelsClampAndStreaming(t *testing.T) {
	const fs = 44100
	silence := make([]byte, 8*fs)
	if o := cross(t, silence, 2, CrossfeedSettings{Strength: 1, DelayMs: 0.5}); !bytes.Equal(o, silence) {
		t.Error("le silence doit rester silence")
	}
	in := interleave(pluck(fs, -12, 220, 100)[:30000], pluck(fs, -16, 340, 70)[:30000])
	if !bytes.Equal(cross(t, in, 2, CrossfeedSettings{Strength: 9, CutoffHz: 700, DelayMs: 50}), cross(t, in, 2, CrossfeedSettings{Strength: 1, CutoffHz: 700, DelayMs: maxCrossfeedDelay})) {
		t.Error("force et retard hors bornes doivent être ramenés dedans")
	}
	s := CrossfeedSettings{Strength: 0.6, DelayMs: 0.25}
	want := cross(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(Crossfeed(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
