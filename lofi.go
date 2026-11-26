package audioprofile

import (
	"io"
	"math"
)

// LoFiSettings règle l'effet lo-fi. Un champ nul désactive sa partie : une structure vide n'a aucun effet.
type LoFiSettings struct {
	LowCutHz     float64 // passe-haut du second ordre : retire les graves, comme un petit haut-parleur ; 0 : aucun
	HighCutHz    float64 // passe-bas du quatrième ordre : retire les aigus, comme une vieille bande ou un téléphone ; 0 : aucun
	Bits         int     // quantification sur ce nombre de bits, de 1 à 15 : le grain d'un convertisseur d'autrefois ; 0 ou >= 16 : aucune
	SampleRateHz float64 // réduction de la fréquence d'échantillonnage, par échantillonneur-bloqueur (le repliement est voulu) ; 0 ou >= celle du flux : aucune
	Wobble       float64 // de 0 à 1 : instabilité de vitesse d'un tourne-disque ou d'une cassette (wow et flutter)
	DriveDB      float64 // saturation douce de bande magnétique, de 0 à 24 dB
	Crackle      float64 // de 0 à 1 : craquements de vinyle, environ 2 + 18 × Crackle par seconde
	Hiss         float64 // de 0 à 1 : souffle de bande, de −60 dBFS efficaces (presque rien) à −40 dBFS (Hiss = 1)
}

const (
	lofiWowRateHz     = 0.6    // vitesse du wow (dérive lente de la hauteur)
	lofiFlutterRateHz = 7.3    // vitesse du flutter (tremblement rapide)
	lofiWowDeviation  = 0.004  // écart de hauteur du wow à Wobble = 1 : ±0,4 %
	lofiFlutterDev    = 0.0012 // écart de hauteur du flutter à Wobble = 1 : ±0,12 %
	lofiBaseDelayMs   = 4.0    // retard moyen de la ligne qui module la hauteur, rattrapé par le lecteur
	lofiHissBaseDB    = -60.0
	lofiHissSpanDB    = 20.0
	lofiHissCutoffHz  = 6000.0 // le souffle est filtré : plus de graves que de sifflement
	lofiCrackleBaseHz = 2.0
	lofiCrackleSpanHz = 18.0
	lofiClickDecayMs  = 0.5
	lofiClickBaseDB   = -45.0
	lofiClickSpanDB   = 27.0
	lofiLowCutOrder   = 2
	lofiHighCutOrder  = 4
	lofiSeed          = 88172645463325252 // graine arbitraire (celle de l exemple de Marsaglia)
)

// LoFi renvoie un flux PCM (s16le, entrelacé) dégradé comme un vieux support : un son « lo-fi ».
//
// Les étages, dans l'ordre, sont tous facultatifs (un champ nul le désactive) :
//
//  1. DriveDB : saturation douce de bande magnétique (la saturation de type tube de la bibliothèque, suréchantillonnée),
//     calibrée pour ne rien changer aux signaux faibles ;
//  2. Wobble : instabilité de vitesse, c'est-à-dire la hauteur qui oscille. Deux oscillations s'additionnent : un wow lent (0,6 Hz,
//     ±0,4 % à Wobble = 1) et un flutter rapide (7,3 Hz, ±0,12 %). Elles passent par une ligne à retard à lecture interpolée
//     (voir Chorus) ; les deux canaux dérivent ensemble. Le retard moyen de 4 ms est rattrapé : le flux garde sa
//     longueur et son alignement sur l'entrée ;
//  3. SampleRateHz : fréquence d'échantillonnage réduite par échantillonneur-bloqueur, sans filtre anti-repliement : les
//     aigus se replient dans le spectre audible, c'est le son des vieux échantillonneurs. Un rapport non entier
//     (44 100 → 8 000) est géré par un accumulateur de phase ;
//  4. Bits : quantification de chaque échantillon sur ce nombre de bits, sans lissage (« dither ») : une distorsion de
//     quantification, de plus en plus audible aux faibles niveaux ;
//  5. LowCutHz et HighCutHz : bande passante réduite (passe-haut d'ordre 2, passe-bas d'ordre 4) ;
//  6. Crackle et Hiss : craquements (des impulsions qui s'éteignent en 0,5 ms, d'amplitude aléatoire, partagées par les
//     canaux, comme sur un sillon) et souffle (un bruit blanc filtré à 6 kHz, indépendant sur chaque canal).
//
// **Avec du bruit (Crackle ou Hiss), le silence n'est plus du silence** : l'effet en ajoute. Le bruit vient d'un
// générateur à graine fixe dont l'état est dans l'étage : deux passages sur le même flux donnent le même résultat, quelle que
// soit la façon de lire. Sans bruit, un silence reste exactement du silence.
//
// Rien n'est compensé en niveau : réduire la bande passante baisse la sonie. Un plafond doux (genou à 0,8)
// arrondit les pics. Les valeurs sont ramenées dans leurs bornes (NaN compte pour 0). Si tous les champs sont nuls,
// ou si sampleRate <= 0, le flux est renvoyé tel quel.
func LoFi(src io.Reader, sampleRate, channels int, s LoFiSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage lo-fi (nil s'il est sans effet).
func (s LoFiSettings) stage(fs float64, channels int) frameStage {
	wobble := unitMix(s.Wobble)
	l := newLoFi(fs, channels, s, lofiWowDeviation*wobble, lofiFlutterDev*wobble)
	if l == nil {
		return nil // pas de *lofi nul dans l'interface : le lecteur le prendrait pour un étage
	}
	return l
}

// lofi est l'étage lo-fi.
type lofi struct {
	channels int
	delay    int // retard moyen de la ligne de hauteur, en trames (la latence rattrapée)

	sats   []*saturator // saturation (peut être nil)
	invLin float64

	lines []*modDelay // ligne à retard de chaque canal (nil sans Wobble)
	wow   lfo
	flut  lfo
	wowD  float64 // amplitude du retard du wow, en échantillons
	flutD float64 // et du flutter

	srStep  float64 // incrément de phase de l'échantillonneur-bloqueur (nil : 0)
	srPhase float64
	held    []float64

	step float64 // pas de quantification, échelle int16 (0 : aucune)

	filters [][]biquad // filtres de bande passante, par canal

	hiss   float64 // amplitude du souffle (avant normalisation)
	hissLP []biquad
	crack  float64 // probabilité de craquement par trame (0 : aucun)
	click  float64 // valeur courante du craquement en cours (partagé)
	clickK float64 // décroissance par trame
	crackC float64 // Crackle, pour l'amplitude
	rng    uint64
}

// newLoFi construit l'étage ; wowDev et flutterDev sont les écarts relatifs de hauteur (0,004 = ±0,4 %).
func newLoFi(fs float64, channels int, s LoFiSettings, wowDev, flutterDev float64) *lofi {
	if fs <= 0 || channels < 1 {
		return nil
	}
	clean := func(v float64) float64 {
		if math.IsNaN(v) || v < 0 {
			return 0
		}
		return v
	}
	l := &lofi{channels: channels, rng: lofiSeed}
	active := false

	if drive := math.Min(maxDriveDB, clean(s.DriveDB)); drive > 0 {
		l.sats = newSaturators(fs, channels, saturation{driveDB: drive, stages: 1})
		const eps = 1e-4
		l.invLin = eps / l.sats[0].shape(eps)
		active = true
	}
	if wowDev > 0 || flutterDev > 0 {
		l.delay = int(math.Round(lofiBaseDelayMs / 1000 * fs))
		if l.delay < 8 {
			l.delay = 8
		}
		// écart de hauteur d (relatif) à la vitesse r : profondeur de retard d / (2π r) secondes
		l.wowD = wowDev / (2 * math.Pi * lofiWowRateHz) * fs
		l.flutD = flutterDev / (2 * math.Pi * lofiFlutterRateHz) * fs
		maxD := l.wowD + l.flutD
		if maxD > float64(l.delay)-3 { // le retard doit rester >= 2 échantillons pour l'interpolation
			scale := (float64(l.delay) - 3) / maxD
			l.wowD, l.flutD = l.wowD*scale, l.flutD*scale
		}
		for ch := 0; ch < channels; ch++ {
			l.lines = append(l.lines, newModDelay(l.delay+int(math.Ceil(l.wowD+l.flutD))+4))
		}
		l.wow, l.flut = newLFO(lofiWowRateHz, fs, 0), newLFO(lofiFlutterRateHz, fs, 0.3)
		active = true
	}
	if sr := clean(s.SampleRateHz); sr > 0 && sr < fs {
		l.srStep = sr / fs
		l.srPhase = 1 - l.srStep // le premier échantillon est pris, puis un tous les 1/srStep
		l.held = make([]float64, channels)
		active = true
	}
	if s.Bits >= 1 && s.Bits <= 15 {
		l.step = math.Pow(2, float64(16-s.Bits))
		active = true
	}
	var coefs []coeffs
	if f := clean(s.LowCutHz); f > 0 && f < fs/2 {
		coefs = append(coefs, butterworth(fs, f, 0, lofiLowCutOrder, true)...)
	}
	if f := clean(s.HighCutHz); f > 0 && f < fs/2 {
		coefs = append(coefs, butterworth(fs, f, 0, lofiHighCutOrder, false)...)
	}
	if len(coefs) > 0 {
		for ch := 0; ch < channels; ch++ {
			f := make([]biquad, len(coefs))
			for i, c := range coefs {
				f[i] = biquad{coef: c}
			}
			l.filters = append(l.filters, f)
		}
		active = true
	}
	if h := unitMix(s.Hiss); h > 0 {
		rmsDB := lofiHissBaseDB + lofiHissSpanDB*h
		lp := onePole(fs, math.Min(lofiHissCutoffHz, fs/2*0.9), false)
		// le souffle est un bruit blanc filtré : on normalise pour que son niveau efficace soit celui annoncé
		probe := biquad{coef: lp}
		var energy float64
		for n := 0; n < 4096; n++ {
			x := 0.0
			if n == 0 {
				x = 1
			}
			y := probe.process(x)
			energy += y * y
		}
		l.hiss = 32768 * math.Pow(10, rmsDB/20) * math.Sqrt(3) / math.Sqrt(energy) // bruit uniforme : efficace = amplitude / √3
		for ch := 0; ch < channels; ch++ {
			l.hissLP = append(l.hissLP, biquad{coef: lp})
		}
		active = true
	}
	if c := unitMix(s.Crackle); c > 0 {
		l.crack = (lofiCrackleBaseHz + lofiCrackleSpanHz*c) / fs
		l.clickK = math.Exp(-1 / (lofiClickDecayMs / 1000 * fs))
		l.crackC = c
		active = true
	}
	if !active {
		return nil
	}
	return l
}

func (l *lofi) latency() int {
	if l.lines == nil {
		return 0
	}
	return l.delay
}

// uniform renvoie un nombre dans [0, 1) (xorshift64*, graine fixe : le résultat ne dépend que du nombre de tirages).
func (l *lofi) uniform() float64 {
	l.rng ^= l.rng >> 12
	l.rng ^= l.rng << 25
	l.rng ^= l.rng >> 27
	return float64((l.rng*0x2545F4914F6CDD1D)>>11) / (1 << 53)
}

func (l *lofi) apply(frame []float64) {
	var wowFlut float64
	if l.lines != nil {
		wowFlut = l.wowD*l.wow.next() + l.flutD*l.flut.next()
	}
	srTick := false
	if l.srStep > 0 {
		l.srPhase += l.srStep
		if l.srPhase >= 1 {
			l.srPhase -= 1
			srTick = true
		}
	}
	for ch := range frame {
		y := frame[ch]
		if l.sats != nil {
			y = l.sats[ch].apply(y) * l.invLin
		}
		if l.lines != nil {
			l.lines[ch].push(y)
			y = l.lines[ch].read(float64(l.delay) + wowFlut)
		}
		if l.srStep > 0 {
			if srTick {
				l.held[ch] = y
			}
			y = l.held[ch]
		}
		if l.step > 0 {
			y = math.Round(y/l.step) * l.step
		}
		if l.filters != nil {
			for i := range l.filters[ch] {
				y = l.filters[ch][i].process(y)
			}
		}
		if l.hiss > 0 {
			y += l.hissLP[ch].process((2*l.uniform()-1)*1) * l.hiss
		}
		frame[ch] = y
	}
	if l.crack > 0 {
		if l.uniform() < l.crack { // un craquement : signe et amplitude aléatoires
			amp := 32768 * math.Pow(10, (lofiClickBaseDB+lofiClickSpanDB*l.crackC*(0.2+0.8*l.uniform()))/20)
			if l.uniform() < 0.5 {
				amp = -amp
			}
			l.click += amp
		}
		for ch := range frame {
			frame[ch] += l.click
		}
		l.click = flushTiny(l.click * l.clickK)
	}
}
