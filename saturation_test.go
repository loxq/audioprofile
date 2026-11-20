package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

// goertzel renvoie l'amplitude de la composante à freq dans la 2e moitié du signal.
func goertzel(b []byte, freq float64, fs int) float64 {
	n := len(b) / 2
	var re, im float64
	start := n / 2
	for i := start; i < n; i++ {
		v := float64(int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8))
		a := 2 * math.Pi * freq * float64(i) / float64(fs)
		re += v * math.Cos(a)
		im += v * math.Sin(a)
	}
	return 2 * math.Hypot(re, im) / float64(n-start)
}

// strongSine renvoie un sinus mono d'amplitude ≈ amp (échelle int16).
func strongSine(freq, amp float64, fs, n int) []byte { return tone(freq, amp, fs, n) }

func mustSat(t *testing.T, fs float64, s saturation) *saturator {
	t.Helper()
	sats := newSaturators(fs, 1, s)
	if sats == nil {
		t.Fatalf("saturation %+v : étage attendu", s)
	}
	return sats[0]
}

func TestSaturatorShape(t *testing.T) {
	s := mustSat(t, 44100, saturation{driveDB: 6})
	g := math.Pow(10, 6.0/20)
	if got := s.shape(1); math.Abs(got-1) > 1e-9 {
		t.Errorf("pleine échelle : %.6f, attendu 1", got)
	}
	// signaux faibles : gain ≈ g/tanh(g)
	want := g / math.Tanh(g)
	if got := s.shape(0.001) / 0.001; math.Abs(got-want) > 0.01*want {
		t.Errorf("petit signal : gain %.3f, attendu %.3f", got, want)
	}
	prev := math.Inf(-1)
	for x := -1.0; x <= 1; x += 0.01 { // monotone et impaire
		v := s.shape(x)
		if v < prev {
			t.Fatalf("non monotone en %v", x)
		}
		if math.Abs(s.shape(-x)+v) > 1e-9 {
			t.Fatalf("non impaire en %v", x)
		}
		prev = v
	}
}

func TestSaturatorBiasShape(t *testing.T) {
	s := mustSat(t, 44100, saturation{driveDB: 9, bias: 0.3})
	if s.shape(0) != 0 {
		t.Errorf("shape(0) = %v : le silence doit rester le silence", s.shape(0))
	}
	// aucune des deux faces ne dépasse la pleine échelle, et l'une l'atteint
	hi, lo := s.shape(1), s.shape(-1)
	if math.Max(math.Abs(hi), math.Abs(lo)) > 1+1e-9 || math.Abs(math.Max(math.Abs(hi), math.Abs(lo))-1) > 1e-9 {
		t.Errorf("pleine échelle mal normalisée : %.6f / %.6f", hi, lo)
	}
	// asymétrique : les deux alternances ne sont pas symétriques
	if math.Abs(s.shape(0.5)+s.shape(-0.5)) < 0.05 {
		t.Errorf("courbe symétrique malgré le biais (%.4f, %.4f)", s.shape(0.5), s.shape(-0.5))
	}
	prev := math.Inf(-1)
	for x := -1.0; x <= 1; x += 0.01 {
		if v := s.shape(x); v < prev {
			t.Fatalf("non monotone en %v", x)
		} else {
			prev = v
		}
	}
}

func TestNewSaturators(t *testing.T) {
	for name, s := range map[string]saturation{
		"sans effet": {}, "drive NaN": {driveDB: math.NaN()}, "sortie NaN": {outputDB: math.NaN()},
		"biais NaN": {driveDB: 3, bias: math.NaN()}, "drive négatif": {driveDB: -3},
	} {
		if newSaturators(44100, 2, s) != nil {
			t.Errorf("%s : nil attendu", name)
		}
	}
	if newSaturators(0, 2, saturation{driveDB: 3}) != nil || newSaturators(44100, 0, saturation{driveDB: 3}) != nil {
		t.Error("fs <= 0 ou 0 canal : nil attendu")
	}
	sats := newSaturators(44100, 3, saturation{driveDB: 3, bias: 0.2})
	if len(sats) != 3 || sats[0] == sats[1] || sats[1] == sats[2] {
		t.Error("un saturateur distinct par canal attendu")
	}
	// gain de sortie seul : constant, sans suréchantillonnage
	if got := mustSat(t, 44100, saturation{outputDB: -6}).apply(10000) / 10000; math.Abs(got-math.Pow(10, -6.0/20)) > 1e-9 {
		t.Errorf("gain de sortie seul : %.4f", got)
	}
}

func loudSine(freq float64, fs int) []byte { return strongSine(freq, 20000, fs, fs) }

func TestSaturationAddsOddHarmonics(t *testing.T) {
	const fs = 44100
	in := loudSine(200, fs)
	sat, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, nil, &saturation{driveDB: 9}))
	h3 := goertzel(sat, 600, fs) / goertzel(sat, 200, fs)
	even := goertzel(sat, 400, fs) / goertzel(sat, 200, fs)
	t.Logf("3e harmonique : %.1f %%, 2e : %.3f %%", 100*h3, 100*even)
	if h3 < 0.03 {
		t.Errorf("3e harmonique trop faible : %.3f", h3)
	}
	if even > 0.005 {
		t.Errorf("2e harmonique inattendue : %.4f (saturation symétrique)", even)
	}
}

func TestBiasAddsEvenHarmonicsWithoutDC(t *testing.T) {
	const fs = 44100
	in := loudSine(200, fs)
	run := func(bias float64) []byte {
		out, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, nil, &saturation{driveDB: 9, bias: bias}))
		return out
	}
	sym, asym := run(0), run(0.3)
	even0 := goertzel(sym, 400, fs) / goertzel(sym, 200, fs)
	even := goertzel(asym, 400, fs) / goertzel(asym, 200, fs)
	t.Logf("2e harmonique : %.3f %% sans biais, %.1f %% avec biais 0,3", 100*even0, 100*even)
	if even < 0.02 || even < 20*even0 {
		t.Errorf("le biais doit faire apparaître la 2e harmonique (%.4f contre %.5f)", even, even0)
	}
	// la composante continue est retirée : moyenne ≈ 0 sur la seconde moitié
	var sum float64
	n := len(asym) / 2
	for i := n / 2; i < n; i++ {
		sum += float64(int16(uint16(asym[2*i]) | uint16(asym[2*i+1])<<8))
	}
	if mean := sum / float64(n-n/2); math.Abs(mean) > 0.01*20000 {
		t.Errorf("composante continue résiduelle : %.0f (amplitude 20000)", mean)
	}
}

func TestSaturationSilenceStaysSilent(t *testing.T) {
	in := make([]byte, 4*3000) // stéréo, silence
	out, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), 44100, 2, nil, nil, &saturation{driveDB: 12, bias: 0.4}))
	if !bytes.Equal(in, out) {
		t.Error("le silence doit rester un silence exact, même avec biais")
	}
}

// Le suréchantillonnage atténue le repliement de spectre : à 44,1 kHz, la 3e
// harmonique d'un 15 kHz (45 kHz) se replie à 900 Hz. Réglage modéré, pour que
// la sortie reste sous le plafond doux et que seul le suréchantillonnage joue.
func TestOversamplingReducesAliasing(t *testing.T) {
	const fs = 44100
	in := strongSine(15000, 16000, fs, fs)
	s := saturation{driveDB: 6}
	over, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, nil, &s))

	naive := mustSat(t, fs, s) // même courbe, appliquée sans suréchantillonnage
	raw := make([]byte, len(in))
	for i := 0; i+1 < len(in); i += 2 {
		x := float64(int16(uint16(in[i])|uint16(in[i+1])<<8)) / 32768
		v := int16(math.Round(naive.shape(x) * 32768))
		raw[i], raw[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	aNaive := goertzel(raw, 900, fs) / goertzel(raw, 15000, fs)
	aOver := goertzel(over, 900, fs) / goertzel(over, 15000, fs)
	t.Logf("repliement à 900 Hz : %.2f %% sans, %.3f %% avec suréchantillonnage (%.0f dB)", 100*aNaive, 100*aOver, 20*math.Log10(aNaive/aOver))
	if aNaive < 0.02 {
		t.Fatalf("test non pertinent : le repliement sans suréchantillonnage est trop faible (%.4f)", aNaive)
	}
	if aOver > aNaive/10 { // au moins 20 dB
		t.Errorf("le suréchantillonnage doit réduire le repliement d'au moins 20 dB (%.4f → %.4f)", aNaive, aOver)
	}
}

// Même poussé fort, l'étage ne produit jamais de pleine échelle (plafond doux) :
// la fin de chaîne n'a donc pas à écrêter.
func TestSaturationNeverReachesFullScale(t *testing.T) {
	const fs = 44100
	for _, f := range []float64{100, 1000, 8000, 15000} {
		in := strongSine(f, 32000, fs, fs)
		out, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, nil, &saturation{driveDB: 18, bias: 0.2}))
		for i := 0; i+1 < len(out); i += 2 {
			if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v >= 32767 || v <= -32768 {
				t.Fatalf("%.0f Hz : pleine échelle atteinte à l'échantillon %d", f, i/2)
			}
		}
	}
}

func TestSoftCeiling(t *testing.T) {
	for _, x := range []float64{0, 0.3, 0.9, -0.9} {
		if softCeiling(x) != x {
			t.Errorf("softCeiling(%v) = %v : identité attendue sous le genou", x, softCeiling(x))
		}
	}
	prev := -1.0
	for x := 0.0; x <= 3; x += 0.01 {
		y := softCeiling(x)
		if y < prev || y > ceilingTop {
			t.Fatalf("softCeiling(%.2f) = %v : monotone et ≤ %.5f attendus", x, y, ceilingTop)
		}
		prev = y
	}
	if softCeiling(-1.5) != -softCeiling(1.5) {
		t.Error("softCeiling doit être impaire")
	}
	// genou réglable : identité en dessous, jamais la pleine échelle au-dessus
	if softCeilingAt(0.7, 0.8) != 0.7 || softCeilingAt(1.6, 0.8) > ceilingTop || softCeilingAt(1.6, 0.8) <= 0.8 {
		t.Errorf("softCeilingAt mal calibré : %v, %v", softCeilingAt(0.7, 0.8), softCeilingAt(1.6, 0.8))
	}
}

// Les états (filtres, anti-continu) sont propres à chaque canal : un canal
// silencieux reste silencieux, et une lecture fragmentée donne le même résultat.
func TestSaturationPerChannelState(t *testing.T) {
	const fs = 44100
	l := loudSine(120, fs)
	in := make([]byte, 0, 2*len(l))
	for i := 0; i+1 < len(l); i += 2 {
		in = append(in, l[i], l[i+1], 0, 0) // gauche fort, droite silencieuse
	}
	sat := saturation{driveDB: 12, bias: 0.3}
	out, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 2, nil, nil, &sat))
	for i := 0; i+3 < len(out); i += 4 {
		if out[i+2] != 0 || out[i+3] != 0 {
			t.Fatalf("diaphonie : le canal droit n'est plus silencieux à la trame %d", i/4)
		}
	}
	chunked, _ := io.ReadAll(newEqualizerReader(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), fs, 2, nil, nil, &sat))
	if !bytes.Equal(out, chunked) {
		t.Error("la lecture fragmentée doit donner le même résultat")
	}
}

func TestRockSaturationScalesWithIntensity(t *testing.T) {
	const fs = 44100
	// isole la saturation : on retire le compresseur de Rock le temps du test
	saved := profileCompression[Rock]
	profileCompression[Rock] = nil
	defer func() { profileCompression[Rock] = saved }()

	// Dans les basses, là où Rock est au maximum de son égalisation, le niveau
	// qui entre dans la saturation suit le drive : plus d'intensité, plus de grain.
	// (À 200 Hz, le gain automatique compense exactement le drive : pas de différence.)
	in := sine(50, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // amplitude ≈ 20000
		v := int16(int(int16(uint16(in[i])|uint16(in[i+1])<<8)) * 5 / 2)
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	thd := func(p Profile, intensity float64) float64 {
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, intensity))
		return goertzel(out, 150, fs) / goertzel(out, 50, fs)
	}
	lo, hi := thd(Rock, 0.4), thd(Rock, 1)
	t.Logf("Rock 3e harmonique @50 Hz : %.2f %% @0.4, %.2f %% @1", 100*lo, 100*hi)
	if !(hi > lo*1.3 && lo > 0) {
		t.Errorf("la saturation doit croître avec l'intensité (%.4f → %.4f)", lo, hi)
	}
	// les autres profils n'ajoutent pas de saturation
	if h := thd(Pop, 1); h > 0.005 {
		t.Errorf("Pop ne doit pas saturer (3e harmonique %.4f)", h)
	}
}

// À niveau d'écoute normal (-18 dBFS, 1 kHz), Rock doit rendre à peu près le
// niveau d'entrée : la saturation compense la baisse du gain automatique.
func TestRockLevelNearUnity(t *testing.T) {
	const fs = 44100
	in := sine(1000, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // 8000 → ≈ -12 dBFS ; /2 → ≈ -18 dBFS
		v := int16(uint16(in[i])|uint16(in[i+1])<<8) / 2
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, Rock, 1))
	got := 20 * math.Log10(rms(out)/rms(in))
	t.Logf("Rock @1 kHz, -18 dBFS : %+.2f dB", got)
	if math.Abs(got) > 1.5 {
		t.Errorf("niveau %+.2f dB, attendu ≈ 0 ± 1.5", got)
	}
}

func TestSaturatedOutputNeverClips(t *testing.T) {
	const fs = 44100
	in := sine(100, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // amplitude ≈ 32000
		v := int16(uint16(in[i])|uint16(in[i+1])<<8) * 4
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, Rock, 1))
	for i := 0; i+1 < len(out); i += 2 {
		if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v == 32767 || v == -32768 {
			t.Fatalf("écrêtage à l'échantillon %d", i/2)
		}
	}
}

// Seuls les profils asymétriques (biais > 0) produisent des harmoniques paires.
func TestOnlyBiasedProfilesAddEvenHarmonics(t *testing.T) {
	const fs = 44100
	in := tone(200, ampOf(-9), fs, fs)
	biased := 0
	for _, p := range Profiles() {
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, 1))
		even := goertzel(out, 400, fs) / goertzel(out, 200, fs)
		t.Logf("%-10v 2e harmonique : %.2f %%", p, 100*even)
		if profileSaturation[p].bias > 0 {
			biased++
			if even < 0.02 {
				t.Errorf("%v est asymétrique : harmoniques paires attendues (%.4f)", p, even)
			}
		} else if even > 0.005 {
			t.Errorf("%v ne doit pas produire d'harmonique paire (%.4f)", p, even)
		}
	}
	if biased < 2 {
		t.Errorf("Jazz et Tube doivent être asymétriques (%d trouvé(s))", biased)
	}
}

func TestSoftCeilingNeverRoundsToFullScale(t *testing.T) {
	for _, x := range []float64{0.95, 1, 1.5, 3, 10, 1000} {
		for _, k := range []float64{ceilingKnee, postCeilingKnee} {
			if v := math.Round(32768 * softCeilingAt(x, k)); v >= 32767 {
				t.Errorf("x=%v knee=%v : arrondi à %v", x, k, v)
			}
		}
	}
}
