package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func ratioAt(t *testing.T, freq float64, fs int, bands []Band) float64 {
	t.Helper()
	in := sine(freq, fs, fs)
	out, err := io.ReadAll(Equalizer(bytes.NewReader(in), fs, 1, bands))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
	return rms(out) / rms(in)
}

func db(g float64) float64 { return math.Pow(10, g/20) }

// Courbe « rock » : sourire avec creux à 400 Hz et présence à 3 kHz.
func TestEqualizerMultiBand(t *testing.T) {
	const fs = 44100
	bands := []Band{
		{Type: LowShelf, FreqHz: 100, GainDB: 5},
		{Type: Peaking, FreqHz: 400, GainDB: -4, Q: 1},
		{Type: Peaking, FreqHz: 3000, GainDB: 4, Q: 1},
		{Type: HighShelf, FreqHz: 8000, GainDB: 3},
	}
	for _, tc := range []struct {
		freq   float64
		wantDB float64
		tolDB  float64
	}{
		{400, -4, 0.7},  // creux
		{3000, 4, 0.7},  // présence
		{20, 5, 0.7},    // plateau des basses
		{18000, 3, 0.7}, // plateau des aigus
		{1000, 0, 2.0},  // entre les bandes : proche de 0
	} {
		got := 20 * math.Log10(ratioAt(t, tc.freq, fs, bands))
		t.Logf("%.0f Hz : %+.2f dB (attendu %+.1f)", tc.freq, got, tc.wantDB)
		if math.Abs(got-tc.wantDB) > tc.tolDB {
			t.Errorf("%.0f Hz : %+.2f dB, attendu %+.1f ± %.1f", tc.freq, got, tc.wantDB, tc.tolDB)
		}
	}
}

func TestEqualizerQ(t *testing.T) {
	const fs = 44100
	// à une octave du centre, une cloche étroite agit moins qu'une cloche large
	wide := ratioAt(t, 2000, fs, []Band{{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 0.5}})
	narrow := ratioAt(t, 2000, fs, []Band{{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 4}})
	if !(wide > narrow) {
		t.Errorf("Q large (%.2f) doit agir plus loin du centre que Q étroit (%.2f)", wide, narrow)
	}
	// au centre, le gain est exactement celui demandé quelle que soit Q
	for _, q := range []float64{0.5, 1, 4} {
		got := ratioAt(t, 1000, fs, []Band{{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: q}})
		if math.Abs(got-db(6)) > 0.05*db(6) {
			t.Errorf("Q=%.1f : ratio %.2f, attendu %.2f", q, got, db(6))
		}
	}
}

func TestEqualizerIgnoredBands(t *testing.T) {
	in := sine(1000, 44100, 1000)
	for name, bands := range map[string][]Band{
		"aucune bande":         nil,
		"gain nul":             {{Type: Peaking, FreqHz: 1000, GainDB: 0}},
		"fréquence <= 0":       {{Type: LowShelf, FreqHz: 0, GainDB: 6}},
		"au-dessus de Nyquist": {{Type: HighShelf, FreqHz: 30000, GainDB: 6}},
		"gain NaN":             {{Type: Peaking, FreqHz: 1000, GainDB: math.NaN()}},
	} {
		out, _ := io.ReadAll(Equalizer(bytes.NewReader(in), 44100, 1, bands))
		if !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	out, _ := io.ReadAll(Equalizer(bytes.NewReader(in), 0, 1, []Band{{Type: Peaking, FreqHz: 1000, GainDB: 6}}))
	if !bytes.Equal(in, out) {
		t.Error("sampleRate <= 0 : le signal doit rester intact")
	}
}

func TestEqualizerGainClamped(t *testing.T) {
	const fs = 44100
	// 60 dB demandés → ramenés à 24 dB ; mesuré sur un signal faible pour éviter l'écrêtage
	in := sine(1000, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // /16 : amplitude 500
		v := int16(uint16(in[i])|uint16(in[i+1])<<8) / 16
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	out, _ := io.ReadAll(Equalizer(bytes.NewReader(in), fs, 1, []Band{{Type: Peaking, FreqHz: 1000, GainDB: 60, Q: 1}}))
	got := rms(out) / rms(in)
	if math.Abs(got-db(24)) > 0.05*db(24) {
		t.Errorf("ratio %.2f, attendu ≈ %.2f (24 dB)", got, db(24))
	}
}

func TestEqualizerStereoOddChunks(t *testing.T) {
	in := sine(60, 44100, 2*3001)
	bands := []Band{{Type: LowShelf, FreqHz: 100, GainDB: 6}, {Type: Peaking, FreqHz: 1000, GainDB: -3}}
	out, err := io.ReadAll(Equalizer(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), 44100, 2, bands))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
	// le résultat doit être identique à une lecture en un seul bloc
	ref, _ := io.ReadAll(Equalizer(bytes.NewReader(in), 44100, 2, bands))
	if !bytes.Equal(out, ref) {
		t.Error("la lecture fragmentée doit donner le même résultat")
	}
}
