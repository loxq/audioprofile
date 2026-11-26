package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func gated(t *testing.T, in []byte, ch int, s GateSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(Gate(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func gateGain(t *testing.T, freq, level float64, s GateSettings) float64 {
	t.Helper()
	const fs = 44100
	in := tone(freq, ampOf(level), fs, 3*fs)
	out := gated(t, in, 1, s)
	return 20 * math.Log10(goertzel(out, freq, fs)/goertzel(in, freq, fs))
}

func TestGateNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -50, 200, 80), pluck(fs, -60, 300, 60))
	for name, s := range map[string]GateSettings{
		"rapport 1":   {ThresholdDB: -40, Ratio: 1},
		"rapport < 1": {ThresholdDB: -40, Ratio: 0.3},
		"rapport nul": {ThresholdDB: -40},
		"rapport NaN": {ThresholdDB: -40, Ratio: math.NaN()},
		"seuil NaN":   {ThresholdDB: math.NaN(), Ratio: 4},
		"plage NaN":   {ThresholdDB: -40, Ratio: 4, RangeDB: math.NaN()},
		"attaque NaN": {ThresholdDB: -40, Ratio: 4, AttackMs: math.NaN()},
	} {
		if out := gated(t, in, 2, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Gate(bytes.NewReader(in), 0, 2, GateSettings{ThresholdDB: -40, Ratio: 4})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Courbe statique : rien au-dessus du seuil, une pente de Ratio − 1 en dessous, un raccord sans saut
// à travers le genou, et une réduction plafonnée à RangeDB.
func TestGateStaticCurve(t *testing.T) {
	g := GateSettings{ThresholdDB: -40, Ratio: 3, RangeDB: 30}.stage(44100, 1).(*gate)
	for _, tc := range []struct{ lvl, want float64 }{
		{-20, 0}, {-40, -1.5 * 0}, {-34, 0},
		{-45, -10}, // 5 dB sous le seuil, pente 2 → −10 dB
		{-50, -20}, // 10 dB sous le seuil → −20 dB
		{-60, -30}, // 20 dB sous le seuil donnerait −40 : plafonné à RangeDB = 30
		{-100, -30},
	} {
		if tc.lvl == -40 { // sur le seuil, dans le genou : la valeur exacte vient du raccord quadratique
			if r := g.reduction(tc.lvl); r > 0 || r < -2.5 {
				t.Errorf("sur le seuil : %.2f dB, attendu un petit négatif", r)
			}
			continue
		}
		if r := g.reduction(tc.lvl); math.Abs(r-tc.want) > 0.01 {
			t.Errorf("niveau %g dBFS : réduction %.2f dB, attendu %.2f", tc.lvl, r, tc.want)
		}
	}
	// continuité sur tout l'axe : aucun saut de plus de 0,1 dB pour un pas de 0,05 dB
	prev := g.reduction(-20)
	for lvl := -20.05; lvl > -80; lvl -= 0.05 {
		r := g.reduction(lvl)
		if math.Abs(r-prev) > 0.25 { // pente maximale de 2 × 0,05 = 0,1 dB par pas, avec marge
			t.Fatalf("saut de %.2f dB à %.2f dBFS", r-prev, lvl)
		}
		if r > 0 {
			t.Fatalf("réduction positive (%.2f dB) à %.2f dBFS", r, lvl)
		}
		prev = r
	}
}

// Au-dessus du seuil, un signal stable n'est pas touché, même dans les basses (où le détecteur de
// crête ondule) ; en dessous, il est réduit selon la courbe, à RangeDB au plus.
func TestGateSteadyTones(t *testing.T) {
	s := GateSettings{ThresholdDB: -40, Ratio: 2, RangeDB: 40}
	for _, f := range []float64{40, 50, 100, 1000, 8000} {
		for _, lv := range []float64{-30, -12} {
			if g := gateGain(t, f, lv, s); math.Abs(g) > 0.1 {
				t.Errorf("%g Hz à %g dBFS (au-dessus du seuil) : %+.2f dB, attendu intact", f, lv, g)
			}
		}
	}
	// 15 dB sous le seuil avec un rapport 2 : −15 dB (±2)
	if g := gateGain(t, 1000, -55, s); math.Abs(g+15) > 2 {
		t.Errorf("rapport 2, 15 dB sous le seuil : %+.1f dB, attendu ≈ −15", g)
	}
	// gate franc : réduit de RangeDB (à ±3)
	if g := gateGain(t, 1000, -60, GateSettings{ThresholdDB: -40, Ratio: 100, RangeDB: 35}); math.Abs(g+35) > 3 {
		t.Errorf("gate : %+.1f dB, attendu ≈ −35", g)
	}
}

// Un gate sur de la parole simulée : des salves fortes séparées par du bruit de fond. Le bruit est
// réduit entre les salves, les salves passent intactes (après l'ouverture), et plus l'attaque est
// courte, plus le début de la salve passe.
func TestGateOpensAndCloses(t *testing.T) {
	const fs = 44100
	n := 4 * fs
	sig := make([]float64, n)
	noise := make([]float64, n)
	seed := uint32(3)
	for i := range noise {
		seed = seed*1664525 + 1013904223
		noise[i] = float64(int16(seed>>16)) / 32768 * ampOf(-58) * 1.7 // bruit de fond ≈ −60 dBFS efficace
	}
	for i := range sig {
		sig[i] = noise[i]
		if i%fs >= fs/2 && i%fs < fs/2+fs/4 { // une salve de 250 ms toutes les secondes
			sig[i] += ampOf(-20) * math.Sin(2*math.Pi*500*float64(i)/fs)
		}
	}
	for i := range sig {
		sig[i] = math.Round(sig[i])
	}
	in := interleave(sig)
	s := GateSettings{ThresholdDB: -45, Ratio: 20, RangeDB: 40, AttackMs: 2, ReleaseMs: 80}
	out := floats(gated(t, in, 1, s))
	// bruit entre les salves, après la fermeture : réduit d'au moins 25 dB
	floor := func(sec int) (a, b float64) {
		from, to := sec*fs+fs*95/100, sec*fs+fs*145/100 // de 450 ms après la fin de la salve à 50 ms avant la suivante : le gate est fermé
		return rmsRange(sig, from, to), rmsRange(out, from, to)
	}
	a, b := floor(1)
	if r := 20 * math.Log10(b/a); r > -25 {
		t.Errorf("bruit de fond entre les salves : %.1f dB, attendu une réduction d'au moins 25 dB", r)
	}
	// salve : de 20 à 240 ms après son début, niveau intact à ±0,5 dB
	b0 := 2*fs + fs/2
	if g := 20 * math.Log10(rmsRange(out, b0+fs/50, b0+fs/4-fs/100)/rmsRange(sig, b0+fs/50, b0+fs/4-fs/100)); math.Abs(g) > 0.5 {
		t.Errorf("salve : %+.2f dB, attendu intacte", g)
	}
	// attaque courte contre attaque longue : le début de la salve (4 premières ms) passe mieux
	head := func(att float64) float64 {
		o := floats(gated(t, in, 1, GateSettings{ThresholdDB: -45, Ratio: 20, RangeDB: 40, AttackMs: att, ReleaseMs: 80}))
		return 20 * math.Log10(rmsRange(o, b0, b0+fs/250)/rmsRange(sig, b0, b0+fs/250))
	}
	short, long := head(0.5), head(40)
	t.Logf("4 premières ms de la salve : %+.1f dB (attaque 0,5 ms), %+.1f dB (attaque 40 ms)", short, long)
	if short < long+5 {
		t.Errorf("attaque 0,5 ms (%+.1f dB) doit laisser passer au moins 5 dB de plus que 40 ms (%+.1f dB)", short, long)
	}
}

// Détection liée : une voix forte à gauche tient le gate ouvert pour les deux canaux ; un bruit faible
// seul le ferme pour les deux, du même facteur.
func TestGateLinkedStereo(t *testing.T) {
	const fs = 44100
	l := make([]float64, 2*fs)
	r := make([]float64, 2*fs)
	for i := range l {
		r[i] = math.Round(ampOf(-52) * math.Sin(2*math.Pi*700*float64(i)/fs)) // faible, sous le seuil
		if i < fs {
			l[i] = math.Round(ampOf(-15) * math.Sin(2*math.Pi*300*float64(i)/fs)) // forte pendant la première seconde
		}
	}
	out := gated(t, interleave(l, r), 2, GateSettings{ThresholdDB: -40, Ratio: 50, RangeDB: 40, ReleaseMs: 50})
	ro := floats(monoOf(out, 1))
	first := 20 * math.Log10(rmsRange(ro, fs/5, fs*9/10)/rmsRange(r, fs/5, fs*9/10))            // pendant la voix de gauche
	second := 20 * math.Log10(rmsRange(ro, fs*13/10, fs*19/10)/rmsRange(r, fs*13/10, fs*19/10)) // après
	if math.Abs(first) > 0.3 || second > -25 {
		t.Errorf("canal droit : %+.2f dB pendant la voix de gauche (attendu intact), %+.1f dB après (attendu réduit)", first, second)
	}
}

func TestGateSilenceClampAndStreaming(t *testing.T) {
	const fs = 44100
	silence := make([]byte, 4*fs)
	if o := gated(t, silence, 2, GateSettings{ThresholdDB: -40, Ratio: 10}); !bytes.Equal(o, silence) {
		t.Error("le silence doit rester silence")
	}
	in := interleave(pluck(fs, -30, 200, 150)[:30000], pluck(fs, -38, 300, 120)[:30000])
	if !bytes.Equal(gated(t, in, 2, GateSettings{ThresholdDB: -40, Ratio: 1e6, RangeDB: 1e6}), gated(t, in, 2, GateSettings{ThresholdDB: -40, Ratio: maxRatio, RangeDB: maxGateRangeDB})) {
		t.Error("rapport et plage hors bornes doivent être ramenés dedans")
	}
	s := GateSettings{ThresholdDB: -35, Ratio: 6, RangeDB: 30}
	want := gated(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(Gate(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
