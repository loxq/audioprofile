package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

// inert est un compresseur qui existe (donc découpe et recompose la bande) mais ne
// réduit jamais rien : son seuil est à 0 dBFS, jamais atteint par les signaux de test.
var inert = CompressorSettings{ThresholdDB: 0, Ratio: 2}

func fourBands(c ...CompressorSettings) MultibandSettings {
	return MultibandSettings{Crossovers: []float64{120, 1200, 6000}, Bands: c}
}

func runMB(t *testing.T, in []byte, ch int, s MultibandSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(MultibandCompressor(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// mbGainDB mesure le gain d'un sinus de 8000 à freq.
func mbGainDB(t *testing.T, s MultibandSettings, freq float64) float64 {
	t.Helper()
	const fs = 44100
	n := 2 * fs // 2 s : le régime établi des graves demande de la durée
	in := tone(freq, 8000, fs, n)
	out := runMB(t, in, 1, s)
	return 20 * math.Log10(goertzel(out, freq, fs)/goertzel(in, freq, fs))
}

// La somme des bandes a un module plat de 20 Hz à 18 kHz, y compris aux séparations :
// c'est ce que garantit la compensation passe-tout des bandes graves.
func TestMultibandSumIsFlat(t *testing.T) {
	s := fourBands(inert, inert, inert, inert)
	for _, f := range []float64{20, 40, 80, 120, 200, 400, 800, 1200, 2000, 3500, 6000, 9000, 14000, 18000} {
		if g := mbGainDB(t, s, f); math.Abs(g) > 0.1 {
			t.Errorf("%.0f Hz : gain %.3f dB, attendu 0 ± 0,1", f, g)
		}
	}
	// deux bandes seulement
	two := MultibandSettings{Crossovers: []float64{500}, Bands: []CompressorSettings{inert, inert}}
	for _, f := range []float64{100, 500, 3000} {
		if g := mbGainDB(t, two, f); math.Abs(g) > 0.1 {
			t.Errorf("2 bandes, %.0f Hz : gain %.3f dB", f, g)
		}
	}
}

// Compresser les basses ne doit pas faire baisser les aigus, contrairement au
// compresseur large bande aux mêmes réglages.
func TestMultibandBandsAreIndependent(t *testing.T) {
	const fs = 44100
	bass := tone(80, ampOf(-6), fs, 2*fs)
	treble := tone(6000, ampOf(-30), fs, 2*fs)
	mix := make([]byte, len(bass))
	for i := 0; i < len(mix); i += 2 {
		v := int(int16(uint16(bass[i])|uint16(bass[i+1])<<8)) + int(int16(uint16(treble[i])|uint16(treble[i+1])<<8))
		mix[i], mix[i+1] = byte(uint16(int16(v))), byte(uint16(int16(v))>>8)
	}
	comp := CompressorSettings{ThresholdDB: -30, Ratio: 8, AttackMs: 2, ReleaseMs: 50}
	s := fourBands(comp, inert, inert, inert)
	out := runMB(t, mix, 1, s)
	bassDB := 20 * math.Log10(goertzel(out, 80, fs)/goertzel(mix, 80, fs))
	trebleDB := 20 * math.Log10(goertzel(out, 6000, fs)/goertzel(mix, 6000, fs))
	if bassDB > -10 {
		t.Errorf("basses : %.1f dB, attendu une forte réduction (< −10 dB)", bassDB)
	}
	if math.Abs(trebleDB) > 0.3 {
		t.Errorf("aigus : %.2f dB, attendu inchangé (±0,3 dB)", trebleDB)
	}

	wide, _ := io.ReadAll(Compressor(bytes.NewReader(mix), fs, 1, comp))
	wideDB := 20 * math.Log10(goertzel(wide, 6000, fs)/goertzel(mix, 6000, fs))
	t.Logf("aigus : multibande %.2f dB, large bande %.2f dB", trebleDB, wideDB)
	if wideDB > -3 {
		t.Errorf("témoin large bande : aigus %.2f dB, la comparaison n'a pas de sens", wideDB)
	}
}

// La compensation de chaque bande ne s'applique qu'à elle.
func TestMultibandPerBandMakeup(t *testing.T) {
	s := fourBands(inert, inert, inert, CompressorSettings{Ratio: 1, MakeupDB: 6})
	if g := mbGainDB(t, s, 12000); math.Abs(g-6) > 0.3 {
		t.Errorf("12 kHz : gain %.2f dB, attendu +6", g)
	}
	if g := mbGainDB(t, s, 300); math.Abs(g) > 0.2 {
		t.Errorf("300 Hz : gain %.2f dB, attendu 0", g)
	}
	if g := mbGainDB(t, s, 2500); math.Abs(g) > 0.5 { // loin de 6 kHz : −24 dB env. pour la bande des aigus
		t.Errorf("2,5 kHz : gain %.2f dB, attendu ≈ 0", g)
	}
}

func TestMultibandInvalidSettingsAreIdentity(t *testing.T) {
	const fs = 44100
	in := tone(1000, 8000, fs, 4000)
	comp := CompressorSettings{ThresholdDB: -30, Ratio: 4}
	for name, s := range map[string]MultibandSettings{
		"vide":                      {},
		"bandes en trop":            {Crossovers: []float64{500}, Bands: []CompressorSettings{comp, comp, comp}},
		"bandes manquantes":         {Crossovers: []float64{500, 2000}, Bands: []CompressorSettings{comp, comp}},
		"une seule bande":           {Bands: []CompressorSettings{comp}},
		"séparations décroissantes": {Crossovers: []float64{2000, 500}, Bands: []CompressorSettings{comp, comp, comp}},
		"séparations égales":        {Crossovers: []float64{500, 500}, Bands: []CompressorSettings{comp, comp, comp}},
		"séparation nulle":          {Crossovers: []float64{0}, Bands: []CompressorSettings{comp, comp}},
		"séparation NaN":            {Crossovers: []float64{math.NaN()}, Bands: []CompressorSettings{comp, comp}},
		"séparation > Nyquist":      {Crossovers: []float64{30000}, Bands: []CompressorSettings{comp, comp}},
		"aucune bande active":       {Crossovers: []float64{500}, Bands: []CompressorSettings{{}, {Ratio: 1}}},
	} {
		if out := runMB(t, in, 1, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	out, _ := io.ReadAll(MultibandCompressor(bytes.NewReader(in), 0, 1, fourBands(comp, comp, comp, comp)))
	if !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Détection liée par bande : la même réduction sur les deux canaux, et un canal
// silencieux reste exactement silencieux (pas de diaphonie par les filtres).
func TestMultibandLinkedStereo(t *testing.T) {
	const fs = 44100
	comp := CompressorSettings{ThresholdDB: -30, Ratio: 6, AttackMs: 2, ReleaseMs: 50}
	s := MultibandSettings{Crossovers: []float64{500}, Bands: []CompressorSettings{comp, inert}}

	in := stereoPCM(2*fs, sineFn(100, ampOf(-6), fs), sineFn(100, ampOf(-24), fs))
	out := runMB(t, in, 2, s)
	gl := 20 * math.Log10(goertzel(monoOf(out, 0), 100, fs)/goertzel(monoOf(in, 0), 100, fs))
	gr := 20 * math.Log10(goertzel(monoOf(out, 1), 100, fs)/goertzel(monoOf(in, 1), 100, fs))
	if gl > -8 || math.Abs(gl-gr) > 0.1 {
		t.Errorf("gain gauche %.2f dB, droit %.2f dB : réduction attendue forte et identique", gl, gr)
	}

	in = stereoPCM(fs, sineFn(100, ampOf(-6), fs), func(int) float64 { return 0 })
	for i, v := range channel(runMB(t, in, 2, s), 1) {
		if v != 0 {
			t.Fatalf("canal silencieux : %d à la trame %d", v, i)
		}
	}
}

// monoOf extrait un canal d'un flux stéréo sous forme de flux mono.
func monoOf(b []byte, ch int) []byte {
	out := make([]byte, len(b)/2)
	for i := 0; i < len(b)/4; i++ {
		copy(out[2*i:], b[4*i+2*ch:4*i+2*ch+2])
	}
	return out
}

func TestMultibandStreamingAndLength(t *testing.T) {
	const fs = 44100
	comp := CompressorSettings{ThresholdDB: -30, Ratio: 4, AttackMs: 5, ReleaseMs: 80, KneeDB: 6}
	s := fourBands(comp, comp, comp, comp)
	in := stereoPCM(6001, sineFn(150, 9000, fs), sineFn(3000, 6000, fs))
	want := runMB(t, in, 2, s)
	if len(want) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(want), len(in))
	}
	if bytes.Equal(want, in) {
		t.Error("le traitement doit modifier le signal")
	}
	got, err := io.ReadAll(iotest.OneByteReader(MultibandCompressor(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}

func TestScaleMultiband(t *testing.T) {
	s := fourBands(CompressorSettings{Ratio: 3, MakeupDB: 2}, inert, inert, inert)
	orig := s.Bands[0]
	h := scaleMultiband(s, 0.5)
	if h.Bands[0].Ratio != 2 || h.Bands[0].MakeupDB != 1 {
		t.Errorf("intensité 0,5 : %+v", h.Bands[0])
	}
	if d := scaleMultiband(s, 3); d.Bands[0].Ratio != 7 || d.Bands[0].MakeupDB != 2 {
		t.Errorf("intensité 3 : %+v (compensation plafonnée à celle de l'intensité 1)", d.Bands[0])
	}
	if s.Bands[0] != orig {
		t.Error("scaleMultiband ne doit pas modifier son argument")
	}
}
