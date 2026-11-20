package audioprofile

import (
	"bytes"
	"io"
	"testing"
)

func TestMidGain(t *testing.T) {
	const fs = 44100
	for _, tc := range []struct {
		name      string
		intensity float64
		freq      float64
		minRat    float64
		maxRat    float64
	}{
		{"boost médiums", 1.0, 1000, 4.5, 6.5}, // ~ +15 dB = x5.6
		{"boost graves", 1.0, 40, 0.95, 1.1},
		{"boost aigus", 1.0, 15000, 0.95, 1.1},
		{"cut médiums", -1.0, 1000, 0.15, 0.22}, // ~ -15 dB = x0.18
		{"cut graves", -1.0, 40, 0.9, 1.05},
		{"cut aigus", -1.0, 15000, 0.9, 1.05},
	} {
		in := sine(tc.freq, fs, fs)
		out, err := io.ReadAll(MidBoost(bytes.NewReader(in), fs, 1, tc.intensity))
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

func TestMidZeroIsIdentity(t *testing.T) {
	in := sine(1000, 44100, 1000)
	out, _ := io.ReadAll(MidBoost(bytes.NewReader(in), 44100, 1, 0))
	if !bytes.Equal(in, out) {
		t.Error("intensity 0 doit laisser le signal intact")
	}
}
