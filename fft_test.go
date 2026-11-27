package audioprofile

import (
	"math"
	"math/cmplx"
	"math/rand"
	"testing"
)

func naiveDFT(re, im []float64) ([]float64, []float64) {
	n := len(re)
	outRe, outIm := make([]float64, n), make([]float64, n)
	for k := 0; k < n; k++ {
		var s complex128
		for t := 0; t < n; t++ {
			s += complex(re[t], im[t]) * cmplx.Rect(1, -2*math.Pi*float64(k*t)/float64(n))
		}
		outRe[k], outIm[k] = real(s), imag(s)
	}
	return outRe, outIm
}

// La transformée rapide donne celle de la définition, pour plusieurs tailles et des entrées complexes quelconques.
func TestFFTMatchesNaiveDFT(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, n := range []int{2, 4, 8, 16, 64, 256} {
		re, im := make([]float64, n), make([]float64, n)
		for i := range re {
			re[i], im[i] = rng.NormFloat64(), rng.NormFloat64()
		}
		wantRe, wantIm := naiveDFT(re, im)
		newFFT(n).transform(re, im)
		for k := range re {
			if math.Abs(re[k]-wantRe[k]) > 1e-9 || math.Abs(im[k]-wantIm[k]) > 1e-9 {
				t.Fatalf("n=%d, case %d : (%g, %g), attendu (%g, %g)", n, k, re[k], im[k], wantRe[k], wantIm[k])
			}
		}
	}
}

func TestFFTKnownSignals(t *testing.T) {
	const n = 1024
	f := newFFT(n)
	// une impulsion a un spectre plat
	re, im := make([]float64, n), make([]float64, n)
	re[0] = 1
	f.transform(re, im)
	for k := range re {
		if math.Abs(re[k]-1) > 1e-12 || math.Abs(im[k]) > 1e-12 {
			t.Fatalf("impulsion : case %d = (%g, %g)", k, re[k], im[k])
		}
	}
	// un cosinus de 100 cycles tombe dans la case 100 (et sa symétrique), d'amplitude n/2
	for i := range re {
		re[i], im[i] = math.Cos(2*math.Pi*100*float64(i)/n), 0
	}
	f.transform(re, im)
	for k := range re {
		mag := math.Hypot(re[k], im[k])
		if k == 100 || k == n-100 {
			if math.Abs(mag-n/2) > 1e-6 {
				t.Errorf("case %d : %g, attendu %d", k, mag, n/2)
			}
		} else if mag > 1e-6 {
			t.Errorf("case %d : %g, attendu 0", k, mag)
		}
	}
	// Parseval : l'énergie est conservée (à un facteur n près)
	rng := rand.New(rand.NewSource(2))
	var energy float64
	for i := range re {
		re[i], im[i] = rng.NormFloat64(), 0
		energy += re[i] * re[i]
	}
	f.transform(re, im)
	var spectral float64
	for k := range re {
		spectral += re[k]*re[k] + im[k]*im[k]
	}
	if math.Abs(spectral/n-energy) > 1e-6*energy {
		t.Errorf("Parseval : %g contre %g", spectral/n, energy)
	}
}
