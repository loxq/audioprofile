package bassboost

import (
	"io"
	"math"
)

const (
	bytesPerSm = 2              // PCM 16 bits
	readChunk  = 4096           // taille de lecture (en trames) par passage
	defaultQ   = math.Sqrt2 / 2 // Q par défaut (1/√2) : réponse la plus plate sans surtension
	minQ       = 0.1
	maxBandDB  = 24.0 // |gain| maximal accepté par bande
)

// BandType est le type de filtre d'une bande d'égalisation.
type BandType int

const (
	LowShelf  BandType = iota // renforce/atténue tout ce qui est sous FreqHz
	Peaking                   // cloche centrée sur FreqHz
	HighShelf                 // renforce/atténue tout ce qui est au-dessus de FreqHz
	Gain                      // gain global sur tout le spectre (FreqHz et Q ignorés)
)

// Band décrit une bande d'un égaliseur paramétrique.
type Band struct {
	Type   BandType
	FreqHz float64 // fréquence de coupure (shelf) ou centrale (peaking)
	GainDB float64 // gain en dB (positif = renforcé), ramené dans [-24, +24]
	Q      float64 // facteur de qualité ; <= 0 → 1/√2. Plus Q est grand, plus la bande est étroite
}

// Equalizer renvoie un flux PCM (s16le, entrelacé) auquel les bandes sont
// appliquées en série, en une seule passe et sans tout charger en mémoire.
//
//   - src        : flux PCM d'entrée (16 bits signé little-endian)
//   - sampleRate : fréquence d'échantillonnage en Hz (ex. 44100)
//   - channels   : nombre de canaux (1 = mono, 2 = stéréo...)
//   - bands      : bandes à appliquer
//
// Les bandes sans effet (gain nul, ou fréquence <= 0 ou >= Nyquist hors type Gain) sont ignorées.
// S'il n'en reste aucune, ou si sampleRate <= 0, le flux est renvoyé tel quel.
func Equalizer(src io.Reader, sampleRate, channels int, bands []Band) io.Reader {
	return newEqualizerReader(src, sampleRate, channels, bands, nil)
}

// newEqualizerReader est Equalizer avec, en option, un étage de saturation
// appliqué après les filtres.
func newEqualizerReader(src io.Reader, sampleRate, channels int, bands []Band, sat *saturator) io.Reader {
	if channels < 1 {
		channels = 1
	}
	var coefs []coeffs
	if sampleRate > 0 {
		coefs = designBands(float64(sampleRate), bands)
	}

	r := &reader{src: src, channels: channels, passthru: len(coefs) == 0 && sat == nil, sat: sat}
	if !r.passthru {
		r.filters = make([][]biquad, channels)
		for ch := range r.filters {
			r.filters[ch] = make([]biquad, len(coefs))
			for i, c := range coefs {
				r.filters[ch][i] = biquad{coef: c}
			}
		}
	}
	return r
}

// designBands calcule les coefficients des bandes utiles (ignore les autres).
func designBands(fs float64, bands []Band) []coeffs {
	var coefs []coeffs
	for _, b := range bands {
		if b.GainDB == 0 || math.IsNaN(b.GainDB) {
			continue
		}
		gain := math.Max(-maxBandDB, math.Min(maxBandDB, b.GainDB))
		if b.Type == Gain {
			coefs = append(coefs, coeffs{b0: math.Pow(10, gain/20)})
			continue
		}
		if b.FreqHz <= 0 || b.FreqHz >= fs/2 {
			continue
		}
		q := b.Q
		if q <= 0 {
			q = defaultQ
		}
		q = math.Max(minQ, q)
		switch b.Type {
		case LowShelf:
			coefs = append(coefs, lowShelf(fs, b.FreqHz, gain, q))
		case Peaking:
			coefs = append(coefs, peaking(fs, b.FreqHz, gain, q))
		case HighShelf:
			coefs = append(coefs, highShelf(fs, b.FreqHz, gain, q))
		}
	}
	return coefs
}

// singleBand applique une seule bande dont le gain vaut intensity × maxGainDB,
// avec intensity ramenée dans [-1, 1]. Sert aux préréglages BassBoost,
// MidBoost et TrebleBoost.
func singleBand(src io.Reader, sampleRate, channels int, intensity float64, b Band) io.Reader {
	b.GainDB = math.Max(-1, math.Min(1, intensity)) * maxGainDB
	return Equalizer(src, sampleRate, channels, []Band{b})
}

// ---------------------------------------------------------------- lecteur

type reader struct {
	src      io.Reader
	channels int
	filters  [][]biquad // filters[canal][bande]
	sat      *saturator // saturation + gain de sortie, après les filtres (peut être nil)
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
		y := float64(int16(uint16(b[i]) | uint16(b[i+1])<<8))
		for j := range r.filters[ch] {
			y = r.filters[ch][j].process(y)
		}
		if r.sat != nil {
			y = r.sat.apply(y)
		}
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

// biquad : forme directe II transposée (un état par canal et par bande).
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

// ---------------------------------------------------------------- filtres
// Formules du RBJ Audio EQ Cookbook. Avec q = 1/√2, les shelfs équivalent à
// une pente S = 1.

func lowShelf(fs, f0, gainDB, q float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * q)
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

func highShelf(fs, f0, gainDB, q float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * q)
	tsa := 2 * math.Sqrt(A) * alpha

	a0 := (A + 1) - (A-1)*cosw + tsa
	return coeffs{
		b0: A * ((A + 1) + (A-1)*cosw + tsa) / a0,
		b1: -2 * A * ((A - 1) + (A+1)*cosw) / a0,
		b2: A * ((A + 1) + (A-1)*cosw - tsa) / a0,
		a1: 2 * ((A - 1) - (A+1)*cosw) / a0,
		a2: ((A + 1) - (A-1)*cosw - tsa) / a0,
	}
}

// peaking : filtre en cloche. Le gain gainDB est atteint à f0 ; les
// extrémités du spectre restent inchangées.
func peaking(fs, f0, gainDB, q float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * q)

	a0 := 1 + alpha/A
	return coeffs{
		b0: (1 + alpha*A) / a0,
		b1: -2 * cosw / a0,
		b2: (1 - alpha*A) / a0,
		a1: -2 * cosw / a0,
		a2: (1 - alpha/A) / a0,
	}
}
