package audioprofile

import "math"

const (
	maxDriveDB  = 24.0 // drive maximal accepté
	maxOutputDB = 24.0 // |gain de sortie| maximal accepté
	maxBias     = 0.5  // |asymétrie| maximale acceptée

	oversample   = 4    // facteur de suréchantillonnage de l'étage non linéaire
	oversampleHz = 0.43 // coupure des filtres de suréchantillonnage, en fraction de la fréquence d'échantillonnage
	dcCutoffHz   = 7.0  // coupure du filtre anti-continu (quand l'étage est asymétrique)
	maxStages    = 4    // nombre maximal d'étages en cascade
	couplingHz   = 30.0 // coupure du filtre de couplage entre deux étages
	ceilingKnee  = 0.9  // au-delà (en valeur normalisée), le plafond doux de la saturation arrondit les pics
)

// ceilingTop est l'asymptote des plafonds doux : deux pas de quantification sous
// la pleine échelle, pour que la sortie arrondie en int16 ne touche jamais ±32767.
const ceilingTop = 1 - 2.0/32768

// softCeilingAt arrondit les pics au-delà de knee et les fait tendre vers
// ceilingTop sans l'atteindre : continue, de pente 1 au genou.
func softCeilingAt(x, knee float64) float64 {
	a := math.Abs(x)
	if a <= knee {
		return x
	}
	span := ceilingTop - knee
	return math.Copysign(knee+span*math.Tanh((a-knee)/span), x)
}

// softCeiling est softCeilingAt au genou ceilingKnee.
func softCeiling(x float64) float64 { return softCeilingAt(x, ceilingKnee) }

// butterworth8Q donne les facteurs Q des 4 sections d'un filtre de Butterworth d'ordre 8
// (Q = 1 / (2·sin((2k−1)π/16))).
var butterworth8Q = [4]float64{0.5098, 0.6013, 0.9000, 2.5629}

// saturation décrit l'étage de saturation d'un profil. Un champ nul = sans effet.
type saturation struct {
	driveDB  float64 // gain poussé dans la saturation (plus c'est haut, plus le grain est marqué)
	bias     float64 // asymétrie : 0 = symétrique (harmoniques impaires), > 0 ajoute des harmoniques paires
	outputDB float64 // gain de sortie final, pour régler le niveau après saturation
	stages   int     // étages en cascade (0 ou 1 = un seul) ; le drive est réparti entre eux
}

// highPass1 : filtre passe-haut du premier ordre (condensateur de couplage).
type highPass1 struct{ r, x, y float64 }

func (h *highPass1) process(x float64) float64 {
	y := x - h.x + h.r*h.y
	h.x, h.y = x, y
	return y
}

// saturator applique une saturation douce de type tube, puis un gain de sortie.
// Il garde un état (filtres de suréchantillonnage, filtre anti-continu) : il en
// faut donc une instance par canal.
//
// Avec g = 10^(driveDB/20) et un biais b, sur x normalisé dans [-1, 1] :
//
//	f(x) = tanh(g·(x+b)) − tanh(g·b)
//
// puis normalisation pour que la pleine échelle reste à la pleine échelle.
// Le terme tanh(g·b) garantit f(0) = 0 (le silence reste le silence). Avec
// b = 0 la courbe est symétrique (harmoniques impaires seulement) ; avec b > 0
// elle est asymétrique, comme celle d'un tube, et ajoute des harmoniques paires
// (2e, 4e…), perçues comme plus chaudes. Un filtre passe-haut retire alors la
// composante continue que l'asymétrie fait apparaître.
//
// La sortie d'une onde saturée, une fois limitée en bande, peut dépasser la
// pleine échelle (la fondamentale d'une onde carrée vaut 4/π ≈ 1,27 fois son
// sommet, et le filtrage supprime les harmoniques qui la maintenaient en
// dessous). Un plafond doux (softCeiling) arrondit ces pics au lieu de les
// laisser écrêter net en fin de chaîne.
//
// La non-linéarité s'exécute à oversample × la fréquence d'échantillonnage,
// entre deux filtres passe-bas d'ordre 8 (coupure 0,43·fs) : les harmoniques
// qui dépasseraient Nyquist sont atténuées avant de se replier (aliasing)
// dans le spectre audible. Ces filtres arrondissent légèrement les aigus
// extrêmes (voir README).
type saturator struct {
	drive   float64 // g ; 0 = pas de saturation (gain de sortie seul)
	bias    float64
	biasOff float64 // tanh(g·b)
	norm    float64 // normalisation de la pleine échelle
	out     float64 // gain de sortie linéaire

	up, down [4]biquad // filtres de suréchantillonnage / décimation
	stages   int
	coupling [maxStages - 1]highPass1 // couplage entre étages, à la fréquence suréchantillonnée
	dcBlock  bool
	dc       highPass1 // anti-continu en sortie, à la fréquence d'origine
}

// newSaturators renvoie un saturateur par canal, ou nil si l'étage est sans effet
// (drive nul et gain de sortie nul) ou invalide (NaN).
func newSaturators(fs float64, channels int, s saturation) []*saturator {
	if fs <= 0 || channels < 1 || math.IsNaN(s.driveDB) || math.IsNaN(s.outputDB) || math.IsNaN(s.bias) {
		return nil
	}
	driveDB := math.Max(0, math.Min(maxDriveDB, s.driveDB))
	outputDB := math.Max(-maxOutputDB, math.Min(maxOutputDB, s.outputDB))
	if driveDB == 0 && outputDB == 0 {
		return nil
	}
	proto := saturator{out: math.Pow(10, outputDB/20)}
	proto.stages = 1
	if driveDB > 0 {
		if s.stages > 1 {
			proto.stages = min(s.stages, maxStages)
		}
		proto.drive = math.Pow(10, driveDB/float64(proto.stages)/20) // drive réparti entre les étages
		proto.bias = math.Max(-maxBias, math.Min(maxBias, s.bias))
		proto.biasOff = math.Tanh(proto.drive * proto.bias)
		raw := func(x float64) float64 { return math.Tanh(proto.drive*(x+proto.bias)) - proto.biasOff }
		proto.norm = 1 / math.Max(math.Abs(raw(1)), math.Abs(raw(-1)))

		fsUp := fs * oversample
		for i, q := range butterworth8Q {
			c := lowPass(fsUp, oversampleHz*fs, q)
			proto.up[i] = biquad{coef: c}
			proto.down[i] = biquad{coef: c}
		}
		for i := range proto.coupling {
			proto.coupling[i].r = math.Exp(-2 * math.Pi * couplingHz / fsUp)
		}
		if proto.bias != 0 {
			proto.dcBlock = true
			proto.dc.r = math.Exp(-2 * math.Pi * dcCutoffHz / fs)
		}
	}
	sats := make([]*saturator, channels)
	for i := range sats {
		c := proto // copie : chaque canal a ses propres états
		sats[i] = &c
	}
	return sats
}

// shape est la courbe de saturation statique, sur x normalisé.
func (s *saturator) shape(x float64) float64 {
	return (math.Tanh(s.drive*(x+s.bias)) - s.biasOff) * s.norm
}

// apply traite un échantillon exprimé dans l'échelle int16.
func (s *saturator) apply(y float64) float64 {
	x := y / 32768
	if s.drive > 0 {
		var o float64
		for k := 0; k < oversample; k++ {
			u := 0.0
			if k == 0 {
				u = x * oversample // insertion de zéros, compensée par le facteur
			}
			for i := range s.up {
				u = s.up[i].process(u)
			}
			for st := 0; st < s.stages; st++ {
				u = -s.shape(u) // un étage à tube inverse la phase
				if st < s.stages-1 {
					u = s.coupling[st].process(u) // couplage : retire la continue entre étages
				}
			}
			if s.stages%2 == 1 {
				u = -u // nombre impair d'étages : on rétablit la polarité d'origine
			}
			for i := range s.down {
				u = s.down[i].process(u)
			}
			o = u // décimation : on garde la dernière sortie du groupe
		}
		x = o
		if s.dcBlock {
			x = s.dc.process(x)
		}
	}
	x *= s.out
	if s.drive > 0 {
		x = softCeiling(x)
	}
	return x * 32768
}
