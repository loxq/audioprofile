package bassboost

import (
	"bytes"
	"io"
	"testing"
)

func TestTrebleGain(t *testing.T) {
	const fs = 44100
	for _, tc := range []struct {
		name      string
		intensity float64
		freq      float64
		minRat    float64
		maxRat    float64
	}{
		{"boost aigus", 1.0, 12000, 3.0, 6.0},  // ~ +15 dB = x5.6
		{"boost graves", 1.0, 100, 0.95, 1.05}, // graves inchangés
		{"cut aigus", -1.0, 12000, 0.15, 0.35}, // ~ -13 dB = x0.2
		{"cut graves", -1.0, 100, 0.95, 1.05},
	} {
		in := sine(tc.freq, fs, fs)
		out, err := io.ReadAll(TrebleBoost(bytes.NewReader(in), fs, 1, tc.intensity))
		if err != nil || len(out) != len(in) {
			t.Fatalf("%s : err=%v len=%d/%d", tc.name, err, len(out), len(in))
		}
		ratio := rms(out) / rms(in)
		t.Logf("%s (%.0f Hz) : x%.2f", tc.name, tc.freq, ratio)
		if ratio < tc.minRat || ratio > tc.maxRat {
			t.Errorf("%s : ratio %.2f hors [%.2f, %.2f]", tc.name, ratio, tc.minRat, tc.maxRat)
		}
	}
}

func TestTrebleZeroIsIdentity(t *testing.T) {
	in := sine(8000, 44100, 1000)
	out, _ := io.ReadAll(TrebleBoost(bytes.NewReader(in), 44100, 1, 0))
	if !bytes.Equal(in, out) {
		t.Error("intensity 0 doit laisser le signal intact")
	}
}

func TestTrebleLowSampleRateIsIdentity(t *testing.T) {
	in := sine(1000, 6000, 1000)
	out, _ := io.ReadAll(TrebleBoost(bytes.NewReader(in), 6000, 1, 1))
	if !bytes.Equal(in, out) {
		t.Error("coupure au-dessus de Nyquist : le signal doit rester intact")
	}
}
