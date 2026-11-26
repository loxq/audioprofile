package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

// wetOf fait passer x (mono, flottant) par l'étage m et renvoie sa partie réverbérée seule : sortie − sec.
func wetOf(m *modulation, x []float64) []float64 {
	wet := make([]float64, len(x))
	frame := []float64{0}
	for i, v := range x {
		frame[0] = v
		m.apply(frame)
		wet[i] = frame[0] - v
	}
	return wet
}

func sineF(freq, amp float64, fs, n int) []float64 {
	x := make([]float64, n)
	for i := range x {
		x[i] = amp * math.Sin(2*math.Pi*freq*float64(i)/float64(fs))
	}
	return x
}

// freqTrack renvoie la fréquence instantanée d'un sinus (une valeur par période, par les passages à zéro montants,
// interpolés linéairement) et leurs instants en secondes.
func freqTrack(x []float64, fs int) (freqs, times []float64) {
	var last float64
	have := false
	for i := 1; i < len(x); i++ {
		if x[i-1] < 0 && x[i] >= 0 {
			t := float64(i-1) + (-x[i-1])/(x[i]-x[i-1])
			if have {
				freqs = append(freqs, float64(fs)/(t-last))
				times = append(times, t/float64(fs))
			}
			last, have = t, true
		}
	}
	return
}

func TestModulationNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, r := range map[string]io.Reader{
		"chorus mix nul":      Chorus(bytes.NewReader(in), fs, 2, ChorusSettings{Voices: 3}),
		"chorus mix NaN":      Chorus(bytes.NewReader(in), fs, 2, ChorusSettings{Mix: math.NaN()}),
		"flanger mix nul":     Flanger(bytes.NewReader(in), fs, 2, FlangerSettings{Feedback: 0.5}),
		"flanger mix négatif": Flanger(bytes.NewReader(in), fs, 2, FlangerSettings{Mix: -1}),
		"chorus fs nul":       Chorus(bytes.NewReader(in), 0, 2, ChorusSettings{Mix: 0.5}),
		"flanger fs nul":      Flanger(bytes.NewReader(in), 0, 2, FlangerSettings{Mix: 0.5}),
	} {
		if out, _ := io.ReadAll(r); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
}

// Lecture fractionnaire : exacte aux retards entiers, et l'interpolation de Hermite garde le niveau des aigus
// (une interpolation linéaire en perdrait 1,5 dB à 8 kHz à mi-échantillon).
func TestModDelayInterpolation(t *testing.T) {
	const fs = 44100
	x := sineF(1234, 1000, fs, 400)
	m := newModDelay(100)
	for i := 0; i < 300; i++ {
		m.push(x[i])
	}
	for _, d := range []float64{2, 7, 50} {
		if got, want := m.read(d), x[299-int(d)+1]; got != want {
			t.Errorf("retard entier %g : %g, attendu %g", d, got, want)
		}
	}
	for _, f := range []float64{1000, 4000, 8000} {
		sig := sineF(f, 1000, fs, 2000)
		m := newModDelay(100)
		worst, worstLin := 0.0, 0.0
		for _, frac := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
			var e, eLin float64
			var n int
			m = newModDelay(100)
			for i, v := range sig {
				m.push(v)
				if i < 200 {
					continue
				}
				d := 20 + frac
				got := m.read(d)
				e += got * got
				lin := (1-frac)*sig[i-20+1] + frac*sig[i-21+1] // interpolation linéaire entre d = 20 et 21
				eLin += lin * lin
				n++
			}
			ref := 1000 * 1000 / 2.0
			worst = math.Max(worst, math.Abs(10*math.Log10(e/float64(n)/ref)))
			worstLin = math.Max(worstLin, math.Abs(10*math.Log10(eLin/float64(n)/ref)))
		}
		t.Logf("%g Hz : écart de niveau maximal %.2f dB (Hermite), %.2f dB (linéaire)", f, worst, worstLin)
		limit := 0.05
		if f >= 8000 {
			limit = 0.5
		}
		if worst > limit {
			t.Errorf("%g Hz : le niveau varie de %.2f dB selon la fraction, attendu au plus %.2f", f, worst, limit)
		}
		if f >= 4000 && worst > worstLin/2 {
			t.Errorf("%g Hz : Hermite (%.2f dB) doit faire au moins deux fois mieux que le linéaire (%.2f dB)", f, worst, worstLin)
		}
	}
}

// Modulation : l'écart de fréquence d'un 1 kHz vaut 2π × vitesse × profondeur × f, oscille à la vitesse demandée,
// et vaut zéro quand la profondeur est nulle.
func TestModulationFrequencyDeviation(t *testing.T) {
	const fs = 44100
	const f0, rate, depth = 1000.0, 2.0, 1.0 // Hz, Hz, ms
	x := sineF(f0, 8000, fs, 4*fs)
	m := newModulation(fs, 1, 12, depth, rate, 1, 0, 1)
	freqs, times := freqTrack(wetOf(m, x), fs)
	var lo, hi, mean float64
	lo, hi = math.Inf(1), math.Inf(-1)
	var kept []float64
	for i, f := range freqs {
		if times[i] < 0.5 { // régime établi
			continue
		}
		kept = append(kept, f)
		lo, hi = math.Min(lo, f), math.Max(hi, f)
		mean += f
	}
	mean /= float64(len(kept))
	dev := (hi - lo) / 2
	want := f0 * 2 * math.Pi * rate * depth / 1000
	t.Logf("écart de fréquence : %.2f Hz (attendu %.2f Hz), moyenne %.3f Hz", dev, want, mean)
	if math.Abs(dev/want-1) > 0.12 {
		t.Errorf("écart %.2f Hz, attendu %.2f Hz à 12 %% près", dev, want)
	}
	if math.Abs(mean-f0) > 0.5 {
		t.Errorf("fréquence moyenne %.3f Hz : la hauteur moyenne ne doit pas bouger", mean)
	}
	changes := 0
	for i := 1; i < len(kept); i++ {
		if (kept[i-1]-mean)*(kept[i]-mean) < 0 {
			changes++
		}
	}
	if secs := float64(len(kept)) / f0; math.Abs(float64(changes)/(2*secs)-rate) > 0.3 {
		t.Errorf("vitesse mesurée %.2f Hz, attendue %g", float64(changes)/(2*secs), rate)
	}
	// profondeur nulle : fréquence constante
	still := newModulation(fs, 1, 12, 0, rate, 1, 0, 1)
	freqs, times = freqTrack(wetOf(still, x), fs)
	var dmax float64
	for i, f := range freqs {
		if times[i] > 0.5 {
			dmax = math.Max(dmax, math.Abs(f-f0))
		}
	}
	if dmax > 0.05 {
		t.Errorf("profondeur nulle : écart de fréquence %.3f Hz, attendu 0", dmax)
	}
}

// Flanger statique : un retard de 44 échantillons à Mix = 1 creuse le peigne à 44 100 / 88 Hz (plus de 30 dB)
// et double le niveau (+6 dB) à 44 100 / 44 Hz, un pic du peigne.
func TestFlangerCombNull(t *testing.T) {
	const fs = 44100
	m := newModulation(fs, 1, 44.0/fs*1000, 0, 0.25, 1, 0, 1)
	null := sineF(fs/88.0, 8000, fs, 2*fs)
	out := make([]float64, len(null))
	fr := []float64{0}
	for i, v := range null {
		fr[0] = v
		m.apply(fr)
		out[i] = fr[0]
	}
	if g := 20 * math.Log10(rmsRange(out, fs, 2*fs)/rmsRange(null, fs, 2*fs)); g > -30 {
		t.Errorf("creux du peigne : %.1f dB, attendu moins de −30 dB", g)
	}
	peak := newModulation(fs, 1, 44.0/fs*1000, 0, 0.25, 1, 0, 1)
	sig := sineF(fs/44.0, 8000, fs, 2*fs) // une fréquence du peigne où les deux se somment
	for i, v := range sig {
		fr[0] = v
		peak.apply(fr)
		out[i] = fr[0]
	}
	if g := 20 * math.Log10(rmsRange(out, fs, 2*fs)/rmsRange(sig, fs, 2*fs)); math.Abs(g-6.02) > 0.3 {
		t.Errorf("pic du peigne : %+.2f dB, attendu +6,02", g)
	}
}

// Avec rétroaction, de ±0,95, la réponse à une impulsion décroît jusqu'à zéro exact.
func TestFlangerFeedbackDecays(t *testing.T) {
	const fs = 44100
	for _, fb := range []float64{0.95, -0.95} {
		m := newModulation(fs, 1, 2, 1.2, 0.3, 1, fb, 1)
		x := make([]float64, 8*fs)
		x[0] = 20000
		wet := wetOf(m, x)
		var peak, last float64
		for i, v := range wet {
			peak = math.Max(peak, math.Abs(v))
			if i >= 7*fs {
				last = math.Max(last, math.Abs(v))
			}
		}
		if peak < 1000 || peak > 40000 {
			t.Errorf("rétroaction %g : crête %.0f, attendue de l'ordre de l'impulsion", fb, peak)
		}
		if last != 0 {
			t.Errorf("rétroaction %g : après 7 s, valeur résiduelle %g, attendue 0", fb, last)
		}
	}
}

// Les voix sont moyennées : en ajouter ne rend pas l'effet plus fort ; en stéréo, la modulation de droite est
// décalée, donc deux canaux identiques en entrée ne le sont plus en sortie ; le niveau total reste proche du sec.
func TestChorusVoicesAndWidth(t *testing.T) {
	const fs = 44100
	x := sineF(660, 8000, fs, 3*fs)
	level := func(voices int) float64 {
		m := ChorusSettings{DelayMs: 15, DepthMs: 4, RateHz: 1.3, Voices: voices, Mix: 1}.stage(fs, 1).(*modulation)
		return rmsRange(wetOf(m, x), fs, 3*fs)
	}
	if l1, l4 := level(1), level(4); l4 > l1*1.05 {
		t.Errorf("4 voix : effet de %.0f contre %.0f pour 1 voix, ne doit pas être plus fort", l4, l1)
	}
	in := interleave(x, x)
	out, _ := io.ReadAll(Chorus(bytes.NewReader(in), fs, 2, ChorusSettings{Mix: 0.5}))
	l, r := floats(monoOf(out, 0)), floats(monoOf(out, 1))
	diff := make([]float64, len(l))
	for i := range l {
		diff[i] = l[i] - r[i]
	}
	if d := rmsRange(diff, fs, 3*fs) / rmsRange(l, fs, 3*fs); d < 0.01 {
		t.Errorf("gauche et droite quasi identiques (écart relatif %.4f) : l'effet doit élargir l'image", d)
	}
	if g := 20 * math.Log10(rmsRange(l, fs, 3*fs)/rmsRange(x, fs, 3*fs)); g < -3 || g > 3 {
		t.Errorf("niveau %+.1f dB par rapport au sec, attendu à ±3 dB", g)
	}
}

func TestModulationClampsStreamsAndKeepsLength(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 200, 90)[:30000], pluck(fs, -16, 300, 70)[:30000])
	read := func(r io.Reader) []byte { out, _ := io.ReadAll(r); return out }
	if !bytes.Equal(read(Chorus(bytes.NewReader(in), fs, 2, ChorusSettings{DelayMs: 999, DepthMs: 999, RateHz: 999, Voices: 99, Mix: 9})),
		read(Chorus(bytes.NewReader(in), fs, 2, ChorusSettings{DelayMs: 50, DepthMs: 49.5, RateHz: maxModRateHz, Voices: maxChorusVoices, Mix: 1}))) {
		t.Error("chorus : les valeurs hors bornes doivent être ramenées dedans")
	}
	if !bytes.Equal(read(Flanger(bytes.NewReader(in), fs, 2, FlangerSettings{DelayMs: 999, DepthMs: 999, RateHz: 999, Feedback: 9, Mix: 9})),
		read(Flanger(bytes.NewReader(in), fs, 2, FlangerSettings{DelayMs: 10, DepthMs: 9.8, RateHz: maxModRateHz, Feedback: 0.95, Mix: 1}))) {
		t.Error("flanger : les valeurs hors bornes doivent être ramenées dedans")
	}
	silence := make([]byte, 8*fs)
	for name, r := range map[string]io.Reader{
		"chorus":  Chorus(bytes.NewReader(silence), fs, 2, ChorusSettings{Mix: 0.5}),
		"flanger": Flanger(bytes.NewReader(silence), fs, 2, FlangerSettings{Feedback: 0.9, Mix: 0.5}),
	} {
		if !bytes.Equal(read(r), silence) {
			t.Errorf("%s : le silence doit rester silence", name)
		}
	}
	for name, mk := range map[string]func(io.Reader) io.Reader{
		"chorus":  func(r io.Reader) io.Reader { return Chorus(r, fs, 2, ChorusSettings{Voices: 4, Mix: 0.6}) },
		"flanger": func(r io.Reader) io.Reader { return Flanger(r, fs, 2, FlangerSettings{Feedback: -0.6, Mix: 0.6}) },
	} {
		want := read(mk(bytes.NewReader(in)))
		if len(want) != len(in) || bytes.Equal(want, in) {
			t.Fatalf("%s : longueur %d/%d, identique=%v", name, len(want), len(in), bytes.Equal(want, in))
		}
		if got := read(iotest.OneByteReader(mk(bytes.NewReader(in)))); !bytes.Equal(got, want) {
			t.Errorf("%s : la lecture fragmentée doit donner le même résultat", name)
		}
	}
	// signal fort : plafond doux, aucun écrêtage
	hot := interleave(pluck(fs, -0.3, 200, 300), pluck(fs, -0.3, 260, 300))
	for _, v := range samples(read(Flanger(bytes.NewReader(hot), fs, 2, FlangerSettings{Feedback: 0.8, Mix: 1}))) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
	// basse fréquence d'échantillonnage : retard minimal respecté, pas de panique
	low := tone(500, 8000, 8000, 4000)
	if out, _ := io.ReadAll(Flanger(bytes.NewReader(low), 8000, 1, FlangerSettings{DelayMs: 0.3, DepthMs: 0.1, Mix: 1})); len(out) != len(low) {
		t.Error("8 kHz : la longueur doit être conservée")
	}
}
