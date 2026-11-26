package audioprofile

import (
	"io"
	"math"
)

// ChorusSettings règle le chorus.
type ChorusSettings struct {
	DelayMs float64 // retard moyen de chaque voix, de 1 à 50 ms ; <= 0 → 18
	DepthMs float64 // amplitude de la modulation du retard, en ms ; <= 0 → 3 (ramenée à DelayMs − 0,5 au plus)
	RateHz  float64 // vitesse de la modulation, de 0,05 à 10 Hz ; <= 0 → 0,8
	Voices  int     // nombre de voix, de 1 à 6 ; <= 0 → 3
	Mix     float64 // niveau des voix par rapport au sec, de 0 (sans effet) à 1
}

// FlangerSettings règle le flanger.
type FlangerSettings struct {
	DelayMs  float64 // retard moyen, de 0,3 à 10 ms ; <= 0 → 2
	DepthMs  float64 // amplitude de la modulation du retard, en ms ; <= 0 → 1,5 (ramenée à DelayMs − 0,2 au plus)
	RateHz   float64 // vitesse de la modulation, de 0,05 à 10 Hz ; <= 0 → 0,25
	Feedback float64 // rétroaction, de −0,95 à 0,95 : renforce la résonance ; son signe change le timbre
	Mix      float64 // niveau du signal retardé par rapport au sec, de 0 (sans effet) à 1
}

const (
	defaultChorusDelayMs = 18.0
	defaultChorusDepthMs = 3.0
	defaultChorusRateHz  = 0.8
	defaultChorusVoices  = 3
	maxChorusVoices      = 6
	defaultFlangerDelay  = 2.0
	defaultFlangerDepth  = 1.5
	defaultFlangerRate   = 0.25
	maxModRateHz         = 10.0
	minModRateHz         = 0.05
	stereoLFOOffset      = 0.25 // décalage de phase de la modulation entre deux canaux (un quart de tour)
)

// Chorus renvoie un flux PCM (s16le, entrelacé) épaissi par des copies légèrement retardées, dont le retard
// oscille lentement : l'effet d'un ensemble de plusieurs instruments, jamais tout à fait accordés.
//
// Chaque voix lit le signal à un retard de DelayMs ± DepthMs (sinusoïde de fréquence RateHz, décalée d'un
// tour divisé par le nombre de voix) ; la lecture interpole sur quatre points. Le retard qui varie fait
// varier la hauteur : l'écart de fréquence est de 2π × RateHz × DepthMs (par exemple ±1,5 % pour 0,8 Hz et 3 ms, les valeurs par défaut,
// soit environ 26 centièmes de ton : réduisez DepthMs si c'est trop).
// Les voix sont moyennées (en ajouter ne rend pas l'effet plus fort) puis ajoutées au sec :
// sortie = sec + Mix × moyenne des voix. En stéréo, la modulation du canal droit est décalée d'un quart de
// tour : l'effet élargit l'image. Le sec n'est ni retardé ni modifié.
//
// Les valeurs sont ramenées dans leurs bornes (NaN compte pour 0, donc pour la valeur par défaut). Un plafond
// doux (genou à 0,8) arrondit les pics. Sans effet (flux identique) si Mix est nul ou si sampleRate <= 0.
func Chorus(src io.Reader, sampleRate, channels int, s ChorusSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// Flanger renvoie un flux PCM (s16le, entrelacé) dont le son est mélangé à une copie très courte (0,3 à 10 ms)
// dont le retard oscille : un filtre en peigne qui balaie le spectre, le « jet d'avion » des années 1970.
//
// Le signal retardé est ajouté au sec (sortie = sec + Mix × retardé) et réinjecté avec Feedback. Un retard
// de τ crée des creux aux fréquences (k + 1/2)/τ ; à Mix = 1, sans rétroaction ni modulation, un son à 1/(2τ)
// est supprimé de plus de 30 dB. La rétroaction approfondit le peigne et ajoute de la résonance ; une
// rétroaction négative décale les creux et les pics. La modulation du canal droit est décalée d'un quart de tour.
//
// Les valeurs sont ramenées dans leurs bornes (NaN compte pour 0). Un plafond doux (genou à 0,8) arrondit les
// pics. Sans effet (flux identique) si Mix est nul ou si sampleRate <= 0.
func Flanger(src io.Reader, sampleRate, channels int, s FlangerSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

func clampOr(v, def, lo, hi float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return def
	}
	return math.Max(lo, math.Min(hi, v))
}

func unitMix(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	return math.Max(0, math.Min(1, v))
}

// stage construit l'étage du chorus (nil s'il est sans effet).
func (s ChorusSettings) stage(fs float64, channels int) frameStage {
	mix := unitMix(s.Mix)
	if mix == 0 || fs <= 0 || channels < 1 {
		return nil
	}
	delay := clampOr(s.DelayMs, defaultChorusDelayMs, 1, 50)
	depth := math.Min(clampOr(s.DepthMs, defaultChorusDepthMs, 0.01, 50), delay-0.5)
	voices := s.Voices
	if voices <= 0 {
		voices = defaultChorusVoices
	}
	if voices > maxChorusVoices {
		voices = maxChorusVoices
	}
	return newModulation(fs, channels, delay, depth, clampOr(s.RateHz, defaultChorusRateHz, minModRateHz, maxModRateHz), voices, 0, mix)
}

// stage construit l'étage du flanger (nil s'il est sans effet).
func (s FlangerSettings) stage(fs float64, channels int) frameStage {
	mix := unitMix(s.Mix)
	if mix == 0 || fs <= 0 || channels < 1 {
		return nil
	}
	delay := clampOr(s.DelayMs, defaultFlangerDelay, 0.3, 10)
	depth := math.Min(clampOr(s.DepthMs, defaultFlangerDepth, 0.01, 10), delay-0.2)
	fb := s.Feedback
	if math.IsNaN(fb) {
		fb = 0
	}
	fb = math.Max(-0.95, math.Min(0.95, fb))
	return newModulation(fs, channels, delay, depth, clampOr(s.RateHz, defaultFlangerRate, minModRateHz, maxModRateHz), 1, fb, mix)
}

// modulation est l'étage commun au chorus (plusieurs voix, sans rétroaction) et au flanger (une voix, avec rétroaction).
type modulation struct {
	mix, fb      float64
	delaySamples float64
	depthSamples float64
	voices       int
	lines        []*modDelay // une ligne à retard par canal
	lfo          [][]lfo     // lfo[canal][voix]
}

func newModulation(fs float64, channels int, delayMs, depthMs, rateHz float64, voices int, fb, mix float64) *modulation {
	m := &modulation{
		mix: mix, fb: fb, voices: voices,
		delaySamples: delayMs / 1000 * fs, depthSamples: depthMs / 1000 * fs,
	}
	// l'interpolation sur quatre points demande un retard d'au moins 2 échantillons, même à basse fréquence d'échantillonnage
	m.delaySamples = math.Max(m.delaySamples, 3)
	m.depthSamples = math.Min(m.depthSamples, m.delaySamples-2)
	for ch := 0; ch < channels; ch++ {
		m.lines = append(m.lines, newModDelay(int(math.Ceil(m.delaySamples+m.depthSamples))+4))
		var l []lfo
		for v := 0; v < voices; v++ {
			l = append(l, newLFO(rateHz, fs, float64(v)/float64(voices)+stereoLFOOffset*float64(ch)))
		}
		m.lfo = append(m.lfo, l)
	}
	return m
}

func (m *modulation) apply(frame []float64) {
	for ch := range frame {
		line := m.lines[ch]
		var wet float64
		for v := range m.lfo[ch] {
			d := m.delaySamples + m.depthSamples*m.lfo[ch][v].next()
			wet += line.read(d) // d échantillons avant celui qu'on s'apprête à écrire
		}
		wet /= float64(m.voices)
		line.push(flushTiny(frame[ch] + m.fb*wet))
		frame[ch] += m.mix * wet
	}
}
