package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func lofiRun(t *testing.T, in []byte, ch int, s LoFiSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(LoFi(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLoFiNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]LoFiSettings{
		"vide":                       {},
		"valeurs négatives":          {LowCutHz: -5, HighCutHz: -9, Bits: -3, SampleRateHz: -1, Wobble: -1, DriveDB: -2, Crackle: -1, Hiss: -1},
		"NaN":                        {LowCutHz: math.NaN(), HighCutHz: math.NaN(), SampleRateHz: math.NaN(), Wobble: math.NaN(), DriveDB: math.NaN(), Crackle: math.NaN(), Hiss: math.NaN()},
		"16 bits":                    {Bits: 16},
		"fréquence >= celle du flux": {SampleRateHz: 44100},
		"coupures >= Nyquist":        {LowCutHz: 30000, HighCutHz: 30000},
	} {
		if out := lofiRun(t, in, 2, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(LoFi(bytes.NewReader(in), 0, 2, LoFiSettings{Bits: 8})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Quantification seule : chaque échantillon est un multiple du pas, et le rapport signal / bruit suit 6,02 × bits − 4,26 dB
// pour un sinus à −6 dBFS (6,02 × bits + 1,76 dB à pleine échelle).
func TestLoFiBitReduction(t *testing.T) {
	const fs = 44100
	in := tone(997, ampOf(-6), fs, 2*fs)
	for _, bits := range []int{4, 8, 12} {
		out := lofiRun(t, in, 1, LoFiSettings{Bits: bits})
		step := int16(1) << (16 - bits)
		var sig, errE float64
		a, o := samples(in), samples(out)
		for i := range a {
			if o[i]%step != 0 {
				t.Fatalf("%d bits : échantillon %d = %d n'est pas un multiple de %d", bits, i, o[i], step)
			}
			d := float64(o[i]) - float64(a[i])
			sig += float64(a[i]) * float64(a[i])
			errE += d * d
		}
		snr, want := 10*math.Log10(sig/errE), 6.02*float64(bits)-4.26
		t.Logf("%d bits : rapport signal / bruit %.1f dB (théorie %.1f dB)", bits, snr, want)
		if math.Abs(snr-want) > 3 {
			t.Errorf("%d bits : %.1f dB, attendu %.1f ± 3", bits, snr, want)
		}
	}
}

// Réduction de la fréquence d'échantillonnage : valeurs tenues (rapport entier), nombre de changements par seconde
// (rapport non entier), et repliement des aigus dans le spectre audible.
func TestLoFiSampleRateReduction(t *testing.T) {
	const fs = 44100
	noise := make([]float64, 2*fs)
	seed := uint32(11)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = float64(int16(seed>>16)) / 2
	}
	// rapport entier (44 100 → 11 025) : groupes de 4 échantillons identiques
	o := samples(lofiRun(t, interleave(noise), 1, LoFiSettings{SampleRateHz: 11025}))
	for i := range o {
		if o[i] != o[i-i%4] {
			t.Fatalf("11 025 Hz : l'échantillon %d diffère du début de son groupe de 4", i)
		}
	}
	// rapport non entier (44 100 → 8 000, soit 5,5125) : environ 8 000 valeurs nouvelles par seconde
	o = samples(lofiRun(t, interleave(noise), 1, LoFiSettings{SampleRateHz: 8000}))
	changes := 0
	for i := 1; i < len(o); i++ {
		if o[i] != o[i-1] {
			changes++
		}
	}
	if per := float64(changes) / 2; math.Abs(per/8000-1) > 0.02 {
		t.Errorf("8 000 Hz : %.0f valeurs nouvelles par seconde, attendu 8 000 à 2 %% près", per)
	}
	// repliement : 9 kHz échantillonné à 11 025 Hz → 2 025 Hz ; 5 kHz à 8 000 Hz → 3 000 Hz
	for _, tc := range []struct{ f, sr, alias float64 }{{9000, 11025, 2025}, {5000, 8000, 3000}} {
		in := tone(tc.f, ampOf(-10), fs, 2*fs)
		out := lofiRun(t, in, 1, LoFiSettings{SampleRateHz: tc.sr})
		alias := 20 * math.Log10(goertzel(out, tc.alias, fs)/goertzel(in, tc.f, fs))
		t.Logf("%g Hz à %g Hz : repliement à %g Hz = %.1f dBc", tc.f, tc.sr, tc.alias, alias)
		if alias < -20 {
			t.Errorf("%g Hz échantillonné à %g Hz : repliement à %g Hz de %.1f dBc, attendu au-dessus de −20", tc.f, tc.sr, tc.alias, alias)
		}
	}
}

// Bande passante : passe-haut d'ordre 2 et passe-bas d'ordre 4 (−24 dB à deux octaves de la coupure).
func TestLoFiBandwidth(t *testing.T) {
	const fs = 44100
	gain := func(freq float64, s LoFiSettings) float64 {
		in := tone(freq, ampOf(-12), fs, 2*fs)
		return 20 * math.Log10(goertzel(lofiRun(t, in, 1, s), freq, fs)/goertzel(in, freq, fs))
	}
	s := LoFiSettings{LowCutHz: 200, HighCutHz: 3000}
	if g := gain(50, s); math.Abs(g+24.1) > 1.5 { // −10·log10(1 + 4⁴)
		t.Errorf("50 Hz (deux octaves sous 200 Hz, ordre 2) : %.1f dB, attendu ≈ −24", g)
	}
	if g := gain(6000, s); math.Abs(g+24.1) > 2 { // une octave au-dessus de 3 kHz, ordre 4 : −10·log10(1 + 2⁸)
		t.Errorf("6 kHz (une octave au-dessus de 3 kHz, ordre 4) : %.1f dB, attendu ≈ −24", g)
	}
	if g := gain(800, s); math.Abs(g) > 0.5 {
		t.Errorf("800 Hz dans la bande : %.2f dB, attendu intact", g)
	}
}

// Saturation de bande : transparente à petit signal, des harmoniques à fort signal.
func TestLoFiDrive(t *testing.T) {
	const fs = 44100
	small := tone(1000, ampOf(-50), fs, 2*fs)
	if g := levelAt(lofiRun(t, small, 1, LoFiSettings{DriveDB: 12}), 1000, fs) - levelAt(small, 1000, fs); math.Abs(g) > 0.1 {
		t.Errorf("petit signal : %+.2f dB, attendu transparent", g)
	}
	loud := tone(1000, ampOf(-10), fs, 2*fs)
	out := lofiRun(t, loud, 1, LoFiSettings{DriveDB: 12})
	if h3 := levelAt(out, 3000, fs) - levelAt(out, 1000, fs); h3 < -50 || h3 > -10 {
		t.Errorf("3e harmonique à %.1f dBc, attendue entre −50 et −10", h3)
	}
}

// Wow et flutter : l'écart de hauteur mesuré par les passages à zéro d'un 1 kHz vaut l'écart demandé, à chacune des
// deux vitesses ; il est nul sans Wobble, et le flux garde son alignement.
func TestLoFiWobblePitchDeviation(t *testing.T) {
	const fs = 44100
	const f0 = 1000.0
	dev := func(wow, flutter float64) (peak, mean, rate float64) {
		l := newLoFi(fs, 1, LoFiSettings{}, wow, flutter)
		x := sineF(f0, 8000, fs, 6*fs)
		out := make([]float64, len(x))
		fr := []float64{0}
		for i, v := range x {
			fr[0] = v
			l.apply(fr)
			out[i] = fr[0]
		}
		freqs, times := freqTrack(out, fs)
		var kept []float64
		lo, hi := math.Inf(1), math.Inf(-1)
		for i, f := range freqs {
			if times[i] < 0.5 || times[i] > 5.5 { // hors des premiers instants et de la fin, où la ligne à retard n'a plus de signal à lire
				continue
			}
			kept = append(kept, f)
			lo, hi = math.Min(lo, f), math.Max(hi, f)
			mean += f
		}
		mean /= float64(len(kept))
		changes := 0
		for i := 1; i < len(kept); i++ {
			if (kept[i-1]-mean)*(kept[i]-mean) < 0 {
				changes++
			}
		}
		return (hi - lo) / 2, mean, float64(changes) / 2 / (float64(len(kept)) / f0)
	}
	// wow seul : ±0,4 % de 1 kHz = ±4 Hz, à 0,6 Hz
	p, m, r := dev(lofiWowDeviation, 0)
	t.Logf("wow : écart %.2f Hz (attendu %.2f), moyenne %.3f Hz, vitesse %.2f Hz", p, f0*lofiWowDeviation, m, r)
	if math.Abs(p/(f0*lofiWowDeviation)-1) > 0.1 || math.Abs(m-f0) > 0.6 || math.Abs(r-lofiWowRateHz) > 0.2 {
		t.Errorf("wow : écart %.2f Hz (attendu %.2f), moyenne %.3f, vitesse %.2f (attendue %g)", p, f0*lofiWowDeviation, m, r, lofiWowRateHz)
	}
	// flutter seul : ±0,12 % = ±1,2 Hz, à 7,3 Hz
	p, m, r = dev(0, lofiFlutterDev)
	t.Logf("flutter : écart %.2f Hz (attendu %.2f), moyenne %.3f Hz, vitesse %.2f Hz", p, f0*lofiFlutterDev, m, r)
	if math.Abs(p/(f0*lofiFlutterDev)-1) > 0.15 || math.Abs(m-f0) > 0.3 || math.Abs(r-lofiFlutterRateHz) > 0.8 {
		t.Errorf("flutter : écart %.2f Hz (attendu %.2f), moyenne %.3f, vitesse %.2f (attendue %g)", p, f0*lofiFlutterDev, m, r, lofiFlutterRateHz)
	}
	// via l'API : Wobble = 1 additionne les deux, Wobble = 0,5 en fait la moitié
	full := LoFiSettings{Wobble: 1}
	pk := func(s LoFiSettings) float64 {
		out := floats(lofiRun(t, interleave(sineF(f0, 8000, fs, 6*fs)), 1, s))
		freqs, times := freqTrack(out, fs)
		lo, hi := math.Inf(1), math.Inf(-1)
		for i, f := range freqs {
			if times[i] > 0.5 && times[i] < 5.5 {
				lo, hi = math.Min(lo, f), math.Max(hi, f)
			}
		}
		return (hi - lo) / 2
	}
	pf, ph := pk(full), pk(LoFiSettings{Wobble: 0.5})
	t.Logf("Wobble 1 : écart %.2f Hz ; Wobble 0,5 : %.2f Hz", pf, ph)
	if pf < 3.5 || pf > 6.5 || math.Abs(ph/pf-0.5) > 0.12 {
		t.Errorf("Wobble 1 : %.2f Hz (attendu 3,5 à 6,5), Wobble 0,5 : %.2f Hz (attendu la moitié)", pf, ph)
	}
}

// Le retard moyen de la ligne de hauteur est rattrapé : des impulsions ressortent à leur trame, à ±1,2 ms près (la
// dérive de hauteur elle-même), et en moyenne à ±15 trames ; la longueur est conservée.
func TestLoFiWobbleKeepsAlignment(t *testing.T) {
	const fs = 44100
	var pos []int
	v := make([]float64, 6*fs)
	for k := 1; k <= 10; k++ {
		p := k * fs / 2
		pos = append(pos, p)
		v[p] = 20000
	}
	out := samples(lofiRun(t, interleave(v), 1, LoFiSettings{Wobble: 1}))
	if len(out) != len(v) {
		t.Fatalf("longueur %d, attendu %d", len(out), len(v))
	}
	var sum float64
	for _, p := range pos {
		best := p - 100
		for i := p - 100; i <= p+100; i++ {
			if out[i] > out[best] {
				best = i
			}
		}
		if d := best - p; d > 55 || d < -55 {
			t.Errorf("impulsion de la trame %d ressortie à la trame %d (écart %d, attendu ±55)", p, best, d)
		} else {
			sum += float64(d)
		}
	}
	if m := sum / float64(len(pos)); math.Abs(m) > 15 {
		t.Errorf("décalage moyen %.1f trames, attendu ≈ 0", m)
	}
}

// Souffle : niveau efficace annoncé, indépendant entre les canaux ; craquements : nombre par seconde, amplitude, partagés
// entre les canaux.
func TestLoFiNoise(t *testing.T) {
	const fs = 44100
	silence2 := make([]byte, 4*10*fs)
	for _, tc := range []struct{ hiss, wantDB float64 }{{1, -40}, {0.5, -50}} {
		out := lofiRun(t, silence2, 2, LoFiSettings{Hiss: tc.hiss})
		l, r := floats(monoOf(out, 0)), floats(monoOf(out, 1))
		if got := 20 * math.Log10(rmsRange(l, fs, 10*fs)/32768); math.Abs(got-tc.wantDB) > 1.5 {
			t.Errorf("Hiss %g : %.1f dBFS efficaces, attendu %g ± 1,5", tc.hiss, got, tc.wantDB)
		}
		var lr float64
		for i := fs; i < 10*fs; i++ {
			lr += l[i] * r[i]
		}
		if c := lr / (rmsRange(l, fs, 10*fs) * rmsRange(r, fs, 10*fs) * 9 * fs); math.Abs(c) > 0.05 {
			t.Errorf("Hiss %g : corrélation de %.3f entre les canaux, attendue ≈ 0", tc.hiss, c)
		}
	}
	// craquements : un événement = le niveau absolu monte de plus de 100 LSB d'un échantillon à l'autre (le début d'un
	// craquement ; la décroissance qui suit fait baisser le niveau absolu)
	silence1 := make([]byte, 2*20*fs)
	for _, tc := range []struct{ crackle, perSec float64 }{{1, 20}, {0.3, 7.4}} {
		o := samples(lofiRun(t, silence1, 1, LoFiSettings{Crackle: tc.crackle}))
		events, peak := 0, 0.0
		for i := 1; i < len(o); i++ {
			if math.Abs(float64(o[i]))-math.Abs(float64(o[i-1])) > 100 {
				events++
			}
			peak = math.Max(peak, math.Abs(float64(o[i])))
		}
		per := float64(events) / 20
		t.Logf("Crackle %g : %.1f craquements par seconde (attendu %.1f), crête %.0f", tc.crackle, per, tc.perSec, peak)
		if math.Abs(per/tc.perSec-1) > 0.2 {
			t.Errorf("Crackle %g : %.1f craquements par seconde, attendu %.1f à 20 %% près (graine fixe : voir TestLoFiCrackleDensityAcrossSeeds)", tc.crackle, per, tc.perSec)
		}
		if peak > ampOf(-18)*2.2 || peak < ampOf(-45) {
			t.Errorf("Crackle %g : crête %.0f hors de la plage attendue", tc.crackle, peak)
		}
	}
	// les craquements sont partagés par les canaux (même sillon) : gauche et droite identiques
	out := lofiRun(t, silence2[:4*5*fs], 2, LoFiSettings{Crackle: 1})
	if !bytes.Equal(channelBytes(out, 0), channelBytes(out, 1)) {
		t.Error("les craquements doivent être identiques à gauche et à droite")
	}
}

// La densité des craquements suit 2 + 18 × Crackle par seconde en moyenne sur plusieurs graines : l'écart d'un
// passage isolé à graine fixe n'est que le hasard d'un processus de Poisson (de l'ordre de 5 % sur 20 s).
func TestLoFiCrackleDensityAcrossSeeds(t *testing.T) {
	const fs = 44100
	for _, c := range []float64{0.3, 1} {
		want := (lofiCrackleBaseHz + lofiCrackleSpanHz*c) * 20
		var total float64
		const seeds = 30
		for seed := uint64(1); seed <= seeds; seed++ {
			l := newLoFi(fs, 1, LoFiSettings{Crackle: c}, 0, 0)
			l.rng = seed * 0x9E3779B97F4A7C15
			for i := 0; i < 20*fs; i++ {
				if l.uniform() < l.crack {
					total++
					l.uniform()
					l.uniform()
				}
			}
		}
		if mean := total / seeds; math.Abs(mean/want-1) > 0.05 {
			t.Errorf("Crackle %g : %.1f craquements en 20 s en moyenne, attendu %.1f à 5 %% près", c, mean, want)
		}
	}
}

func channelBytes(b []byte, ch int) []byte { return monoOf(b, ch) }

// Reproductible : deux passages (et une lecture octet par octet) donnent le même flux. Sans bruit, le silence reste silence ;
// avec du bruit, il n'en reste pas.
func TestLoFiDeterminismAndSilence(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -14, 200, 90)[:30000], pluck(fs, -16, 300, 70)[:30000])
	s := LoFiSettings{LowCutHz: 120, HighCutHz: 5000, Bits: 10, SampleRateHz: 11025, Wobble: 0.6, DriveDB: 6, Crackle: 0.5, Hiss: 0.4}
	a, b := lofiRun(t, in, 2, s), lofiRun(t, in, 2, s)
	if !bytes.Equal(a, b) {
		t.Error("deux passages sur le même flux doivent donner le même résultat")
	}
	if len(a) != len(in) || bytes.Equal(a, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(a), len(in), bytes.Equal(a, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(LoFi(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, a) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
	silence := make([]byte, 4*3*fs)
	clean := LoFiSettings{LowCutHz: 120, HighCutHz: 5000, Bits: 8, SampleRateHz: 8000, Wobble: 1, DriveDB: 12}
	if out := lofiRun(t, silence, 2, clean); !bytes.Equal(out, silence) {
		t.Error("sans bruit, le silence doit rester silence")
	}
	for _, noisy := range []LoFiSettings{{Hiss: 0.3}, {Crackle: 0.5}} {
		if bytes.Equal(lofiRun(t, silence, 2, noisy), silence) {
			t.Errorf("%+v : l'effet ajoute du bruit, le silence ne peut pas rester silence", noisy)
		}
	}
	// signal fort : plafond doux, aucun écrêtage
	hot := interleave(pluck(fs, -0.3, 200, 300), pluck(fs, -0.3, 260, 300))
	for _, v := range samples(lofiRun(t, hot, 2, LoFiSettings{DriveDB: 24, Crackle: 1, Hiss: 1})) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
}
