package audioprofile

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
)

func sine(freq float64, fs, n int) []byte {
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*freq*float64(i)/float64(fs)))
		binary.Write(buf, binary.LittleEndian, v)
	}
	return buf.Bytes()
}

func rms(b []byte) float64 {
	var sum float64
	n := len(b) / 2
	for i := n / 2; i < n; i++ { // ignore le régime transitoire
		v := float64(int16(binary.LittleEndian.Uint16(b[2*i:])))
		sum += v * v
	}
	return math.Sqrt(sum / float64(n-n/2))
}

func TestGain(t *testing.T) {
	const fs = 44100
	for _, tc := range []struct {
		freq   float64
		minRat float64
		maxRat float64
	}{
		{40, 3.0, 6.0},     // graves : fortement amplifiés (~+15 dB = x5.6)
		{5000, 0.95, 1.05}, // aigus : quasi inchangés
	} {
		in := sine(tc.freq, fs, fs)
		out, err := io.ReadAll(BassBoost(bytes.NewReader(in), fs, 1, 1.0))
		if err != nil || len(out) != len(in) {
			t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
		}
		ratio := rms(out) / rms(in)
		t.Logf("%.0f Hz : x%.2f", tc.freq, ratio)
		if ratio < tc.minRat || ratio > tc.maxRat {
			t.Errorf("%.0f Hz : ratio %.2f hors [%.2f, %.2f]", tc.freq, ratio, tc.minRat, tc.maxRat)
		}
	}
}

func TestCut(t *testing.T) {
	const fs = 44100
	for _, tc := range []struct {
		freq   float64
		minRat float64
		maxRat float64
	}{
		{40, 0.15, 0.35},   // graves atténués (~ -13 dB = x0.2)
		{5000, 0.95, 1.05}, // aigus inchangés
	} {
		in := sine(tc.freq, fs, fs)
		out, _ := io.ReadAll(BassBoost(bytes.NewReader(in), fs, 1, -1.0))
		ratio := rms(out) / rms(in)
		t.Logf("%.0f Hz : x%.2f", tc.freq, ratio)
		if ratio < tc.minRat || ratio > tc.maxRat {
			t.Errorf("%.0f Hz : ratio %.2f hors [%.2f, %.2f]", tc.freq, ratio, tc.minRat, tc.maxRat)
		}
	}
}

func TestZeroIsIdentity(t *testing.T) {
	in := sine(60, 44100, 1000)
	out, _ := io.ReadAll(BassBoost(bytes.NewReader(in), 44100, 1, 0))
	if !bytes.Equal(in, out) {
		t.Error("intensity 0 doit laisser le signal intact")
	}
}

func TestStereoOddChunks(t *testing.T) {
	in := sine(60, 44100, 2*3001)
	// lecture octet par octet pour tester le recollage des trames
	out, err := io.ReadAll(BassBoost(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), 44100, 2, 0.5))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
}
