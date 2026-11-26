package audioprofile

import (
	"io"
	"math"
)

// ReverbSettings règle la réverbération.
type ReverbSettings struct {
	RoomSize   float64 // taille de la pièce, de 0 (petite, queue courte) à 1 (grande, queue longue)
	Damping    float64 // absorption des aigus dans la queue, de 0 (pièce claire) à 1 (pièce feutrée)
	Width      float64 // largeur stéréo de la réverbération, de 0 (mono) à 1 (décorrélée)
	PreDelayMs float64 // retard avant la réverbération, de 0 à 200 ms
	Mix        float64 // part de réverbération, de 0 (son sec, sans effet) à 1 (réverbération seule)
}

// Réglages de l'algorithme de Schroeder-Moorer popularisé par « Freeverb » (Jezar at Dreampoint,
// domaine public) : huit filtres en peigne avec amortissement en parallèle, puis quatre filtres
// passe-tout en série. Les longueurs ont été choisies pour 44,1 kHz et sont mises à l'échelle
// des autres fréquences d'échantillonnage.
var (
	reverbCombLengths    = [8]int{1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617}
	reverbAllpassLengths = [4]int{556, 441, 341, 225}
)

const (
	reverbStereoSpread = 23    // décalage des longueurs entre canaux (décorrélation)
	reverbInputGain    = 0.015 // gain d'entrée de Freeverb
	reverbBaseFeedback = 0.7   // rétroaction à la taille 0
	reverbFeedbackSpan = 0.28  // … à la taille 1 : 0,98
	reverbMaxDamp      = 0.4   // amortissement maximal (Freeverb)
	reverbAllpassGain  = 0.5   // rétroaction des passe-tout
	reverbMaxPreDelay  = 200.0 // ms
	reverbFlush        = 1e-15 // en dessous, un état est ramené à 0 : pas de nombres dénormalisés
	reverbNormMaxSecs  = 30    // durée maximale de la réponse simulée pour calibrer le niveau
)

// Reverb renvoie un flux PCM (s16le, entrelacé) auquel une réverbération est ajoutée.
//
// Chaque canal passe par ses propres huit filtres en peigne et quatre passe-tout, d'une longueur
// légèrement différente d'un canal à l'autre (23 échantillons d'écart à 44,1 kHz) ; l'entrée est la
// moyenne des canaux, retardée de PreDelayMs. Le niveau de la partie réverbérée est calibré au
// démarrage sur l'énergie de la réponse impulsionnelle : un bruit blanc (identique sur les canaux)
// ressort de la réverbération avec la même puissance qu'à l'entrée, quels que soient RoomSize, Damping
// et Width. Mix mélange donc
// le sec et le mouillé à puissance constante (loi en cosinus et sinus) : sortie = cos(Mix·π/2) × sec +
// sin(Mix·π/2) × réverbération. Comme les deux sont peu corrélés, le volume reste à peu près celui du son sec
// (un fondu linéaire le ferait baisser de 2 dB à Mix = 0,25). Mix = 0,25 donne 15 % de la puissance en réverbération,
// Mix = 0,5 en donne la moitié.
//
// Width mélange les deux canaux de la réverbération (stéréo seulement) : 1 la laisse décorrélée,
// 0 la rend identique à gauche et à droite. Pour un seul canal, ou plus de deux, Width est ignoré.
//
// **La queue est coupée à la fin du flux** : la sortie a exactement la longueur de l'entrée, comme
// tous les effets de la bibliothèque, donc la réverbération d'un morceau s'arrête net avec le
// dernier échantillon. Pour la laisser s'éteindre, ajoutez du silence à la fin de l'entrée.
//
// Les valeurs sont ramenées dans leurs bornes (NaN compte pour 0). Si Mix est nul, ou si
// sampleRate <= 0, le flux est renvoyé tel quel. Un plafond doux (genou à 0,8) arrondit les pics
// qui dépasseraient la pleine échelle.
func Reverb(src io.Reader, sampleRate, channels int, s ReverbSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

type reverbComb struct {
	buf        []float64
	idx        int
	store      float64
	fb, d1, d2 float64
}

func (c *reverbComb) process(x float64) float64 {
	out := c.buf[c.idx]
	c.store = flushTiny(out*c.d2 + c.store*c.d1)
	c.buf[c.idx] = flushTiny(x + c.store*c.fb)
	if c.idx++; c.idx == len(c.buf) {
		c.idx = 0
	}
	return out
}

type reverbAllpass struct {
	buf []float64
	idx int
}

func (a *reverbAllpass) process(x float64) float64 {
	b := a.buf[a.idx]
	out := b - x
	a.buf[a.idx] = flushTiny(x + b*reverbAllpassGain)
	if a.idx++; a.idx == len(a.buf) {
		a.idx = 0
	}
	return out
}

func flushTiny(x float64) float64 {
	if x < reverbFlush && x > -reverbFlush {
		return 0
	}
	return x
}

// reverbBank est la réverbération d'un canal.
type reverbBank struct {
	combs [8]reverbComb
	aps   [4]reverbAllpass
}

func newReverbBank(fs float64, spread int, feedback, damp float64) *reverbBank {
	scale := fs / 44100
	length := func(n int) int { return int(math.Round(float64(n+spread) * scale)) }
	b := &reverbBank{}
	for i, n := range reverbCombLengths {
		b.combs[i] = reverbComb{buf: make([]float64, length(n)), fb: feedback, d1: damp, d2: 1 - damp}
	}
	for i, n := range reverbAllpassLengths {
		b.aps[i] = reverbAllpass{buf: make([]float64, length(n))}
	}
	return b
}

func (b *reverbBank) process(x float64) float64 {
	var sum float64
	for i := range b.combs {
		sum += b.combs[i].process(x)
	}
	for i := range b.aps {
		sum = b.aps[i].process(sum)
	}
	return sum
}

type reverb struct {
	dry, wetMix float64 // gains du sec et du mouillé (loi de puissance constante), calibrage du mouillé compris
	w1, w2      float64 // mélange stéréo de la partie réverbérée
	stereo      bool
	pre         []float64
	preIdx      int
	banks       []*reverbBank
	wet         []float64
}

// stage construit l'étage de réverbération (nil s'il est sans effet).
func (s ReverbSettings) stage(fs float64, channels int) frameStage {
	clamp := func(v, lo, hi float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(lo, math.Min(hi, v))
	}
	mix := clamp(s.Mix, 0, 1)
	if fs <= 0 || channels < 1 || mix == 0 {
		return nil
	}
	room, damp := clamp(s.RoomSize, 0, 1), clamp(s.Damping, 0, 1)
	width, pre := clamp(s.Width, 0, 1), clamp(s.PreDelayMs, 0, reverbMaxPreDelay)
	feedback := reverbBaseFeedback + reverbFeedbackSpan*room
	damping := reverbMaxDamp * damp

	r := &reverb{dry: math.Cos(mix * math.Pi / 2), stereo: channels == 2, wet: make([]float64, channels)}
	if mix == 1 {
		r.dry = 0 // cos(π/2) vaut 6e-17 : pas de reste de son sec dans la réverbération seule
	}
	if n := int(math.Round(pre / 1000 * fs)); n > 0 {
		r.pre = make([]float64, n)
	}
	for ch := 0; ch < channels; ch++ {
		r.banks = append(r.banks, newReverbBank(fs, reverbStereoSpread*ch, feedback, damping))
	}
	r.w1, r.w2 = 1, 0
	if r.stereo {
		r.w1, r.w2 = width/2+0.5, (1-width)/2
	}
	r.wetMix = math.Sin(mix*math.Pi/2) * reverbWetGain(fs, channels, feedback, damping) / math.Hypot(r.w1, r.w2)
	return r
}

// reverbWetGain calibre le niveau de la partie réverbérée : l'inverse de la racine de l'énergie de la
// réponse impulsionnelle d'un canal (moyenne sur les canaux, simulée jusqu'à extinction), de sorte
// qu'un bruit blanc garde sa puissance.
func reverbWetGain(fs float64, channels int, feedback, damp float64) float64 {
	banks := channels
	if banks > 2 {
		banks = 2 // les canaux au-delà ont presque la même énergie
	}
	var total float64
	for b := 0; b < banks; b++ {
		bank := newReverbBank(fs, reverbStereoSpread*b, feedback, damp)
		var energy, window float64
		maxN := int(reverbNormMaxSecs * fs)
		for n := 0; n < maxN; n++ {
			in := 0.0
			if n == 0 {
				in = reverbInputGain
			}
			y := bank.process(in)
			energy += y * y
			window += y * y
			if (n+1)%4096 == 0 {
				if window < 1e-12*energy {
					break // le reste ne compte plus
				}
				window = 0
			}
		}
		total += energy
	}
	return 1 / math.Sqrt(total/float64(banks))
}

func (r *reverb) apply(frame []float64) {
	var in float64
	for _, y := range frame {
		in += y
	}
	in = in / float64(len(frame)) * reverbInputGain
	if len(r.pre) > 0 {
		delayed := r.pre[r.preIdx]
		r.pre[r.preIdx] = in
		if r.preIdx++; r.preIdx == len(r.pre) {
			r.preIdx = 0
		}
		in = delayed
	}
	for ch, b := range r.banks {
		r.wet[ch] = b.process(in)
	}
	for ch := range frame {
		w := r.wet[ch]
		if r.stereo {
			w = r.wet[ch]*r.w1 + r.wet[1-ch]*r.w2
		}
		frame[ch] = frame[ch]*r.dry + r.wetMix*w
	}
}
