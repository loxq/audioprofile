package audioprofile

import (
	"io"
	"math"
)

// CrossfeedSettings règle le crossfeed pour casque.
type CrossfeedSettings struct {
	Strength float64 // de 0 (sans effet) à 1 (graves presque ramenés au centre) ; 0,5 à 0,7 est un réglage courant
	CutoffHz float64 // fréquence en dessous de laquelle les canaux se mélangent ; <= 0 → 700
	DelayMs  float64 // retard du signal croisé, de 0 à 1 ms ; 0 par défaut (voir la description)
}

const (
	defaultCrossfeedHz = 700.0
	maxCrossfeedDelay  = 1.0 // ms
)

// Crossfeed renvoie un flux PCM (s16le, entrelacé) adapté à l'écoute au casque : il mélange un peu de chaque
// canal dans l'autre, comme le fait l'air entre deux enceintes, ce qui réduit la fatigue d'écoute d'un mixage
// stéréo qui isole trop les deux oreilles (une guitare à gauche, une basse à droite). C'est l'inverse de
// StereoWidth, qui élargit l'image pour les enceintes.
//
// Chaque canal reçoit une part du canal opposé filtré par un passe-bas du premier ordre à CutoffHz, et perd la
// part équivalente de lui-même :
//
//	G' = G + k × (PB(D) − PB(G))      D' = D + k × (PB(G) − PB(D))      avec k = Strength / 2
//
// En termes milieu / côté, le milieu (G + D) n'est jamais touché, donc le résultat reste compatible mono et un son
// au centre ressort à l'identique ; seul le côté (G − D) est réduit sous CutoffHz, d'un facteur voisin de
// (1 − Strength) à basse fréquence : Strength = 0,6 réduit le côté de 7,5 dB à 100 Hz, Strength = 1 de 17 dB (le déphasage du
// passe-bas empêche l'annulation exacte). Au-dessus de quelques fois CutoffHz, rien ne bouge.
//
// DelayMs retarde le signal croisé (0,25 ms environ imite la différence de temps entre les deux oreilles),
// ce qui donne plus de profondeur mais colore les sons du centre entre 300 Hz et 1 kHz (jusqu'à environ 2,4 dB à Strength = 0,6) : le
// milieu n'est alors plus rigoureusement préservé. Il vaut 0 par défaut.
//
// Seuls les flux stéréo (channels = 2) sont traités. Sans effet (flux identique) si Strength est nul ou NaN, si
// sampleRate <= 0 ou si CutoffHz est >= Nyquist. Les valeurs sont ramenées dans leurs bornes.
func Crossfeed(src io.Reader, sampleRate, channels int, s CrossfeedSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage de crossfeed (nil s'il est sans effet).
func (s CrossfeedSettings) stage(fs float64, channels int) frameStage {
	strength := s.Strength
	if math.IsNaN(strength) {
		strength = 0
	}
	strength = math.Max(0, math.Min(1, strength))
	f := s.CutoffHz
	if f <= 0 {
		f = defaultCrossfeedHz
	}
	if strength == 0 || channels != 2 || fs <= 0 || math.IsNaN(f) || f >= fs/2 {
		return nil
	}
	delay := s.DelayMs
	if math.IsNaN(delay) {
		delay = 0
	}
	d := int(math.Round(math.Max(0, math.Min(maxCrossfeedDelay, delay)) / 1000 * fs))
	c := &crossfeed{k: strength / 2, d: d}
	for ch := range c.lp {
		c.lp[ch] = biquad{coef: onePole(fs, f, false)}
		c.ring[ch] = make([]float64, d)
	}
	return c
}

type crossfeed struct {
	k    float64
	d    int // retard du signal croisé, en trames
	lp   [2]biquad
	ring [2][]float64 // retard du passe-bas de chaque canal
	pos  int
}

func (c *crossfeed) apply(frame []float64) {
	lpL, lpR := c.lp[0].process(frame[0]), c.lp[1].process(frame[1])
	crossL, crossR := lpL, lpR // ce que chaque canal envoie à l'autre
	if c.d > 0 {
		crossL, crossR = c.ring[0][c.pos], c.ring[1][c.pos]
		c.ring[0][c.pos], c.ring[1][c.pos] = lpL, lpR
		if c.pos++; c.pos == c.d {
			c.pos = 0
		}
	}
	frame[0] += c.k * (crossR - lpL)
	frame[1] += c.k * (crossL - lpR)
}
