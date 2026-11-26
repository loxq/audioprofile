package audioprofile

import (
	"io"
	"math"
)

// DelaySettings règle le delay (écho).
type DelaySettings struct {
	TimeMs    float64 // durée entre deux échos, de 1 à 2000 ms
	Feedback  float64 // part réinjectée à chaque écho, de 0 (un seul écho) à 0,95 ; l'écho k a l'amplitude Feedback^(k−1)
	Mix       float64 // niveau des échos par rapport au signal sec, de 0 (sans effet) à 1
	DampingHz float64 // passe-bas dans la boucle : chaque écho est plus sombre que le précédent ; <= 0 : aucun
	PingPong  bool    // stéréo : les échos rebondissent entre gauche (premier écho) et droite
}

const (
	maxDelayMs       = 2000.0
	maxDelayFeedback = 0.95
)

// Delay renvoie un flux PCM (s16le, entrelacé) auquel des échos sont ajoutés : sortie = sec + Mix × échos.
// Le sec n'est ni retardé ni atténué.
//
// Chaque canal a sa propre ligne à retard de TimeMs, avec une rétroaction de Feedback : la voix de
// gauche garde ses échos à gauche, celle de droite à droite. L'écho k arrive à k × TimeMs avec une
// amplitude de Mix × Feedback^(k−1) (la durée de la queue est de l'ordre de TimeMs × 6,9 / −ln(Feedback)
// pour −60 dB). DampingHz place un passe-bas du premier ordre dans la boucle : les échos successifs
// perdent leurs aigus, comme dans un écho à bande.
//
// PingPong (stéréo seulement) fait rebondir les échos d'un côté à l'autre : l'entrée est la moyenne
// des deux canaux, le premier écho sort à gauche, le deuxième à droite, le troisième à gauche, etc. L'image
// stéréo d'origine n'est conservée que par le signal sec. Pour un autre nombre de canaux, PingPong est ignoré.
//
// **La queue est coupée à la fin du flux** : la sortie a exactement la longueur de l'entrée. Pour laisser les
// derniers échos mourir, ajoutez du silence à la fin de l'entrée.
//
// Un plafond doux (genou à 0,8) arrondit les pics qui dépasseraient la pleine échelle. Les valeurs sont
// ramenées dans leurs bornes (NaN compte pour 0). Si Mix est nul, ou si sampleRate <= 0, le flux est
// renvoyé tel quel.
func Delay(src io.Reader, sampleRate, channels int, s DelaySettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage du delay (nil s'il est sans effet).
func (s DelaySettings) stage(fs float64, channels int) frameStage {
	clamp := func(v, lo, hi float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(lo, math.Min(hi, v))
	}
	mix := clamp(s.Mix, 0, 1)
	if mix == 0 || fs <= 0 || channels < 1 {
		return nil
	}
	n := int(math.Round(clamp(s.TimeMs, 1, maxDelayMs) / 1000 * fs))
	if n < 1 {
		n = 1
	}
	d := &delay{mix: mix, fb: clamp(s.Feedback, 0, maxDelayFeedback), pingpong: s.PingPong && channels == 2}
	for ch := 0; ch < channels; ch++ {
		d.buf = append(d.buf, make([]float64, n))
	}
	if s.DampingHz > 0 && !math.IsNaN(s.DampingHz) && s.DampingHz < fs/2 {
		d.damp = make([]biquad, channels)
		for ch := range d.damp {
			d.damp[ch] = biquad{coef: onePole(fs, s.DampingHz, false)}
		}
	}
	return d
}

type delay struct {
	mix, fb  float64
	pingpong bool
	buf      [][]float64 // une ligne à retard par canal
	damp     []biquad    // passe-bas de la boucle, par canal (nil : aucun)
	pos      int
}

func (d *delay) apply(frame []float64) {
	n := len(d.buf[0])
	if d.pingpong {
		out0, out1 := d.buf[0][d.pos], d.buf[1][d.pos]
		fb0, fb1 := out1, out0 // les échos changent de côté
		if d.damp != nil {
			fb0, fb1 = d.damp[0].process(fb0), d.damp[1].process(fb1)
		}
		in := (frame[0] + frame[1]) / 2
		d.buf[0][d.pos] = flushTiny(in + d.fb*fb0)
		d.buf[1][d.pos] = flushTiny(d.fb * fb1)
		frame[0] += d.mix * out0
		frame[1] += d.mix * out1
	} else {
		for ch := range frame {
			out := d.buf[ch][d.pos]
			fb := out
			if d.damp != nil {
				fb = d.damp[ch].process(out)
			}
			d.buf[ch][d.pos] = flushTiny(frame[ch] + d.fb*fb)
			frame[ch] += d.mix * out
		}
	}
	if d.pos++; d.pos == n {
		d.pos = 0
	}
}
