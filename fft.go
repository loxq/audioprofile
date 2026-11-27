package audioprofile

import (
	"math"
	"math/bits"
)

// fft est une transformée de Fourier rapide (radix 2, sur place) de taille fixe. Elle sert à l'analyse du spectre de Meter ;
// elle n'est pas thread-safe (les tampons de travail sont à l'appelant).
type fft struct {
	n        int
	cos, sin []float64 // les n/2 racines de l'unité, cos(2πk/n) et −sin(2πk/n)
	rev      []int     // permutation par inversion des bits
}

// newFFT prépare une transformée de taille n, une puissance de deux d'au moins 2.
func newFFT(n int) *fft {
	f := &fft{n: n, cos: make([]float64, n/2), sin: make([]float64, n/2), rev: make([]int, n)}
	for k := range f.cos {
		a := 2 * math.Pi * float64(k) / float64(n)
		f.cos[k], f.sin[k] = math.Cos(a), -math.Sin(a)
	}
	shift := bits.UintSize - bits.Len(uint(n-1))
	for i := range f.rev {
		f.rev[i] = int(bits.Reverse(uint(i)) >> shift)
	}
	return f
}

// transform calcule la transformée directe de (re, im), de longueur n, sur place.
func (f *fft) transform(re, im []float64) {
	for i, j := range f.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for start := 0; start < f.n; start += size {
			for k := 0; k < half; k++ {
				c, s := f.cos[k*step], f.sin[k*step]
				a, b := start+k, start+k+half
				tr := re[b]*c - im[b]*s
				ti := re[b]*s + im[b]*c
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}
