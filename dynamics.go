package bassboost

import (
	"io"
	"math"
)

// CompressorSettings décrit un compresseur de dynamique.
type CompressorSettings struct {
	ThresholdDB float64 // seuil en dBFS (ex. -20) au-delà duquel le niveau est réduit
	Ratio       float64 // rapport de compression (ex. 3 = 3:1) ; <= 1 = pas de compression
	AttackMs    float64 // temps d'attaque en ms ; <= 0 → 10
	ReleaseMs   float64 // temps de relâchement en ms ; <= 0 → 100
	KneeDB      float64 // largeur du genou en dB (0 = coude franc, 6 = doux)
	MakeupDB    float64 // gain de compensation appliqué après la compression
}

// Compressor renvoie un flux PCM (s16le, entrelacé) dont la dynamique est
// compressée : les passages dont le niveau crête dépasse le seuil sont
// atténués selon le rapport Ratio, avec une attaque et un relâchement lissés.
//
// Le niveau est détecté sur le canal le plus fort de chaque trame et le même
// gain est appliqué à tous les canaux (détection « liée »), ce qui préserve
// l'image stéréo.
//
// Sans effet (flux renvoyé tel quel) si sampleRate <= 0, ou si Ratio <= 1
// et MakeupDB = 0.
func Compressor(src io.Reader, sampleRate, channels int, c CompressorSettings) io.Reader {
	return newEqualizerReader(src, sampleRate, channels, nil, &c, nil)
}

const (
	defaultAttackMs  = 10.0
	defaultReleaseMs = 100.0
	maxRatio         = 100.0
	maxMakeupDB      = 24.0
	silenceDB        = -120.0 // plancher du détecteur
)

// compressor : compresseur à action directe (feed-forward), gain calculé en dB
// avec genou doux puis lissé (attaque quand la réduction augmente, relâchement
// quand elle diminue).
type compressor struct {
	thresh, ratio, knee float64
	makeup              float64
	ca, cr              float64 // coefficients de lissage attaque / relâchement
	gr                  float64 // réduction de gain lissée (dB, <= 0)
}

// newCompressor renvoie nil si le compresseur est sans effet.
func newCompressor(fs float64, s *CompressorSettings) *compressor {
	if s == nil || fs <= 0 || math.IsNaN(s.ThresholdDB) || math.IsNaN(s.Ratio) || math.IsNaN(s.MakeupDB) {
		return nil
	}
	ratio := math.Max(1, math.Min(maxRatio, s.Ratio))
	makeup := math.Max(-maxMakeupDB, math.Min(maxMakeupDB, s.MakeupDB))
	if ratio == 1 && makeup == 0 {
		return nil
	}
	att, rel := s.AttackMs, s.ReleaseMs
	if att <= 0 {
		att = defaultAttackMs
	}
	if rel <= 0 {
		rel = defaultReleaseMs
	}
	coef := func(ms float64) float64 { return math.Exp(-1 / (ms / 1000 * fs)) }
	return &compressor{
		thresh: s.ThresholdDB,
		ratio:  ratio,
		knee:   math.Max(0, s.KneeDB),
		makeup: makeup,
		ca:     coef(att),
		cr:     coef(rel),
	}
}

// reduction renvoie la réduction de gain statique (dB, <= 0) pour un niveau lvl (dBFS).
func (c *compressor) reduction(lvl float64) float64 {
	over := lvl - c.thresh
	if 2*over < -c.knee {
		return 0
	}
	slope := 1/c.ratio - 1
	if c.knee > 0 && 2*math.Abs(over) <= c.knee {
		d := over + c.knee/2
		return slope * d * d / (2 * c.knee)
	}
	return slope * over // au-dessus du genou (ou coude franc : over >= 0)
}

// apply traite une trame (un échantillon par canal, échelle int16) sur place.
func (c *compressor) apply(frame []float64) {
	peak := 0.0
	for _, y := range frame {
		peak = math.Max(peak, math.Abs(y))
	}
	lvl := silenceDB
	if peak > 0 {
		lvl = math.Max(silenceDB, 20*math.Log10(peak/32768))
	}
	target := c.reduction(lvl)
	if target < c.gr {
		c.gr = c.ca*c.gr + (1-c.ca)*target // attaque : la réduction augmente
	} else {
		c.gr = c.cr*c.gr + (1-c.cr)*target // relâchement
	}
	g := math.Pow(10, (c.gr+c.makeup)/20)
	for i := range frame {
		frame[i] *= g
	}
}

// scaleCompression met un réglage de compresseur à l'échelle d'une intensité
// dans [0, 1] : le rapport passe de 1 (rien) à Ratio, la compensation suit.
func scaleCompression(c CompressorSettings, intensity float64) CompressorSettings {
	c.Ratio = 1 + (c.Ratio-1)*intensity
	c.MakeupDB *= intensity
	return c
}
