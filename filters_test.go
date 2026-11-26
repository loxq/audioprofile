package audioprofile

import (
	"bytes"
	"io"
	"math"
	"math/cmplx"
	"testing"
	"testing/iotest"
)

// responseDB : gain (dB) d'une chaîne de sections à la fréquence f.
func responseDB(chain []coeffs, fs, f float64) float64 {
	z := cmplx.Rect(1, -2*math.Pi*f/fs)
	h := complex(1, 0)
	for _, c := range chain {
		h *= (complex(c.b0, 0) + complex(c.b1, 0)*z + complex(c.b2, 0)*z*z) / (1 + complex(c.a1, 0)*z + complex(c.a2, 0)*z*z)
	}
	return 20 * math.Log10(cmplx.Abs(h))
}

// −3 dB à la fréquence de coupure, pour tous les ordres, en passe-haut comme en passe-bas.
func TestButterworthIsMinus3dBAtCutoff(t *testing.T) {
	for _, fs := range []float64{44100, 48000, 96000} {
		for order := 1; order <= butterworthMaxOrder; order++ {
			for _, high := range []bool{true, false} {
				chain := butterworth(fs, 1000, 0, order, high)
				if got := responseDB(chain, fs, 1000); math.Abs(got+3.0103) > 0.05 {
					t.Errorf("%.0f Hz, ordre %d, haut=%v : %.3f dB à la coupure, attendu −3,01", fs, order, high, got)
				}
			}
		}
	}
}

// Pente de 6·n dB par octave : à 2 et 4 fois (ou la moitié et le quart de) la coupure, le gain
// suit la courbe de Butterworth 10·log10(1 + r^2n).
func TestButterworthSlope(t *testing.T) {
	const fs = 96000
	for order := 1; order <= butterworthMaxOrder; order++ {
		for _, r := range []float64{2, 4} {
			want := -10 * math.Log10(1+math.Pow(r, 2*float64(order)))
			if got := responseDB(butterworth(fs, 1000, 0, order, false), fs, 1000*r); math.Abs(got-want) > 0.5 {
				t.Errorf("passe-bas ordre %d à %g×fc : %.2f dB, attendu %.2f", order, r, got, want)
			}
			if got := responseDB(butterworth(fs, 1000, 0, order, true), fs, 1000/r); math.Abs(got-want) > 0.5 {
				t.Errorf("passe-haut ordre %d à fc/%g : %.2f dB, attendu %.2f", order, r, got, want)
			}
		}
	}
}

// La bande passante est plate, et un seul filtre n'en traverse pas deux fois les mêmes sections.
func TestButterworthPassbandIsFlat(t *testing.T) {
	const fs = 44100
	for order := 1; order <= butterworthMaxOrder; order++ {
		if g := responseDB(butterworth(fs, 500, 0, order, true), fs, 10000); math.Abs(g) > 0.1 {
			t.Errorf("passe-haut ordre %d à 10 kHz : %.3f dB", order, g)
		}
		if g := responseDB(butterworth(fs, 8000, 0, order, false), fs, 400); math.Abs(g) > 0.1 {
			t.Errorf("passe-bas ordre %d à 400 Hz : %.3f dB", order, g)
		}
	}
}

// Ordre 2 : le Q de la bande est respecté (1/√2 par défaut), et la surtension du Q se voit à la coupure.
func TestFilterOrderTwoUsesQ(t *testing.T) {
	const fs = 44100
	if g := responseDB(butterworth(fs, 1000, 0, 2, false), fs, 1000); math.Abs(g+3.0103) > 0.05 {
		t.Errorf("Q par défaut : %.3f dB à la coupure", g)
	}
	if g := responseDB(butterworth(fs, 1000, 2, 2, false), fs, 1000); math.Abs(g-6.02) > 0.1 {
		t.Errorf("Q = 2 : %.3f dB à la coupure, attendu +6,02 (gain = Q)", g)
	}
	// l'ordre 0 vaut l'ordre 2 ; un ordre hors bornes est ramené dans [1, 8]
	for _, tc := range [][2]int{{0, 2}, {-3, 2}, {99, 8}} {
		a := butterworth(fs, 1000, 0, tc[0], true)
		b := butterworth(fs, 1000, 0, tc[1], true)
		if len(a) != len(b) || responseDB(a, fs, 300) != responseDB(b, fs, 300) {
			t.Errorf("ordre %d : doit se comporter comme l'ordre %d", tc[0], tc[1])
		}
	}
}

// Le gain d'une bande de filtre n'a pas de sens : il est ignoré, y compris à 0 (qui, pour les
// autres types, désactive la bande).
func TestFilterBandsIgnoreGain(t *testing.T) {
	const fs = 44100
	for _, typ := range []BandType{HighPass, LowPass} {
		ref := designBands(fs, []Band{{Type: typ, FreqHz: 1000, Order: 4}})
		if len(ref) != 2 {
			t.Fatalf("type %d : %d sections, attendu 2", typ, len(ref))
		}
		for _, g := range []float64{0, 12, -12, math.NaN()} {
			got := designBands(fs, []Band{{Type: typ, FreqHz: 1000, Order: 4, GainDB: g}})
			if len(got) != len(ref) || responseDB(got, fs, 500) != responseDB(ref, fs, 500) {
				t.Errorf("type %d, gain %v : le gain doit être ignoré", typ, g)
			}
		}
	}
}

// Fréquence nulle, négative, NaN ou au-delà de Nyquist : la bande est ignorée.
func TestFilterInvalidCutoffsAreIgnored(t *testing.T) {
	const fs = 44100
	in := sine(1000, fs, 4000)
	for _, f := range []float64{0, -100, math.NaN(), fs / 2, 30000} {
		for _, typ := range []BandType{HighPass, LowPass} {
			out, _ := io.ReadAll(Equalizer(bytes.NewReader(in), fs, 1, []Band{{Type: typ, FreqHz: f}}))
			if !bytes.Equal(in, out) {
				t.Errorf("type %d, fréquence %v : le signal doit rester intact", typ, f)
			}
		}
	}
	for _, c := range []func(io.Reader, int, int, float64, int) io.Reader{HighPassFilter, LowPassFilter} {
		if out, _ := io.ReadAll(c(bytes.NewReader(in), 0, 1, 1000, 2)); !bytes.Equal(in, out) {
			t.Error("sampleRate 0 : le signal doit rester intact")
		}
	}
}

// De bout en bout, sur un flux : gain mesuré à plusieurs fréquences, pour les deux fonctions.
func TestHighLowPassFilterStream(t *testing.T) {
	const fs = 44100
	gain := func(f func(io.Reader, int, int, float64, int) io.Reader, freq float64, order int) float64 {
		in := tone(freq, 8000, fs, 2*fs)
		out, err := io.ReadAll(f(bytes.NewReader(in), fs, 1, 1000, order))
		if err != nil || len(out) != len(in) {
			t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
		}
		return 20 * math.Log10(goertzel(out, freq, fs)/goertzel(in, freq, fs))
	}
	for _, order := range []int{1, 2, 3, 4} { // au-delà, l'atténuation tombe sous le plancher de quantification 16 bits
		wantHigh := -10 * math.Log10(1+math.Pow(4, 2*float64(order))) // à fc/4
		if g := gain(HighPassFilter, 250, order); math.Abs(g-wantHigh) > 1 {
			t.Errorf("passe-haut ordre %d à 250 Hz : %.2f dB, attendu %.2f", order, g, wantHigh)
		}
		if g := gain(HighPassFilter, 8000, order); math.Abs(g) > 0.2 {
			t.Errorf("passe-haut ordre %d à 8 kHz : %.2f dB, attendu 0", order, g)
		}
		if g := gain(LowPassFilter, 4000, order); math.Abs(g-wantHigh) > 1 {
			t.Errorf("passe-bas ordre %d à 4 kHz : %.2f dB, attendu %.2f", order, g, wantHigh)
		}
		if g := gain(LowPassFilter, 100, order); math.Abs(g) > 0.2 {
			t.Errorf("passe-bas ordre %d à 100 Hz : %.2f dB, attendu 0", order, g)
		}
	}
}

// Les filtres se combinent avec les autres bandes d'un Equalizer, dans l'ordre, et la lecture
// fragmentée donne le même résultat.
func TestFilterCombinesWithOtherBands(t *testing.T) {
	const fs = 44100
	bands := []Band{
		{Type: HighPass, FreqHz: 80, Order: 4},
		{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 1},
		{Type: LowPass, FreqHz: 12000, Order: 2},
	}
	in := sine(1000, fs, 8000)
	one, _ := io.ReadAll(Equalizer(bytes.NewReader(in), fs, 1, bands))
	if g := 20 * math.Log10(goertzel(one, 1000, fs)/goertzel(in, 1000, fs)); math.Abs(g-6) > 0.3 {
		t.Errorf("cloche de +6 dB entre les deux filtres : %.2f dB", g)
	}
	// ordre 4 : 2 sections ; cloche : 1 ; ordre 2 : 1
	if n := len(designBands(fs, bands)); n != 4 {
		t.Errorf("%d sections, attendu 4", n)
	}
	got, _ := io.ReadAll(iotest.OneByteReader(Equalizer(bytes.NewReader(in), fs, 1, bands)))
	if !bytes.Equal(got, one) {
		t.Error("la lecture fragmentée doit donner le même résultat")
	}
}
