package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

// wetIR renvoie la réponse impulsionnelle (flottante, sans quantification) de la partie mouillée
// d'un canal : Mix = 1, une impulsion de 1 à la première trame.
func wetIR(t *testing.T, s ReverbSettings, fs float64, seconds float64) []float64 {
	t.Helper()
	s.Mix = 1
	st := s.stage(fs, 1)
	if st == nil {
		t.Fatal("étage absent")
	}
	n := int(seconds * fs)
	ir := make([]float64, n)
	frame := []float64{0}
	for i := range ir {
		frame[0] = 0
		if i == 0 {
			frame[0] = 1
		}
		st.apply(frame)
		ir[i] = frame[0]
	}
	return ir
}

// rt60 estime la durée de réverbération par l'intégrale de Schroeder : temps mis par la courbe
// d'extinction pour passer de −5 à −25 dB, triplé.
func rt60(ir []float64, fs float64) float64 {
	edc := make([]float64, len(ir))
	var sum float64
	for i := len(ir) - 1; i >= 0; i-- {
		sum += ir[i] * ir[i]
		edc[i] = sum
	}
	var t5, t25 float64
	for i, e := range edc {
		db := 10 * math.Log10(e/edc[0])
		if t5 == 0 && db <= -5 {
			t5 = float64(i)
		}
		if t25 == 0 && db <= -25 {
			t25 = float64(i)
			break
		}
	}
	return 3 * (t25 - t5) / fs
}

func TestReverbNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]ReverbSettings{
		"mix nul": {RoomSize: 0.8, Damping: 0.3, Width: 1, Mix: 0},
		"mix NaN": {RoomSize: 0.8, Mix: math.NaN()},
		"mix < 0": {RoomSize: 0.8, Mix: -1},
		"zéro":    {},
	} {
		out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 2, s))
		if !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Reverb(bytes.NewReader(in), 0, 2, ReverbSettings{Mix: 0.5})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// La durée de la queue croît avec la taille de la pièce (sans dépendre des longueurs exactes des filtres).
func TestReverbDecayGrowsWithRoomSize(t *testing.T) {
	const fs = 44100.0
	var prev float64
	var times []float64
	for _, room := range []float64{0.1, 0.4, 0.7, 0.95} {
		d := rt60(wetIR(t, ReverbSettings{RoomSize: room, Damping: 0.3}, fs, 20), fs)
		times = append(times, d)
		if d <= prev {
			t.Errorf("taille %.2f : RT60 %.2f s, doit dépasser celui de la taille précédente (%.2f s)", room, d, prev)
		}
		prev = d
	}
	t.Logf("RT60 estimé pour des pièces de taille 0,1 / 0,4 / 0,7 / 0,95 : %.2f / %.2f / %.2f / %.2f s", times[0], times[1], times[2], times[3])
	if times[3] < 4*times[0] {
		t.Errorf("la plus grande pièce (%.2f s) doit durer au moins 4 fois la plus petite (%.2f s)", times[3], times[0])
	}
}

// L'amortissement raccourcit la queue des aigus bien plus que celle des graves.
func TestReverbDampingShortensHighFrequencies(t *testing.T) {
	const fs = 44100.0
	energyRatio := func(damp float64) float64 { // aigus / graves dans la queue (de 0,6 s à 1,6 s)
		ir := wetIR(t, ReverbSettings{RoomSize: 0.8, Damping: damp}, fs, 2)
		pcm := make([]byte, 0, 2*len(ir))
		for _, v := range ir {
			pcm = append(pcm, byte(uint16(int16(math.Round(v*3e6)))), byte(uint16(int16(math.Round(v*3e6)))>>8))
		}
		hi, _ := io.ReadAll(HighPassFilter(bytes.NewReader(pcm), int(fs), 1, 4000, 4))
		lo, _ := io.ReadAll(LowPassFilter(bytes.NewReader(pcm), int(fs), 1, 1000, 4))
		from, to := int(0.6*fs)*2, int(1.6*fs)*2
		e := func(b []byte) float64 {
			var s float64
			for i := from; i+1 < to; i += 2 {
				v := float64(int16(uint16(b[i]) | uint16(b[i+1])<<8))
				s += v * v
			}
			return s
		}
		return 10 * math.Log10(e(hi)/e(lo))
	}
	clear, felt := energyRatio(0), energyRatio(1)
	t.Logf("aigus / graves dans la queue : %.1f dB (amortissement 0), %.1f dB (amortissement 1)", clear, felt)
	if felt > clear-6 {
		t.Errorf("amortissement 1 : %.1f dB contre %.1f dB sans, attendu au moins 6 dB de moins d'aigus", felt, clear)
	}
}

// Le niveau de la partie réverbérée est calibré : un bruit blanc garde sa puissance quelles que
// soient la taille de la pièce, l'amortissement et la largeur.
func TestReverbWetLevelIsIndependentOfSettings(t *testing.T) {
	const fs = 44100
	n := 12 * fs
	noise := make([]float64, n)
	seed := uint32(99)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = math.Round(float64(int16(seed>>16)) * 0.15) // bruit blanc, RMS ≈ 2800
	}
	in := interleave(noise)
	rmsIn := rmsRange(noise, 3*fs, n)
	for _, s := range []ReverbSettings{
		{RoomSize: 0.1, Damping: 0.2}, {RoomSize: 0.5, Damping: 0.5}, {RoomSize: 0.9, Damping: 0.8},
		{RoomSize: 0.5, Damping: 0.2, PreDelayMs: 40}, {RoomSize: 0.9, Damping: 0.1},
	} {
		s.Mix = 1
		out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, s))
		got := 20 * math.Log10(rmsRange(floats(out), 3*fs, n)/rmsIn)
		if math.Abs(got) > 1.5 {
			t.Errorf("%+v : puissance du mouillé %+.2f dB par rapport à l'entrée, attendu ±1,5 dB", s, got)
		}
	}
}

// Retard préalable : rien n'est audible avant, et la première réponse arrive peu après.
func TestReverbPreDelay(t *testing.T) {
	const fs = 44100.0
	for _, ms := range []float64{0, 20, 80} {
		ir := wetIR(t, ReverbSettings{RoomSize: 0.5, Damping: 0.3, PreDelayMs: ms}, fs, 1)
		first := -1
		for i, v := range ir {
			if v != 0 {
				first = i
				break
			}
		}
		pre := int(math.Round(ms / 1000 * fs))
		if first < pre || first > pre+3000 {
			t.Errorf("retard de %g ms : première réponse à la trame %d, attendu entre %d et %d", ms, first, pre, pre+3000)
		}
	}
}

// Largeur 0 : les deux canaux de la partie réverbérée sont identiques ; largeur 1 : ils diffèrent.
func TestReverbWidth(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 220, 100), pluck(fs, -16, 340, 70))
	for _, tc := range []struct {
		width     float64
		identical bool
	}{{0, true}, {1, false}} {
		out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 2, ReverbSettings{RoomSize: 0.6, Damping: 0.3, Width: tc.width, Mix: 1}))
		l, r := channel(out, 0), channel(out, 1)
		same := true
		var worst int
		for i := range l {
			d := int(l[i]) - int(r[i])
			if d < 0 {
				d = -d
			}
			if d > worst {
				worst = d
			}
			if d > 1 {
				same = false
			}
		}
		if same != tc.identical {
			t.Errorf("largeur %g : canaux identiques=%v (écart max %d), attendu %v", tc.width, same, worst, tc.identical)
		}
	}
}

// Silence en entrée : silence en sortie, exactement. Et après un signal, la queue s'éteint
// jusqu'à zéro exact, sans rester coincée dans des valeurs minuscules (nombres dénormalisés).
func TestReverbSilenceAndTailReachesZero(t *testing.T) {
	const fs = 44100
	silence := make([]byte, 4*fs*2)
	for _, mix := range []float64{0.3, 1} {
		out, _ := io.ReadAll(Reverb(bytes.NewReader(silence), fs, 2, ReverbSettings{RoomSize: 0.7, Mix: mix}))
		if !bytes.Equal(out, silence) {
			t.Errorf("mix %g : le silence doit rester silence", mix)
		}
	}
	// un coup, puis 8 s de silence : une petite pièce s'éteint
	burst := pluck(fs, -6, 200, 60)
	n := 10 * fs
	pad := make([]float64, n)
	copy(pad, burst[fs/2:fs/2+fs/2])
	in := interleave(pad)
	out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, ReverbSettings{RoomSize: 0.2, Damping: 0.5, Mix: 1}))
	tail := samples(out)[8*fs:]
	for i, v := range tail {
		if v != 0 {
			t.Fatalf("la queue doit être éteinte 8 s après le signal : échantillon %d = %d", i, v)
		}
	}
	// en interne : après un long silence, aucun état n'est resté dans la zone des valeurs minuscules
	st := ReverbSettings{RoomSize: 0.3, Mix: 0.5}.stage(fs, 1).(*reverb)
	st.apply([]float64{20000})
	for i := 0; i < 20*fs; i++ {
		st.apply([]float64{0})
	}
	for _, b := range st.banks {
		for _, c := range b.combs {
			for _, v := range c.buf {
				if v != 0 && math.Abs(v) < reverbFlush {
					t.Fatalf("valeur minuscule non nulle (%g) : risque de nombres dénormalisés", v)
				}
			}
		}
	}
}

// 44,1 et 48 kHz donnent des durées de queue voisines (les longueurs sont mises à l'échelle).
func TestReverbDecayIsSampleRateIndependent(t *testing.T) {
	s := ReverbSettings{RoomSize: 0.6, Damping: 0.3}
	a := rt60(wetIR(t, s, 44100, 12), 44100)
	b := rt60(wetIR(t, s, 48000, 12), 48000)
	c := rt60(wetIR(t, s, 96000, 12), 96000)
	t.Logf("RT60 : %.2f s à 44,1 kHz, %.2f s à 48 kHz, %.2f s à 96 kHz", a, b, c)
	if math.Abs(b/a-1) > 0.15 || math.Abs(c/a-1) > 0.15 {
		t.Errorf("durées %.2f / %.2f / %.2f s : écart > 15 %%", a, b, c)
	}
}

// Mix : mélange à puissance constante entre le sec et le mouillé (cos et sin).
func TestReverbMixIsEqualPower(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -20, 200, 100))
	opts := ReverbSettings{RoomSize: 0.5, Damping: 0.4}
	wet, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, withMix(opts, 1)))
	d, w := floats(in), floats(wet)
	for _, mix := range []float64{0.25, 0.5, 0.8} {
		half, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, withMix(opts, mix)))
		h := floats(half)
		var worst float64
		for i := range h {
			want := math.Cos(mix*math.Pi/2)*d[i] + math.Sin(mix*math.Pi/2)*w[i]
			worst = math.Max(worst, math.Abs(h[i]-want))
		}
		if worst > 2 { // arrondis de l'entrée entière et de chaque sortie
			t.Errorf("mix %g : écart maximal %.1f LSB avec cos·sec + sin·mouillé", mix, worst)
		}
	}
}

// Sur un bruit blanc, le volume ne change presque pas quand on monte le mélange : c'est l'intérêt de
// la loi de puissance constante (un fondu linéaire ferait baisser de 3 dB à Mix = 0,5).
func TestReverbKeepsLoudnessAcrossMix(t *testing.T) {
	const fs = 44100
	n := 12 * fs
	noise := make([]float64, n)
	seed := uint32(5)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = math.Round(float64(int16(seed>>16)) * 0.15)
	}
	in := interleave(noise)
	ref := rmsRange(noise, 3*fs, n)
	for _, mix := range []float64{0.2, 0.5, 0.8} {
		out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, ReverbSettings{RoomSize: 0.6, Damping: 0.3, Mix: mix}))
		if g := 20 * math.Log10(rmsRange(floats(out), 3*fs, n)/ref); math.Abs(g) > 1.2 {
			t.Errorf("mix %g : volume %+.2f dB, attendu ±1,2", mix, g)
		}
	}
}

func withMix(s ReverbSettings, m float64) ReverbSettings { s.Mix = m; return s }

// Plafond doux : un signal fort avec beaucoup de réverbération ne fait jamais écrêter.
func TestReverbNoHardClip(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -0.3, 150, 300), pluck(fs, -0.3, 250, 300))
	out, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 2, ReverbSettings{RoomSize: 0.9, Width: 1, Mix: 0.6}))
	for _, v := range samples(out) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
}

func TestReverbClampsSettings(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 180, 90))[:2*fs/2]
	a, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, ReverbSettings{RoomSize: 7, Damping: -3, Width: 9, PreDelayMs: 9999, Mix: 4}))
	b, _ := io.ReadAll(Reverb(bytes.NewReader(in), fs, 1, ReverbSettings{RoomSize: 1, Damping: 0, Width: 1, PreDelayMs: 200, Mix: 1}))
	if !bytes.Equal(a, b) {
		t.Error("les valeurs hors bornes doivent être ramenées dedans")
	}
}

// Longueur conservée (la queue est coupée), lecture fragmentée identique, mono, stéréo et 3 canaux.
func TestReverbStreamingAndChannels(t *testing.T) {
	const fs = 44100
	s := ReverbSettings{RoomSize: 0.6, Damping: 0.4, Width: 0.8, PreDelayMs: 15, Mix: 0.35}
	for _, ch := range []int{1, 2, 3} {
		chans := make([][]float64, ch)
		for i := range chans {
			chans[i] = pluck(fs, -14-float64(i), 180+60*float64(i), 90)[:30000]
		}
		in := interleave(chans...)
		want, err := io.ReadAll(Reverb(bytes.NewReader(in), fs, ch, s))
		if err != nil || len(want) != len(in) {
			t.Fatalf("%d canaux : err=%v longueur %d/%d", ch, err, len(want), len(in))
		}
		if bytes.Equal(want, in) {
			t.Errorf("%d canaux : le traitement doit modifier le signal", ch)
		}
		got, _ := io.ReadAll(iotest.OneByteReader(Reverb(bytes.NewReader(in), fs, ch, s)))
		if !bytes.Equal(got, want) {
			t.Errorf("%d canaux : la lecture fragmentée doit donner le même résultat", ch)
		}
	}
}
