package audioprofile

import (
	"io"
	"math"
)

// GateSettings règle le gate (expandeur descendant).
type GateSettings struct {
	ThresholdDB float64 // seuil en dBFS : en dessous, le signal est réduit
	Ratio       float64 // rapport d'expansion : 2 = expandeur doux, 100 = gate franc ; <= 1 : sans effet
	RangeDB     float64 // réduction maximale en dB (positive) ; <= 0 → 40
	AttackMs    float64 // temps d'ouverture quand le niveau remonte, en ms ; <= 0 → 2
	ReleaseMs   float64 // temps de fermeture quand le niveau retombe, en ms ; <= 0 → 100
}

const (
	defaultGateRangeDB   = 40.0
	defaultGateAttackMs  = 2.0
	defaultGateReleaseMs = 100.0
	maxGateRangeDB       = 90.0
	gateKneeDB           = 6.0
	gateDetectAttackMs   = 0.2  // détecteur : suit le niveau de près quand il monte
	gateDetectReleaseMs  = 30.0 // … et retombe en 30 ms, pour ne pas hacher les basses fréquences
)

// Gate renvoie un flux PCM (s16le, entrelacé) dont les passages faibles sont réduits (expandeur
// descendant, ou gate avec un rapport élevé) : le bruit de fond entre les phrases, le souffle
// entre les notes. C'est l'inverse d'un compresseur : au-dessus du seuil rien ne change, en dessous
// le niveau chute Ratio fois plus vite qu'il ne baisse à l'entrée. Avec Ratio = 2, un passage 10 dB
// sous le seuil sort 20 dB sous le seuil (réduit de 10 dB) ; avec un rapport de 100, il est
// réduit de RangeDB (le gate).
//
// Le niveau est détecté sur le canal le plus fort (le même gain s'applique à tous les canaux : l'image
// stéréo ne bouge pas), avec un détecteur de crête qui monte en 0,2 ms et retombe en 30 ms. La courbe a un genou
// de 6 dB. La réduction, plafonnée à RangeDB, est lissée : l'ouverture suit AttackMs, la fermeture ReleaseMs.
//
// Il n'y a pas d'anticipation : le tout début d'un son qui sort du silence peut être atténué pendant AttackMs. Un
// signal stable au-dessus du seuil n'est pas touché, et le silence reste exactement du silence.
//
// Sans effet (flux identique) si Ratio <= 1 ou NaN, ou si sampleRate <= 0. RangeDB, AttackMs et ReleaseMs
// sont ramenés dans leurs bornes ; un seuil NaN rend le flux tel quel.
func Gate(src io.Reader, sampleRate, channels int, s GateSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage du gate (nil s'il est sans effet).
func (s GateSettings) stage(fs float64, channels int) frameStage {
	if !(s.Ratio > 1) || math.IsNaN(s.ThresholdDB) || math.IsNaN(s.RangeDB) || math.IsNaN(s.AttackMs) || math.IsNaN(s.ReleaseMs) || fs <= 0 || channels < 1 {
		return nil
	}
	rng := s.RangeDB
	if rng <= 0 {
		rng = defaultGateRangeDB
	}
	att, rel := s.AttackMs, s.ReleaseMs
	if att <= 0 {
		att = defaultGateAttackMs
	}
	if rel <= 0 {
		rel = defaultGateReleaseMs
	}
	coef := func(ms float64) float64 { return math.Exp(-1 / (ms / 1000 * fs)) }
	return &gate{
		thresh: s.ThresholdDB, ratio: math.Min(maxRatio, s.Ratio), rng: math.Min(maxGateRangeDB, rng),
		ca: coef(att), cr: coef(rel), da: coef(gateDetectAttackMs), dr: coef(gateDetectReleaseMs),
	}
}

type gate struct {
	thresh, ratio, rng float64
	ca, cr, da, dr     float64 // lissage du gain (ouverture, fermeture) et du détecteur (montée, descente)
	env                float64 // niveau détecté (échelle int16)
	gr                 float64 // réduction lissée (dB, <= 0)
}

// reduction renvoie la réduction statique (dB, <= 0) pour un niveau lvl (dBFS).
func (g *gate) reduction(lvl float64) float64 {
	over := lvl - g.thresh
	slope := g.ratio - 1
	var r float64
	switch {
	case over >= gateKneeDB/2:
		return 0
	case over <= -gateKneeDB/2:
		r = slope * over
	default: // genou : raccord quadratique avec la droite
		d := over - gateKneeDB/2
		r = -slope * d * d / (2 * gateKneeDB)
	}
	return math.Max(-g.rng, r)
}

func (g *gate) apply(frame []float64) {
	peak := 0.0
	for _, y := range frame {
		peak = math.Max(peak, math.Abs(y))
	}
	if peak > g.env {
		g.env = g.da*g.env + (1-g.da)*peak
	} else {
		g.env = g.dr*g.env + (1-g.dr)*peak
	}
	lvl := silenceDB
	if g.env > 0 {
		lvl = math.Max(silenceDB, 20*math.Log10(g.env/32768))
	}
	target := g.reduction(lvl)
	if target > g.gr { // le niveau remonte : le gate s'ouvre
		g.gr = g.ca*g.gr + (1-g.ca)*target
	} else { // le niveau retombe : le gate se ferme
		g.gr = g.cr*g.gr + (1-g.cr)*target
	}
	if g.gr > -1e-9 {
		g.gr = 0
		return
	}
	k := math.Pow(10, g.gr/20)
	for i := range frame {
		frame[i] *= k
	}
}
