package audioprofile

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

	postCeilingKnee = 0.8 // genou du plafond doux après les filtres finaux (marge plus large : ils déphasent l'onde saturée)
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
	return newEqualizerReader(src, sampleRate, channels, bands, nil, nil)
}

// chain décrit la chaîne de traitement d'un lecteur, dans l'ordre :
// égalisation → compresseur → saturation (+ gain de sortie) → filtres finaux.
type chain struct {
	bands []Band              // égalisation
	comp  *CompressorSettings // compresseur (nil = aucun)
	sat   *saturation         // saturation (nil = aucune)
	post  []coeffs            // filtres appliqués après la saturation (timbre, baffle…)
}

// newEqualizerReader est Equalizer avec, en option, un compresseur puis une
// saturation appliqués dans cet ordre après les filtres.
func newEqualizerReader(src io.Reader, sampleRate, channels int, bands []Band, comp *CompressorSettings, sat *saturation) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{bands: bands, comp: comp, sat: sat})
}

// newChainReader construit un lecteur qui applique la chaîne c.
func newChainReader(src io.Reader, sampleRate, channels int, c chain) io.Reader {
	if channels < 1 {
		channels = 1
	}
	var coefs, post []coeffs
	var cmp *compressor
	var sats []*saturator
	if sampleRate > 0 {
		fs := float64(sampleRate)
		coefs = designBands(fs, c.bands)
		cmp = newCompressor(fs, c.comp)
		if c.sat != nil {
			sats = newSaturators(fs, channels, *c.sat)
		}
		post = c.post
	}
	r := &reader{
		src: src, channels: channels, sat: sats, comp: cmp,
		passthru: len(coefs) == 0 && sats == nil && cmp == nil && len(post) == 0,
		frame:    make([]float64, channels),
	}
	if !r.passthru {
		r.filters = make([][]biquad, channels)
		r.post = make([][]biquad, channels)
		for ch := range r.filters {
			r.filters[ch] = make([]biquad, len(coefs))
			for i, co := range coefs {
				r.filters[ch][i] = biquad{coef: co}
			}
			r.post[ch] = make([]biquad, len(post))
			for i, co := range post {
				r.post[ch][i] = biquad{coef: co}
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
	filters  [][]biquad   // filters[canal][bande]
	comp     *compressor  // compresseur, après les filtres (peut être nil)
	sat      []*saturator // saturation + gain de sortie, un par canal, après le compresseur (peut être nil)
	post     [][]biquad   // post[canal][filtre] : filtres appliqués après la saturation
	frame    []float64    // tampon de travail : une trame (un échantillon par canal)
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
	step := r.channels * bytesPerSm
	for i := 0; i+step <= len(b); i += step {
		for ch := 0; ch < r.channels; ch++ {
			j := i + ch*bytesPerSm
			y := float64(int16(uint16(b[j]) | uint16(b[j+1])<<8))
			for k := range r.filters[ch] {
				y = r.filters[ch][k].process(y)
			}
			r.frame[ch] = y
		}
		if r.comp != nil {
			r.comp.apply(r.frame)
		}
		for ch := 0; ch < r.channels; ch++ {
			y := r.frame[ch]
			if r.sat != nil {
				y = r.sat[ch].apply(y)
			}
			if len(r.post[ch]) > 0 {
				for k := range r.post[ch] {
					y = r.post[ch][k].process(y)
				}
				// les filtres finaux déphasent l'onde saturée : ses pics peuvent
				// dépasser la pleine échelle même avec un gain de 0 dB au maximum
				y = 32768 * softCeilingAt(y/32768, postCeilingKnee)
			}
			// écrêtage pour éviter le débordement d'entier
			y = math.Max(-32768, math.Min(32767, math.Round(y)))
			v := uint16(int16(y))
			j := i + ch*bytesPerSm
			b[j], b[j+1] = byte(v), byte(v>>8)
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

// lowPass : filtre passe-bas du second ordre (RBJ Audio EQ Cookbook).
func lowPass(fs, f0, q float64) coeffs {
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * q)

	a0 := 1 + alpha
	return coeffs{
		b0: (1 - cosw) / 2 / a0,
		b1: (1 - cosw) / a0,
		b2: (1 - cosw) / 2 / a0,
		a1: -2 * cosw / a0,
		a2: (1 - alpha) / a0,
	}
}

// highPass : filtre passe-haut du second ordre (RBJ Audio EQ Cookbook).
func highPass(fs, f0, q float64) coeffs {
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * q)

	a0 := 1 + alpha
	return coeffs{
		b0: (1 + cosw) / 2 / a0,
		b1: -(1 + cosw) / a0,
		b2: (1 + cosw) / 2 / a0,
		a1: -2 * cosw / a0,
		a2: (1 - alpha) / a0,
	}
}
