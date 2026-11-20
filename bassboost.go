// Package bassboost applique un renforcement des basses (filtre low-shelf)
// sur un flux PCM 16 bits signé little-endian, entrelacé.
package bassboost

import (
	"io"
	"math"
)

const (
	cutoffHz   = 120.0 // fréquence de coupure du shelf
	maxGainDB  = 15.0  // gain à |intensity| = 1
	readChunk  = 4096  // taille de lecture (en trames) par passage
	bytesPerSm = 2     // PCM 16 bits
)

// BassBoost renvoie un flux PCM (s16le, entrelacé) dont les basses sont
// renforcées. Le traitement se fait au fil de l'eau, sans tout charger en mémoire.
//
//   - src        : flux PCM d'entrée (16 bits signé little-endian)
//   - sampleRate : fréquence d'échantillonnage en Hz (ex. 44100)
//   - channels   : nombre de canaux (1 = mono, 2 = stéréo...)
//   - intensity  : intensité de l'effet de -1.0 (basses atténuées de 15 dB)
//     à +1.0 (basses renforcées de 15 dB), 0.0 = aucun effet.
//     Les valeurs hors de cet intervalle sont ramenées dans [-1, 1].
func BassBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader {
	return newFilterReader(src, sampleRate, channels, intensity, cutoffHz, lowShelf)
}

// newFilterReader construit un lecteur appliquant le filtre biquad produit par
// design à la fréquence de coupure cutoff. Factorisé entre BassBoost, MidBoost et TrebleBoost.
func newFilterReader(src io.Reader, sampleRate, channels int, intensity, cutoff float64,
	design func(fs, f0, gainDB float64) coeffs) io.Reader {
	if channels < 1 {
		channels = 1
	}
	intensity = math.Max(-1, math.Min(1, intensity))

	r := &reader{
		src:      src,
		channels: channels,
		filters:  make([]biquad, channels),
		// pas de filtrage si l'effet est nul ou si la coupure dépasse Nyquist
		passthru: intensity == 0 || sampleRate <= 0 || cutoff >= float64(sampleRate)/2,
	}
	if !r.passthru {
		coef := design(float64(sampleRate), cutoff, intensity*maxGainDB)
		for i := range r.filters {
			r.filters[i] = biquad{coef: coef}
		}
	}
	return r
}

// ---------------------------------------------------------------- lecteur

type reader struct {
	src      io.Reader
	channels int
	filters  []biquad
	passthru bool

	pending []byte // octets lus mais ne formant pas encore une trame complète
	out     []byte // octets traités, pas encore rendus à l'appelant
	err     error
}

func (r *reader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		r.fill()
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

// fill lit un bloc depuis la source et traite les trames complètes.
func (r *reader) fill() {
	frameSize := r.channels * bytesPerSm
	buf := make([]byte, readChunk*frameSize)
	copy(buf, r.pending)
	n, err := r.src.Read(buf[len(r.pending):])
	total := len(r.pending) + n

	usable := total - total%frameSize
	r.pending = append(r.pending[:0], buf[usable:total]...)
	r.out = r.process(buf[:usable])

	if err != nil {
		r.err = err // une éventuelle trame incomplète finale est ignorée
	}
}

func (r *reader) process(b []byte) []byte {
	if r.passthru {
		return b
	}
	ch := 0
	for i := 0; i+1 < len(b); i += bytesPerSm {
		s := float64(int16(uint16(b[i]) | uint16(b[i+1])<<8))
		y := r.filters[ch].process(s)
		// écrêtage pour éviter le débordement d'entier
		y = math.Max(-32768, math.Min(32767, math.Round(y)))
		v := uint16(int16(y))
		b[i], b[i+1] = byte(v), byte(v>>8)
		if ch++; ch == r.channels {
			ch = 0
		}
	}
	return b
}

// ---------------------------------------------------------------- biquad

type coeffs struct{ b0, b1, b2, a1, a2 float64 }

// biquad : forme directe II transposée (un état par canal).
type biquad struct {
	coef   coeffs
	z1, z2 float64
}

func (f *biquad) process(x float64) float64 {
	c := &f.coef
	y := c.b0*x + f.z1
	f.z1 = c.b1*x - c.a1*y + f.z2
	f.z2 = c.b2*x - c.a2*y
	return y
}

// lowShelf calcule un filtre low-shelf (RBJ Audio EQ Cookbook, pente S = 1).
func lowShelf(fs, f0, gainDB float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / 2 * math.Sqrt2
	tsa := 2 * math.Sqrt(A) * alpha

	a0 := (A + 1) + (A-1)*cosw + tsa
	return coeffs{
		b0: A * ((A + 1) - (A-1)*cosw + tsa) / a0,
		b1: 2 * A * ((A - 1) - (A+1)*cosw) / a0,
		b2: A * ((A + 1) - (A-1)*cosw - tsa) / a0,
		a1: -2 * ((A - 1) + (A+1)*cosw) / a0,
		a2: ((A + 1) + (A-1)*cosw - tsa) / a0,
	}
}
