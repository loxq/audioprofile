package audioprofile

import (
	"io"
	"math"
	"sort"
)

// Loudness est le résultat d'une mesure de sonie selon l'UIT-R BS.1770-4 et l'EBU R128.
type Loudness struct {
	IntegratedLUFS float64 // sonie intégrée, avec double porte (−70 LUFS absolu, −10 LU relatif) ; valable si Valid
	RangeLU        float64 // plage de sonie (LRA, EBU Tech 3342), en LU ; valable si HasRange
	PeakDBFS       float64 // crête d'échantillon, en dBFS (silenceLevelDB pour du silence)

	Valid    bool // au moins un bloc de 400 ms au-dessus de la porte : IntegratedLUFS a un sens
	HasRange bool // au moins un niveau à court terme (3 s) au-dessus de la porte : RangeLU a un sens
}

const (
	lufsOffset      = -0.691 // décalage de l'UIT-R BS.1770 : −0,691 + 10·log10(somme des carrés filtrés)
	absoluteGateLU  = -70.0  // porte absolue
	relativeGateLU  = -10.0  // porte relative de la sonie intégrée
	rangeGateLU     = -20.0  // porte relative de la plage de sonie
	loudnessHopMs   = 100.0  // pas des blocs : un bloc de 400 ms toutes les 100 ms (recouvrement de 75 %)
	momentaryBlocks = 4      // 400 ms = 4 pas
	shortTermBlocks = 30     // 3 s = 30 pas
)

// kWeighting renvoie les deux filtres de la pondération K de l'UIT-R BS.1770 : un shelf aigu
// (+4 dB vers 1,7 kHz, qui modélise la tête) puis un passe-haut (38 Hz). Les coefficients sont
// calculés pour la fréquence d'échantillonnage (formules de libebur128) ; à 48 kHz, ils
// retombent sur ceux de la table de la recommandation.
func kWeighting(fs float64) (shelf, highPass coeffs) {
	{
		const f0, gainDB, q = 1681.974450955533, 3.999843853973347, 0.7071752369554196
		k := math.Tan(math.Pi * f0 / fs)
		vh := math.Pow(10, gainDB/20)
		vb := math.Pow(vh, 0.4996667741545416)
		a0 := 1 + k/q + k*k
		shelf = coeffs{
			b0: (vh + vb*k/q + k*k) / a0,
			b1: 2 * (k*k - vh) / a0,
			b2: (vh - vb*k/q + k*k) / a0,
			a1: 2 * (k*k - 1) / a0,
			a2: (1 - k/q + k*k) / a0,
		}
	}
	{
		const f0, q = 38.13547087602444, 0.5003270373238773
		k := math.Tan(math.Pi * f0 / fs)
		a0 := 1 + k/q + k*k
		highPass = coeffs{b0: 1, b1: -2, b2: 1, a1: 2 * (k*k - 1) / a0, a2: (1 - k/q + k*k) / a0}
	}
	return
}

// LoudnessMeter mesure la sonie d'un flux PCM (s16le, entrelacé) au fil de l'eau : il
// reçoit les octets par Write, par morceaux de n'importe quelle taille, et ne garde en
// mémoire que l'énergie de chaque pas de 100 ms.
//
// Tous les canaux ont le même poids : c'est exact en mono et en stéréo, approché au-delà
// (la recommandation pondère les canaux d'ambiance de 5.1 et ignore le canal des basses).
type LoudnessMeter struct {
	channels int
	hop      int // trames par pas de 100 ms
	valid    bool

	shelf, hp [][2]biquad // un couple de filtres par canal : [0] = shelf, [1] = passe-haut
	pending   []byte      // octets d'une trame incomplète

	acc   float64   // somme des carrés filtrés du pas en cours, tous canaux confondus
	inHop int       // trames dans le pas en cours
	steps []float64 // énergie moyenne de chaque pas terminé (somme sur les canaux, par échantillon)
	peak  float64   // plus grande valeur absolue vue (échelle 16 bits)
}

// NewLoudnessMeter renvoie un mesureur de sonie pour un flux de sampleRate Hz et channels
// canaux. Avec des paramètres invalides, il ignore ce qu'on lui envoie et ne produit aucun
// résultat valable.
func NewLoudnessMeter(sampleRate, channels int) *LoudnessMeter {
	m := &LoudnessMeter{channels: channels}
	if sampleRate <= 0 || channels < 1 {
		return m
	}
	m.valid = true
	m.hop = int(math.Round(loudnessHopMs / 1000 * float64(sampleRate)))
	if m.hop < 1 {
		m.hop = 1
	}
	s, h := kWeighting(float64(sampleRate))
	m.shelf = make([][2]biquad, channels)
	for ch := range m.shelf {
		m.shelf[ch] = [2]biquad{{coef: s}, {coef: h}}
	}
	return m
}

// Write ajoute des échantillons à la mesure. Il renvoie toujours len(p), nil.
func (m *LoudnessMeter) Write(p []byte) (int, error) {
	n := len(p)
	if !m.valid {
		return n, nil
	}
	frameSize := m.channels * bytesPerSm
	if len(m.pending) > 0 {
		need := frameSize - len(m.pending)
		if len(p) < need {
			m.pending = append(m.pending, p...)
			return n, nil
		}
		m.pending = append(m.pending, p[:need]...)
		m.frame(m.pending)
		m.pending = m.pending[:0]
		p = p[need:]
	}
	for len(p) >= frameSize {
		m.frame(p[:frameSize])
		p = p[frameSize:]
	}
	m.pending = append(m.pending, p...)
	return n, nil
}

// frame traite une trame complète.
func (m *LoudnessMeter) frame(b []byte) {
	for ch := 0; ch < m.channels; ch++ {
		v := float64(int16(uint16(b[2*ch]) | uint16(b[2*ch+1])<<8))
		m.peak = math.Max(m.peak, math.Abs(v))
		z := m.shelf[ch][1].process(m.shelf[ch][0].process(v / 32768))
		m.acc += z * z
	}
	m.inHop++
	if m.inHop == m.hop {
		m.steps = append(m.steps, m.acc/float64(m.hop))
		m.acc, m.inHop = 0, 0
	}
}

// lufs convertit une énergie moyenne (somme sur les canaux) en LUFS.
func lufs(energy float64) float64 { return lufsOffset + 10*math.Log10(energy) }

// Loudness renvoie la mesure sur tout ce qui a été reçu. Elle peut être appelée à tout moment,
// et la mesure peut continuer ensuite.
func (m *LoudnessMeter) Loudness() Loudness {
	res := Loudness{PeakDBFS: silenceLevelDB}
	if m.peak > 0 {
		res.PeakDBFS = 20 * math.Log10(m.peak/32768)
	}
	// blocs de 400 ms (moyenne de 4 pas) et niveaux à court terme de 3 s (moyenne de 30 pas)
	blocks := slidingMean(m.steps, momentaryBlocks)
	if g, ok := gatedMean(blocks, absoluteGateLU, relativeGateLU); ok {
		res.IntegratedLUFS, res.Valid = lufs(g), true
	}
	short := slidingMean(m.steps, shortTermBlocks)
	if lra, ok := loudnessRange(short); ok {
		res.RangeLU, res.HasRange = lra, true
	}
	return res
}

// slidingMean renvoie la moyenne glissante, sur n valeurs, de v (une valeur par fenêtre complète).
func slidingMean(v []float64, n int) []float64 {
	if len(v) < n {
		return nil
	}
	out := make([]float64, 0, len(v)-n+1)
	var sum float64
	for i, x := range v {
		sum += x
		if i >= n {
			sum -= v[i-n]
		}
		if i >= n-1 {
			out = append(out, sum/float64(n))
		}
	}
	return out
}

// gatedMean applique la porte absolue (absLU) puis la porte relative (relLU sous la moyenne des
// blocs restants) à des énergies de blocs, et renvoie l'énergie moyenne des blocs retenus.
// Les deux portes se calculent sur la moyenne des énergies, jamais sur une moyenne de LUFS.
func gatedMean(energies []float64, absLU, relLU float64) (float64, bool) {
	absE := math.Pow(10, (absLU-lufsOffset)/10)
	mean := func(keep func(e float64) bool) (float64, int) {
		var s float64
		var n int
		for _, e := range energies {
			if e > absE && keep(e) {
				s += e
				n++
			}
		}
		if n == 0 {
			return 0, 0
		}
		return s / float64(n), n
	}
	ungated, n := mean(func(float64) bool { return true })
	if n == 0 {
		return 0, false
	}
	relE := math.Pow(10, (lufs(ungated)+relLU-lufsOffset)/10)
	g, n := mean(func(e float64) bool { return e > relE })
	return g, n > 0
}

// loudnessRange calcule la plage de sonie (EBU Tech 3342) à partir des énergies à court terme :
// porte absolue de −70 LUFS, porte relative de −20 LU, puis écart entre les centiles 10 % et 95 %.
func loudnessRange(shortTerm []float64) (float64, bool) {
	absE := math.Pow(10, (absoluteGateLU-lufsOffset)/10)
	var s float64
	var n int
	for _, e := range shortTerm {
		if e > absE {
			s += e
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	relE := math.Pow(10, (lufs(s/float64(n))+rangeGateLU-lufsOffset)/10)
	var levels []float64
	for _, e := range shortTerm {
		if e > absE && e > relE {
			levels = append(levels, lufs(e))
		}
	}
	if len(levels) == 0 {
		return 0, false
	}
	sort.Float64s(levels)
	at := func(p float64) float64 { return levels[int(p*float64(len(levels)-1)+0.5)] }
	return at(0.95) - at(0.10), true
}

// MeasureLoudness lit tout src et renvoie sa sonie. Rien n'est gardé en mémoire à part
// l'énergie de chaque pas de 100 ms (environ 10 valeurs par seconde).
func MeasureLoudness(src io.Reader, sampleRate, channels int) (Loudness, error) {
	m := NewLoudnessMeter(sampleRate, channels)
	_, err := io.Copy(m, src)
	return m.Loudness(), err
}
