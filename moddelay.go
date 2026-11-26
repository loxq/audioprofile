package audioprofile

import "math"

// modDelay est une ligne à retard à lecture fractionnaire, pour les effets de modulation (chorus,
// flanger, vibrato). La lecture interpole sur quatre points (Catmull-Rom, un Hermite cubique) : un retard
// qui varie de moins d'un échantillon fait bouger la fraction à chaque trame, et une interpolation
// linéaire ferait alors osciller le niveau des aigus de plusieurs dB (un son de « gargouillis »).
type modDelay struct {
	buf  []float64
	mask int
	pos  int // indice du dernier échantillon écrit
}

// newModDelay renvoie une ligne capable de rendre un retard jusqu'à maxSamples échantillons.
func newModDelay(maxSamples int) *modDelay {
	size := 8
	for size < maxSamples+4 {
		size *= 2
	}
	return &modDelay{buf: make([]float64, size), mask: size - 1}
}

// push écrit l'échantillon courant.
func (m *modDelay) push(x float64) {
	m.pos = (m.pos + 1) & m.mask
	m.buf[m.pos] = x
}

// read renvoie l'échantillon écrit il y a d échantillons (d >= 2, fractionnaire) : d = 1 serait le dernier
// échantillon écrit, mais l'interpolation lit un point plus récent, qui n'existe pas encore ; il faut donc
// d >= 2. Avant le premier push, tout est nul.
func (m *modDelay) read(d float64) float64 {
	i := int(d)
	t := d - float64(i)
	at := func(k int) float64 { return m.buf[(m.pos-k+1)&m.mask] }
	y0, y1, y2, y3 := at(i-1), at(i), at(i+1), at(i+2) // du plus récent au plus ancien, autour de d
	c1 := 0.5 * (y2 - y0)
	c2 := y0 - 2.5*y1 + 2*y2 - 0.5*y3
	c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
	return ((c3*t+c2)*t+c1)*t + y1
}

// lfo est un oscillateur sinusoïdal basse fréquence.
type lfo struct{ phase, inc float64 }

func newLFO(rateHz, fs, phase float64) lfo { return lfo{phase: phase, inc: rateHz / fs} }

// next renvoie la valeur courante (dans [-1, 1]) puis avance.
func (l *lfo) next() float64 {
	v := math.Sin(2 * math.Pi * l.phase)
	l.phase += l.inc
	if l.phase >= 1 {
		l.phase -= math.Floor(l.phase)
	}
	return v
}
