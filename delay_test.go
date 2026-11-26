package audioprofile

import (
	"bytes"
	"io"
	"math"
	"math/cmplx"
	"testing"
	"testing/iotest"
)

func delayed(t *testing.T, in []byte, ch int, s DelaySettings) []byte {
	t.Helper()
	out, err := io.ReadAll(Delay(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func impulses(n int, positions ...int) []float64 {
	v := make([]float64, n)
	for _, p := range positions {
		v[p] = 20000
	}
	return v
}

func TestDelayNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]DelaySettings{
		"mix nul":     {TimeMs: 300, Feedback: 0.5},
		"mix NaN":     {TimeMs: 300, Feedback: 0.5, Mix: math.NaN()},
		"mix négatif": {TimeMs: 300, Mix: -1},
	} {
		if out := delayed(t, in, 2, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Delay(bytes.NewReader(in), 0, 2, DelaySettings{TimeMs: 300, Mix: 1})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Les échos tombent aux multiples exacts de TimeMs, avec l'amplitude Mix × Feedback^(k−1) ; rien ailleurs.
func TestDelayEchoTimesAndAmplitudes(t *testing.T) {
	const fs = 44100
	const d = 441 // 10 ms
	for _, tc := range []struct{ fb, mix float64 }{{0.5, 1}, {0.8, 0.5}, {0, 1}} {
		in := interleave(impulses(8*d, 100))
		got := samples(delayed(t, in, 1, DelaySettings{TimeMs: 10, Feedback: tc.fb, Mix: tc.mix}))
		for i, v := range got {
			want := 0.0
			switch {
			case i == 100:
				want = 20000
			case i > 100 && (i-100)%d == 0:
				k := (i - 100) / d
				want = 20000 * tc.mix * math.Pow(tc.fb, float64(k-1))
				if k > 1 && tc.fb == 0 {
					want = 0
				}
			}
			if math.Abs(float64(v)-want) > 1 {
				t.Fatalf("fb %g mix %g : trame %d = %d, attendu %.1f", tc.fb, tc.mix, i, v, want)
			}
		}
	}
}

// Stéréo : chaque canal garde ses échos ; un canal silencieux reste exactement silencieux.
func TestDelayChannelsAreIndependent(t *testing.T) {
	const d = 441
	in := interleave(impulses(6*d, 50), make([]float64, 6*d))
	out := delayed(t, in, 2, DelaySettings{TimeMs: 10, Feedback: 0.6, Mix: 0.7})
	for i, v := range channel(out, 1) {
		if v != 0 {
			t.Fatalf("canal droit silencieux : %d à la trame %d", v, i)
		}
	}
	if l := channel(out, 0); l[50+d] == 0 || l[50+2*d] == 0 {
		t.Error("le canal gauche doit avoir ses échos")
	}
}

// Ping-pong : le premier écho à gauche, le deuxième à droite, etc., aux trames exactes, avec Feedback^(k−1).
func TestDelayPingPong(t *testing.T) {
	const d = 441
	in := interleave(impulses(8*d, 30), impulses(8*d, 30))
	out := delayed(t, in, 2, DelaySettings{TimeMs: 10, Feedback: 0.7, Mix: 1, PingPong: true})
	l, r := channel(out, 0), channel(out, 1)
	for k := 1; k <= 6; k++ {
		i := 30 + k*d
		want := 20000 * math.Pow(0.7, float64(k-1))
		got, other := float64(l[i]), float64(r[i])
		if k%2 == 0 {
			got, other = other, got
		}
		if math.Abs(got-want) > 1.5 || other != 0 {
			t.Errorf("écho %d : côté attendu %.1f, lu %.0f, autre côté %.0f", k, want, got, other)
		}
	}
	// ignoré en mono et à trois canaux : même flux qu'avec PingPong faux
	mono := interleave(impulses(6*d, 30))
	if !bytes.Equal(delayed(t, mono, 1, DelaySettings{TimeMs: 10, Feedback: 0.7, Mix: 1, PingPong: true}), delayed(t, mono, 1, DelaySettings{TimeMs: 10, Feedback: 0.7, Mix: 1})) {
		t.Error("PingPong doit être ignoré en mono")
	}
	three := interleave(impulses(6*d, 30), impulses(6*d, 60), impulses(6*d, 90))
	if !bytes.Equal(delayed(t, three, 3, DelaySettings{TimeMs: 10, Feedback: 0.7, Mix: 1, PingPong: true}), delayed(t, three, 3, DelaySettings{TimeMs: 10, Feedback: 0.7, Mix: 1})) {
		t.Error("PingPong doit être ignoré à trois canaux")
	}
}

// L'amortissement assombrit les échos successifs : un aigu perd, à chaque tour de boucle, le gain du
// passe-bas en plus de Feedback.
func TestDelayDampingDarkensEchoes(t *testing.T) {
	const fs = 44100
	const d = 4410 // 100 ms
	burst := make([]float64, 5*d)
	for i := 0; i < d/2; i++ {
		burst[i] = math.Round(ampOf(-14) * math.Sin(2*math.Pi*6000*float64(i)/fs))
	}
	in := interleave(burst)
	level := func(s DelaySettings, k int) float64 {
		o := floats(delayed(t, in, 1, s))
		return 20 * math.Log10(rmsRange(o, k*d+d/8, k*d+d/2-d/8))
	}
	plain := DelaySettings{TimeMs: 100, Feedback: 0.8, Mix: 1}
	damp := plain
	damp.DampingHz = 2000
	lp := onePole(fs, 2000, false)
	z := cmplx.Rect(1, -2*math.Pi*6000/fs)
	g := 20 * math.Log10(cmplx.Abs((complex(lp.b0, 0)+complex(lp.b1, 0)*z)/(1+complex(lp.a1, 0)*z)))
	for _, k := range []int{2, 3} { // écho k par rapport à l'écho 1 : (k−1) tours de boucle de plus
		dB := level(damp, k) - level(plain, k)
		want := float64(k-1) * g
		if math.Abs(dB-want) > 1 {
			t.Errorf("écho %d : %.2f dB de moins avec l'amortissement, attendu %.2f (gain du passe-bas à 6 kHz : %.2f dB par tour)", k, dB, want, g)
		}
	}
}

// Bornes, queue coupée, silence, états minuscules, lecture fragmentée.
func TestDelayClampTailAndStreaming(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 200, 90)[:30000], pluck(fs, -16, 300, 70)[:30000])
	if !bytes.Equal(delayed(t, in, 2, DelaySettings{TimeMs: 99999, Feedback: 5, Mix: 9}), delayed(t, in, 2, DelaySettings{TimeMs: maxDelayMs, Feedback: maxDelayFeedback, Mix: 1})) {
		t.Error("les valeurs hors bornes doivent être ramenées dedans")
	}
	if !bytes.Equal(delayed(t, in, 2, DelaySettings{TimeMs: -5, Mix: 0.5}), delayed(t, in, 2, DelaySettings{TimeMs: 1, Mix: 0.5})) {
		t.Error("une durée trop courte doit être ramenée à 1 ms")
	}
	silence := make([]byte, 8*fs)
	if o := delayed(t, silence, 2, DelaySettings{TimeMs: 200, Feedback: 0.9, Mix: 1, PingPong: true}); !bytes.Equal(o, silence) {
		t.Error("le silence doit rester silence")
	}
	s := DelaySettings{TimeMs: 120, Feedback: 0.6, Mix: 0.5, DampingHz: 4000, PingPong: true}
	want := delayed(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(Delay(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
	// la réponse à une impulsion décroît et, après un long silence, aucun état n'est resté minuscule non nul
	st := DelaySettings{TimeMs: 5, Feedback: 0.9, Mix: 1}.stage(fs, 1).(*delay)
	st.apply([]float64{20000})
	var late float64
	for i := 0; i < 60*fs; i++ {
		fr := []float64{0}
		st.apply(fr)
		if i > 59*fs {
			late = math.Max(late, math.Abs(fr[0]))
		}
	}
	if late != 0 {
		t.Errorf("après 60 s, l'écho résiduel vaut %g, attendu 0 (états minuscules ramenés à zéro)", late)
	}
}
