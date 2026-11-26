package audioprofile

import (
	"io"
	"math"
)

// TransientSettings règle le renforçateur de transitoires.
type TransientSettings struct {
	// Attack agit sur le début des sons (l'impact d'une grosse caisse, d'une caisse claire, d'une
	// attaque de médiator), de -1 (attaque aplatie) à +1 (attaque marquée). 0 = inchangé.
	Attack float64
	// Sustain agit sur la queue des sons (la résonance, la tenue, l'ambiance qui suit l'impact),
	// de -1 (queue raccourcie, son plus sec) à +1 (queue allongée, son plus ample). 0 = inchangé.
	Sustain float64
}

const (
	transientFastAttackMs  = 1.0   // enveloppe rapide : suit le signal de près
	transientFastReleaseMs = 50.0  //   … et retombe en 50 ms
	transientSlowAttackMs  = 25.0  // enveloppe lente : n'a pas encore monté au début d'un son
	transientSlowReleaseMs = 350.0 //   … et retombe bien plus lentement que le signal
	transientGainSmoothMs  = 0.5   // lissage du gain : pas de craquement
	transientDeadbandDB    = 2.0   // écart minimal entre les enveloppes pour agir
	transientMaxDiffDB     = 18.0  // écart pris en compte au plus
	transientMaxGainDB     = 12.0  // gain ou atténuation maximal
	transientFloorDBFS     = -70.0 // sous ce niveau (enveloppe lente), rien n'est touché : pas de pompage du bruit de fond
)

// TransientShaper renvoie un flux PCM (s16le, entrelacé) dont les transitoires sont renforcés ou
// adoucis, sans seuil : l'effet suit la forme des sons, pas leur niveau.
//
// Deux enveloppes du niveau crête sont comparées. L'enveloppe rapide monte en 1 ms et retombe en
// 80 ms ; la lente monte en 25 ms et retombe en 350 ms. Au début d'un son, la rapide devance la
// lente : c'est l'attaque. Pendant la queue d'un son qui s'éteint, la lente reste au-dessus de la
// rapide : c'est la tenue. Le gain en dB est proportionnel à cet écart :
//
//	gain = Attack × écart(rapide au-dessus de lente) + Sustain × écart(lente au-dessus de rapide)
//
// Attack = +1 double donc, en dB, l'avance de l'attaque sur le corps du son ; Attack = −1 la
// supprime. Comme on compare deux enveloppes d'un même signal, le résultat ne dépend pas du
// niveau d'entrée : le même coup de caisse claire est traité de la même façon à −30 et à −12 dBFS.
//
// Un son stable (un sinus, une note tenue) n'est pas touché : sous 2 dB d'écart entre les
// enveloppes, aucun gain n'est appliqué (« zone morte », qui absorbe l'ondulation de l'enveloppe
// des très basses fréquences). Le gain est limité à ±12 dB et lissé sur 2 ms. Il n'agit pas sous
// −70 dBFS (enveloppe lente), pour ne pas faire pomper le bruit de fond.
//
// Le niveau est détecté sur le canal le plus fort et le même gain s'applique à tous les canaux
// (détection liée) : l'image stéréo ne bouge pas. Comme l'attaque est renforcée sans compensation,
// les pics peuvent grimper : un plafond doux (genou à 0,8) arrondit ceux qui dépasseraient la
// pleine échelle. Sur un master très fort, dont les pics sont déjà à 0 dBFS, il n'y a donc presque
// rien à gagner sur l'attaque.
//
// Les valeurs de Attack et Sustain sont ramenées dans [-1, 1] (NaN compte pour 0). Si les deux
// sont nulles, ou si sampleRate <= 0, le flux est renvoyé tel quel.
func TransientShaper(src io.Reader, sampleRate, channels int, s TransientSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage du renforçateur de transitoires (nil s'il est sans effet).
func (s TransientSettings) stage(fs float64, channels int) frameStage {
	clamp := func(v float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(-1, math.Min(1, v))
	}
	att, sus := clamp(s.Attack), clamp(s.Sustain)
	if fs <= 0 || (att == 0 && sus == 0) {
		return nil
	}
	coef := func(ms float64) float64 { return math.Exp(-1 / (ms / 1000 * fs)) }
	return &transient{
		attack: att, sustain: sus,
		fastA: coef(transientFastAttackMs), fastR: coef(transientFastReleaseMs),
		slowA: coef(transientSlowAttackMs), slowR: coef(transientSlowReleaseMs),
		smooth: coef(transientGainSmoothMs),
		floor:  32768 * math.Pow(10, transientFloorDBFS/20),
	}
}

type transient struct {
	attack, sustain            float64
	fastA, fastR, slowA, slowR float64
	smooth                     float64
	floor                      float64 // amplitude sous laquelle l'enveloppe lente n'autorise aucun gain
	fast, slow                 float64 // enveloppes (échelle int16)
	gainDB                     float64 // gain lissé
}

// follow fait suivre à env le niveau x, avec le coefficient d'attaque quand x monte et de
// relâchement quand il baisse.
func follow(env, x, a, r float64) float64 {
	if x > env {
		return a*env + (1-a)*x
	}
	return r*env + (1-r)*x
}

func (t *transient) apply(frame []float64) {
	peak := 0.0
	for _, y := range frame {
		peak = math.Max(peak, math.Abs(y))
	}
	t.fast = follow(t.fast, peak, t.fastA, t.fastR)
	t.slow = follow(t.slow, peak, t.slowA, t.slowR)

	target := 0.0
	if t.slow > t.floor && t.fast > 0 {
		diff := 20 * math.Log10(t.fast/t.slow) // > 0 : début d'un son ; < 0 : queue d'un son
		switch {
		case diff > transientDeadbandDB:
			target = t.attack * math.Min(diff-transientDeadbandDB, transientMaxDiffDB)
		case diff < -transientDeadbandDB:
			target = t.sustain * math.Min(-diff-transientDeadbandDB, transientMaxDiffDB)
		}
		target = math.Max(-transientMaxGainDB, math.Min(transientMaxGainDB, target))
	}
	t.gainDB = t.smooth*t.gainDB + (1-t.smooth)*target
	if t.gainDB == 0 {
		return
	}
	g := math.Pow(10, t.gainDB/20)
	for i := range frame {
		frame[i] *= g
	}
}
