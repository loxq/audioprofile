package audioprofile

import (
	"io"
	"math"
)

// ExciterSettings règle l'exciter (enrichisseur d'harmoniques).
type ExciterSettings struct {
	FreqHz  float64 // fréquence de séparation : seul ce qui est au-dessus est enrichi ; <= 0 → 3000
	Amount  float64 // part d'harmoniques ajoutée, de 0 (sans effet) à 1 ; voir la description
	DriveDB float64 // gain poussé dans la saturation, de 3 à 24 dB ; <= 0 → 20
	Warmth  float64 // asymétrie de la saturation, de 0 (harmoniques impaires seules) à 1 (harmoniques paires en plus)
}

const (
	defaultExciterHz    = 3000.0
	defaultExciterDrive = 20.0
	minExciterDrive     = 3.0
	maxExciterDrive     = 24.0
	maxExciterBias      = 0.5 // asymétrie maximale (Warmth = 1)
)

// Exciter renvoie un flux PCM (s16le, entrelacé) dont les aigus sont enrichis d'harmoniques : de la
// brillance et de la présence, qu'une simple égalisation (qui ne fait que relever ce qui existe) ne donne pas.
//
// Le signal passe par un filtre passe-haut de Linkwitz-Riley d'ordre 4 à FreqHz, puis par la saturation de type tube de la
// bibliothèque (courbe tanh, suréchantillonnage ×4 : peu de repliement de spectre). On en retire la même chaîne de
// suréchantillonnage sans saturation, qui a exactement le même retard : il ne reste que ce que la saturation a
// **ajouté** (les harmoniques, et la petite compression de la fondamentale). Un second passe-haut retire ce qui tombe sous
// FreqHz, et le résultat, multiplié par Amount, est ajouté au signal d'origine, qui n'est jamais retardé ni modifié :
//
//	sortie = entrée + Amount × passe-haut(saturation(passe-haut(entrée)) − même chaîne sans saturation)
//
// L'alignement des retards est essentiel : ajouter directement la voie saturée (retardée de quelques échantillons
// par les filtres de suréchantillonnage) au signal sec ferait un filtre en peigne, avec un creux de 13 dB à 10 kHz.
//
// La saturation est calibrée pour rendre un signal faible à l'identique : **les harmoniques n'apparaissent que si les
// aigus sont assez forts pour la faire travailler** (le drive est absolu, en dBFS) ; sur un signal faible, l'exciter ne
// fait presque rien. Augmentez DriveDB pour en obtenir sur des aigus plus discrets. Warmth > 0 rend la saturation
// asymétrique, ce qui ajoute les harmoniques paires (octave, chaleur) aux impaires (tierce, mordant). Comme la
// fondamentale est un peu comprimée par la saturation, son niveau bouge de ±1,6 dB au plus à Amount = 1 sur un aigu à −20 dBFS
// (jusqu'à −4,6 dB sur un aigu très fort, à −14 dBFS : saturation profonde) ; il ne bouge pas à petit signal.
//
// Les canaux sont traités séparément. Comme les harmoniques ajoutées peuvent dépasser la pleine
// échelle, un plafond doux (genou à 0,8) arrondit les pics. Les valeurs sont ramenées dans leurs bornes
// (NaN compte pour 0). Si Amount est nul, ou si sampleRate <= 0 ou FreqHz >= Nyquist, le flux est renvoyé tel quel.
func Exciter(src io.Reader, sampleRate, channels int, s ExciterSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage de l'exciter (nil s'il est sans effet).
func (s ExciterSettings) stage(fs float64, channels int) frameStage {
	clamp := func(v, lo, hi float64) float64 {
		if math.IsNaN(v) {
			return 0
		}
		return math.Max(lo, math.Min(hi, v))
	}
	amount := clamp(s.Amount, 0, 1)
	f := s.FreqHz
	if f <= 0 {
		f = defaultExciterHz
	}
	if amount == 0 || fs <= 0 || channels < 1 || math.IsNaN(f) || f >= fs/2 {
		return nil
	}
	drive := s.DriveDB
	if !(drive > 0) {
		drive = defaultExciterDrive
	}
	drive = clamp(drive, minExciterDrive, maxExciterDrive)
	warmth := clamp(s.Warmth, 0, 1)

	e := &exciter{amount: amount, chans: make([]exciterChannel, channels)}
	sats := newSaturators(fs, channels, saturation{driveDB: drive, bias: warmth * maxExciterBias, stages: 1})
	// gain des petits signaux de la saturation : ce qu'il faut diviser pour qu'un signal faible
	// ressorte à l'identique, et que seuls les signaux forts, comprimés, produisent des harmoniques
	const eps = 1e-4
	lin := sats[0].shape(eps) / eps
	e.invLin = 1 / lin
	for ch := range e.chans {
		e.chans[ch] = exciterChannel{pre: newLR4(highPass(fs, f, defaultQ)), post: newLR4(highPass(fs, f, defaultQ)), sat: sats[ch], lin: newLinearOversampler(fs)}
	}
	return e
}

type exciterChannel struct {
	pre, post lr4
	sat       *saturator
	lin       *linearOversampler
}

// linearOversampler est la chaîne de suréchantillonnage du saturateur sans la saturation : mêmes filtres,
// donc même retard et même réponse en fréquence. Soustraite à la sortie du saturateur, elle ne laisse que ce que
// la non-linéarité a ajouté.
type linearOversampler struct {
	up, down [4]biquad
}

func newLinearOversampler(fs float64) *linearOversampler {
	l := &linearOversampler{}
	for i, q := range butterworth8Q {
		c := lowPass(fs*oversample, oversampleHz*fs, q)
		l.up[i], l.down[i] = biquad{coef: c}, biquad{coef: c}
	}
	return l
}

// process prend un échantillon (échelle int16) et le rend retardé comme le fait le saturateur.
func (l *linearOversampler) process(y float64) float64 {
	x := y / 32768
	var o float64
	for k := 0; k < oversample; k++ {
		u := 0.0
		if k == 0 {
			u = x * oversample
		}
		for i := range l.up {
			u = l.up[i].process(u)
		}
		for i := range l.down {
			u = l.down[i].process(u)
		}
		o = u
	}
	return o * 32768
}

type exciter struct {
	amount, invLin float64
	chans          []exciterChannel
}

func (e *exciter) apply(frame []float64) {
	for ch, x := range frame {
		c := &e.chans[ch]
		hi := c.pre.process(x)
		added := c.sat.apply(hi)*e.invLin - c.lin.process(hi) // seulement ce que la saturation ajoute
		frame[ch] = x + e.amount*c.post.process(added)
	}
}
