package audioprofile

import (
	"io"
	"math"
)

const (
	// MaxWidth est la largeur stéréo maximale acceptée par StereoWidth.
	MaxWidth = 3.0

	sideHighPassHz = 200.0 // sous cette fréquence, le côté n'est pas modifié : les basses restent au centre
)

// stereoSettings décrit l'élargisseur stéréo d'une chaîne.
type stereoSettings struct{ width float64 }

// StereoWidth renvoie un flux PCM (s16le, entrelacé) dont l'image stéréo est
// élargie ou resserrée par un traitement mid/side.
//
// width multiplie le signal « côté » (S = (G − D) / 2) au-dessus de 200 Hz :
// 1 = inchangé, au-delà de 1 = plus large (jusqu'à MaxWidth), en dessous de 1 =
// plus étroit (0 = mono au-dessus de 200 Hz). Le signal « milieu »
// (M = (G + D) / 2, ce qui est au centre : voix, grosse caisse, basse) n'est
// jamais touché, donc G + D est conservé : le résultat reste compatible mono.
// Le côté n'est pas modifié sous 200 Hz (il est seulement déphasé par le filtre
// de séparation, ce qui laisse le milieu et donc le mono intacts) : les basses
// restent au centre.
//
// Seuls les flux stéréo (channels = 2) sont traités ; les autres sont renvoyés
// tels quels, de même que width = 1 ou une valeur NaN.
//
// Élargir peut faire dépasser la pleine échelle à un signal déjà fort : les
// pics sont alors arrondis par le plafond doux (genou à 0,8), pas écrêtés net.
func StereoWidth(src io.Reader, sampleRate, channels int, width float64) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{stereo: &stereoSettings{width: width}})
}

// widener applique l'élargissement mid/side à une trame stéréo. Le côté est
// séparé à 200 Hz par un filtre de Linkwitz-Riley d'ordre 4 (deux biquads
// Butterworth en cascade de chaque côté) : S' = BF(S) + largeur × HF(S).
type widener struct {
	width  float64
	lo, hi [2]biquad
}

// newWidener renvoie nil si l'élargissement est sans effet.
func newWidener(fs float64, channels int, s *stereoSettings) *widener {
	if s == nil || channels != 2 || fs <= 0 || math.IsNaN(s.width) || fs/2 <= sideHighPassHz {
		return nil
	}
	width := math.Max(0, math.Min(MaxWidth, s.width))
	if width == 1 {
		return nil
	}
	w := &widener{width: width}
	for i := range w.lo {
		w.lo[i] = biquad{coef: lowPass(fs, sideHighPassHz, defaultQ)}
		w.hi[i] = biquad{coef: highPass(fs, sideHighPassHz, defaultQ)}
	}
	return w
}

// apply traite une trame stéréo (échelle int16) sur place.
func (w *widener) apply(frame []float64) {
	m := (frame[0] + frame[1]) / 2
	s := (frame[0] - frame[1]) / 2
	lo, hi := s, s
	for i := range w.lo {
		lo = w.lo[i].process(lo)
		hi = w.hi[i].process(hi)
	}
	s = lo + w.width*hi
	frame[0], frame[1] = m+s, m-s
}
