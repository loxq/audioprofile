package audioprofile

import (
	"io"
	"math"
	"sync"
)

const (
	// MeterFloorDB est la valeur renvoyée pour un niveau nul ou inconnu (silence, fenêtre pas encore remplie) : un nombre
	// fini, pour qu'un instantané s'écrive tel quel en JSON.
	MeterFloorDB = -120.0

	meterSubMs     = 10  // durée d'un pas de mesure
	meterPeakSteps = 20  // fenêtre de la crête : 200 ms
	meterRMSSteps  = 30  // fenêtre du niveau efficace : 300 ms (celle d'un vumètre)
	meterMomSteps  = 40  // sonie momentanée : 400 ms
	meterShortStep = 300 // sonie à court terme : 3 s
	meterFFTSize   = 4096
	meterBands     = 64
	meterLowHz     = 20.0
	meterHighHz    = 20000.0
)

// Meter mesure un flux PCM (s16le, entrelacé) au fil de l'eau sans le modifier : niveaux de crête et efficace par canal,
// sonie momentanée et à court terme (EBU R128), spectre en bandes. C'est ce qu'il faut pour afficher un vumètre ou
// un analyseur, ou surveiller un flux en exploitation (silence, écrêtage, dérive de sonie).
//
// Un Meter se branche comme un io.TeeReader : Wrap renvoie le flux, identique, mesuré au passage ; ou Write reçoit les
// octets (il est un io.Writer, pour io.MultiWriter ou io.TeeReader). Les octets peuvent arriver par morceaux de
// n'importe quelle taille. Snapshot, lui, se lit depuis n'importe quel goroutine, sans gêner le flux : il ne consomme
// rien, plusieurs lecteurs (plusieurs pages ouvertes) voient les mêmes valeurs.
//
// Un Meter pour le flux avant le traitement et un autre pour le flux après permettent de voir ce que fait la chaîne.
type Meter struct {
	rate, channels int
	valid          bool
	subFrames      int // trames par pas de mesure

	mu      sync.Mutex
	pending []byte // octets d'une trame incomplète
	frames  int64  // trames mesurées depuis le début
	clips   int64  // échantillons à la pleine échelle
	shelf   [][2]biquad
	// pas en cours
	curN     int
	curSumSq []float64
	curPeak  []float64
	curKW    float64
	// anneau des pas terminés
	ringSumSq []float64 // [pas*channels + canal]
	ringPeak  []float64
	ringKW    []float64 // énergie pondérée K du pas, sommée sur les canaux, par trame
	ringHead  int       // prochain pas à écrire
	ringCount int       // pas écrits, au plus meterShortStep
	// spectre : les dernières meterFFTSize trames, mélangées en mono
	mono     []float32
	monoHead int
	spectrum spectrumAnalyzer
}

// spectrumAnalyzer garde de quoi calculer le spectre : fenêtre, transformée, bandes, et le dernier résultat.
type spectrumAnalyzer struct {
	mu       sync.Mutex // protège les tampons de travail et le cache
	fft      *fft
	window   []float64
	re, im   []float64
	loBin    []int // première et dernière case de chaque bande
	hiBin    []int
	freqs    []float64
	cacheKey int64 // nombre de trames du dernier calcul (−1 : aucun)
	cache    []float64
}

// MeterSnapshot est l'état d'un Meter à un instant. Les niveaux sont en dBFS (0 = pleine échelle d'un sinus), sonie en
// LUFS ; MeterFloorDB signifie silence ou valeur pas encore connue.
type MeterSnapshot struct {
	Frames int64 // trames mesurées depuis le début

	PeakDBFS  []float64 // par canal : crête d'échantillon sur les 200 dernières ms
	RMSDBFS   []float64 // par canal : niveau efficace sur les 300 dernières ms
	ClipCount int64     // échantillons à la pleine échelle depuis le début (cumul : comparez deux instantanés)

	MomentaryLUFS  float64 // sonie sur 400 ms
	MomentaryValid bool    // au moins 400 ms mesurées
	ShortTermLUFS  float64 // sonie sur 3 s
	ShortTermValid bool    // au moins 3 s mesurées

	// SpectrumDBFS : niveau de chaque bande de SpectrumFreqs, sur les 4096 dernières trames (fenêtre de Hann). Dans une
	// bande, c'est la plus forte case de la transformée : un sinus d'amplitude A y lit 20·log10(A) à 1,4 dB près, un bruit y
	// lit à plat.
	SpectrumDBFS []float64
}

// NewMeter renvoie un mesureur pour un flux de sampleRate Hz et channels canaux. Avec des paramètres invalides, il ignore
// ce qu'on lui envoie et ne renvoie que des valeurs de silence.
func NewMeter(sampleRate, channels int) *Meter {
	m := &Meter{rate: sampleRate, channels: channels}
	m.spectrum.cacheKey = -1
	if sampleRate <= 0 || channels < 1 {
		m.spectrum.freqs = []float64{}
		return m
	}
	m.valid = true
	m.subFrames = int(math.Round(float64(meterSubMs) / 1000 * float64(sampleRate))) // attention : meterSubMs / 1000 seul serait une division entière
	if m.subFrames < 1 {
		m.subFrames = 1
	}
	s, h := kWeighting(float64(sampleRate))
	m.shelf = make([][2]biquad, channels)
	for ch := range m.shelf {
		m.shelf[ch] = [2]biquad{{coef: s}, {coef: h}}
	}
	m.curSumSq = make([]float64, channels)
	m.curPeak = make([]float64, channels)
	m.ringSumSq = make([]float64, meterShortStep*channels)
	m.ringPeak = make([]float64, meterShortStep*channels)
	m.ringKW = make([]float64, meterShortStep)
	m.mono = make([]float32, meterFFTSize)
	m.initSpectrum()
	return m
}

func (m *Meter) initSpectrum() {
	sp := &m.spectrum
	sp.fft = newFFT(meterFFTSize)
	sp.window = make([]float64, meterFFTSize)
	for i := range sp.window {
		sp.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(meterFFTSize)) // Hann périodique
	}
	sp.re, sp.im = make([]float64, meterFFTSize), make([]float64, meterFFTSize)
	hi := math.Min(meterHighHz, float64(m.rate)/2*0.95)
	lo := meterLowHz
	if hi <= lo*2 { // fréquence d'échantillonnage minuscule : une seule bande plate
		hi = lo * 2
	}
	binHz := float64(m.rate) / meterFFTSize
	ratio := math.Pow(hi/lo, 1.0/meterBands)
	maxBin := meterFFTSize/2 - 1
	for b := 0; b < meterBands; b++ {
		fLo, fHi := lo*math.Pow(ratio, float64(b)), lo*math.Pow(ratio, float64(b+1))
		center := math.Sqrt(fLo * fHi)
		first, last := int(math.Ceil(fLo/binHz)), int(math.Floor(fHi/binHz))
		if last < first { // bande plus étroite qu'une case : la case la plus proche du centre
			first = int(math.Round(center / binHz))
			last = first
		}
		first, last = min(max(first, 1), maxBin), min(max(last, 1), maxBin)
		sp.loBin, sp.hiBin = append(sp.loBin, first), append(sp.hiBin, last)
		sp.freqs = append(sp.freqs, center)
	}
}

// SpectrumFreqs renvoie la fréquence centrale (Hz) de chaque bande de SpectrumDBFS : 64 bandes réparties en échelle
// logarithmique de 20 Hz à 20 kHz (ou un peu sous Nyquist). La liste est la même pour toute la vie du Meter.
func (m *Meter) SpectrumFreqs() []float64 { return append([]float64{}, m.spectrum.freqs...) }

// Write mesure p. Il renvoie toujours len(p), nil : un Meter n'interrompt jamais un flux.
func (m *Meter) Write(p []byte) (int, error) {
	n := len(p)
	if !m.valid || n == 0 {
		return n, nil
	}
	frameSize := m.channels * bytesPerSm
	m.mu.Lock()
	defer m.mu.Unlock()
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

// Wrap renvoie src à l'identique, en mesurant ce qui passe.
func (m *Meter) Wrap(src io.Reader) io.Reader { return &meterReader{src: src, m: m} }

type meterReader struct {
	src io.Reader
	m   *Meter
}

func (r *meterReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		r.m.Write(p[:n])
	}
	return n, err
}

// frame mesure une trame complète (m.mu tenu).
func (m *Meter) frame(b []byte) {
	var mono float64
	for ch := 0; ch < m.channels; ch++ {
		raw := int16(uint16(b[2*ch]) | uint16(b[2*ch+1])<<8)
		v := float64(raw)
		if raw == math.MaxInt16 || raw == math.MinInt16 {
			m.clips++
		}
		a := math.Abs(v)
		m.curSumSq[ch] += v * v
		if a > m.curPeak[ch] {
			m.curPeak[ch] = a
		}
		z := m.shelf[ch][1].process(m.shelf[ch][0].process(v / 32768))
		m.curKW += z * z
		mono += v
	}
	m.mono[m.monoHead] = float32(mono / float64(m.channels) / 32768)
	if m.monoHead++; m.monoHead == len(m.mono) {
		m.monoHead = 0
	}
	m.frames++
	if m.curN++; m.curN == m.subFrames {
		m.endStep()
	}
}

// endStep range le pas qui vient de finir dans l'anneau.
func (m *Meter) endStep() {
	base := m.ringHead * m.channels
	copy(m.ringSumSq[base:base+m.channels], m.curSumSq)
	copy(m.ringPeak[base:base+m.channels], m.curPeak)
	m.ringKW[m.ringHead] = m.curKW / float64(m.subFrames)
	for ch := range m.curSumSq {
		m.curSumSq[ch], m.curPeak[ch] = 0, 0
	}
	m.curKW, m.curN = 0, 0
	if m.ringHead++; m.ringHead == meterShortStep {
		m.ringHead = 0
	}
	if m.ringCount < meterShortStep {
		m.ringCount++
	}
}

func toDB(power float64) float64 {
	if !(power > 1e-12) { // inclut NaN et zéro
		return MeterFloorDB
	}
	return math.Max(MeterFloorDB, 10*math.Log10(power))
}

// lastSteps renvoie les indices des n derniers pas écrits, du plus récent au plus ancien (au plus ringCount).
func (m *Meter) lastSteps(n int) []int {
	n = min(n, m.ringCount)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = (m.ringHead - 1 - i + meterShortStep) % meterShortStep
	}
	return idx
}

// Snapshot renvoie l'état de la mesure. Il ne consomme rien et peut être appelé depuis n'importe quel goroutine.
func (m *Meter) Snapshot() MeterSnapshot {
	if !m.valid {
		return MeterSnapshot{PeakDBFS: []float64{}, RMSDBFS: []float64{}, SpectrumDBFS: []float64{}, MomentaryLUFS: MeterFloorDB, ShortTermLUFS: MeterFloorDB}
	}
	m.mu.Lock()
	snap := MeterSnapshot{Frames: m.frames, ClipCount: m.clips, PeakDBFS: make([]float64, m.channels), RMSDBFS: make([]float64, m.channels),
		MomentaryLUFS: MeterFloorDB, ShortTermLUFS: MeterFloorDB}
	peakIdx, rmsIdx := m.lastSteps(meterPeakSteps), m.lastSteps(meterRMSSteps)
	for ch := 0; ch < m.channels; ch++ {
		peak, sum := 0.0, 0.0
		for _, i := range peakIdx {
			peak = math.Max(peak, m.ringPeak[i*m.channels+ch])
		}
		peak = math.Max(peak, m.curPeak[ch]) // le pas en cours compte aussi : une crête ne doit pas attendre 10 ms
		for _, i := range rmsIdx {
			sum += m.ringSumSq[i*m.channels+ch]
		}
		snap.PeakDBFS[ch] = peakDB(peak)
		if len(rmsIdx) > 0 {
			snap.RMSDBFS[ch] = toDB(sum / float64(len(rmsIdx)*m.subFrames) / 32768 / 32768)
		} else {
			snap.RMSDBFS[ch] = MeterFloorDB
		}
	}
	snap.MomentaryLUFS, snap.MomentaryValid = m.loudness(meterMomSteps)
	snap.ShortTermLUFS, snap.ShortTermValid = m.loudness(meterShortStep)
	frames := m.frames
	var ring []float32
	if frames > 0 { // avant que l'anneau soit plein, ses cases vides sont des zéros, les plus anciens
		ring = make([]float32, len(m.mono))
		copy(ring, m.mono[m.monoHead:]) // les plus anciennes d'abord
		copy(ring[len(m.mono)-m.monoHead:], m.mono[:m.monoHead])
	}
	m.mu.Unlock()

	snap.SpectrumDBFS = m.spectrumOf(frames, ring)
	return snap
}

func peakDB(v float64) float64 {
	if v <= 0 {
		return MeterFloorDB
	}
	return math.Max(MeterFloorDB, 20*math.Log10(v/32768))
}

// loudness donne la sonie sur les steps derniers pas ; valid est faux tant que la fenêtre n'est pas pleine.
func (m *Meter) loudness(steps int) (lufsValue float64, valid bool) {
	idx := m.lastSteps(steps)
	if len(idx) == 0 {
		return MeterFloorDB, false
	}
	sum := 0.0
	for _, i := range idx {
		sum += m.ringKW[i]
	}
	e := sum / float64(len(idx))
	if !(e > 1e-12) {
		return MeterFloorDB, len(idx) == steps
	}
	return math.Max(MeterFloorDB, lufs(e)), len(idx) == steps
}

// spectrumOf calcule le spectre des dernières trames (ring : plus ancienne d'abord, ou nil s'il n'y en a pas). Le résultat
// est gardé tant qu'aucune trame n'arrive : plusieurs lecteurs d'un même instant ne refont pas le calcul.
func (m *Meter) spectrumOf(frames int64, ring []float32) []float64 {
	sp := &m.spectrum
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.cacheKey == frames && sp.cache != nil {
		return append([]float64(nil), sp.cache...)
	}
	out := make([]float64, meterBands)
	for i := range out {
		out[i] = MeterFloorDB
	}
	if ring != nil {
		for i := range sp.re {
			sp.re[i], sp.im[i] = float64(ring[i])*sp.window[i], 0
		}
		sp.fft.transform(sp.re, sp.im)
		// un sinus d'amplitude A donne |X| = A·N/4 avec une fenêtre de Hann (gain cohérent 1/2) : A = 4|X|/N
		scale := 4.0 / meterFFTSize
		for b := range out {
			peak := 0.0
			for k := sp.loBin[b]; k <= sp.hiBin[b]; k++ {
				peak = math.Max(peak, math.Hypot(sp.re[k], sp.im[k]))
			}
			if a := peak * scale; a > 1e-6 {
				out[b] = math.Max(MeterFloorDB, 20*math.Log10(a))
			}
		}
	}
	sp.cacheKey, sp.cache = frames, append([]float64(nil), out...)
	return out
}
