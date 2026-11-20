package audioprofile

import (
	"io"
	"math"
)

// TubeAmpSettings décrit un ampli à tubes simulé.
type TubeAmpSettings struct {
	DriveDB float64 // gain total poussé dans les étages, réparti entre eux (0 = pas de saturation)
	Stages  int     // étages à tube en cascade, de 1 à 4 ; <= 0 → 2
	Bias    float64 // asymétrie des tubes, de 0 (symétrique) à 0,5 ; ajoute des harmoniques paires

	// Correcteur de timbre, appliqué après les étages (en dB, + = renforcé).
	BassDB   float64 // basses : shelf à 100 Hz
	MidDB    float64 // médiums : cloche à 650 Hz (Q = 0,8)
	TrebleDB float64 // aigus : shelf à 4 kHz

	// Cabinet active une simulation de baffle et de haut-parleur : coupe sous
	// 80 Hz, bosse de résonance à 110 Hz, présence à 2,8 kHz, coupure des aigus
	// vers 5 kHz. Sur un morceau complet, cela donne un son volontairement étouffé,
	// « de radio » : à réserver à un usage créatif (ou à une guitare seule).
	Cabinet bool

	OutputDB float64 // gain de sortie final
}

const (
	defaultStages = 2

	toneBassHz   = 100.0
	toneMidHz    = 650.0
	toneMidQ     = 0.8
	toneTrebleHz = 4000.0

	cabHighPassHz = 80.0
	cabResonHz    = 110.0
	cabResonDB    = 3.0
	cabResonQ     = 1.2
	cabPresenceHz = 2800.0
	cabPresenceDB = 3.0
	cabPresenceQ  = 1.0
	cabLowPassHz  = 5000.0
)

// TubeAmp renvoie un flux PCM (s16le, entrelacé) passé dans un ampli à tubes
// simulé : des étages de saturation asymétriques en cascade, puis un
// correcteur de timbre et, en option, un baffle.
//
// Chaque étage utilise la saturation de type tube suréchantillonnée décrite
// dans le README (courbe tanh asymétrique, suréchantillonnage ×4, plafond doux).
// Entre deux étages, la phase est inversée, comme dans un vrai préampli, et un
// filtre de couplage (30 Hz) retire la composante continue que l'asymétrie fait
// apparaître. Le drive total est réparti à parts égales entre les étages.
//
// Les filtres posés après les étages (timbre et baffle) sont suivis d'un gain
// automatique qui ramène leur réponse maximale à 0 dB, pour ne pas faire écrêter
// la sortie : un renfort de timbre baisse donc le reste d'autant.
//
// Un plafond doux en sortie (genou à 0,8) arrondit les pics qui dépasseraient
// encore la pleine échelle au lieu de les écrêter net : un filtre à 0 dB au maximum
// peut en effet déphaser les harmoniques d'une onde saturée de façon à additionner
// leurs pics (dépassement de 1,36 fois la pleine échelle mesuré avec le baffle,
// +8 dB de basses et 18 dB de drive).
//
// Aucune compensation de niveau n'est faite pour le reste : la saturation relève
// les signaux faibles ; ajustez OutputDB au besoin.
//
// Sans effet (flux renvoyé tel quel) si sampleRate <= 0 ou si tous les réglages
// sont nuls et Cabinet est faux.
func TubeAmp(src io.Reader, sampleRate, channels int, s TubeAmpSettings) io.Reader {
	stages := s.Stages
	if stages <= 0 {
		stages = defaultStages
	}
	c := chain{
		sat:  &saturation{driveDB: s.DriveDB, bias: s.Bias, outputDB: s.OutputDB, stages: stages},
		post: tubeAmpPost(float64(sampleRate), s),
	}
	return newChainReader(src, sampleRate, channels, c)
}

// tubeAmpPost calcule les filtres posés après les étages : correcteur de timbre,
// puis baffle, puis gain automatique.
func tubeAmpPost(fs float64, s TubeAmpSettings) []coeffs {
	if fs <= 0 {
		return nil
	}
	post := designBands(fs, []Band{
		{Type: LowShelf, FreqHz: toneBassHz, GainDB: s.BassDB},
		{Type: Peaking, FreqHz: toneMidHz, GainDB: s.MidDB, Q: toneMidQ},
		{Type: HighShelf, FreqHz: toneTrebleHz, GainDB: s.TrebleDB},
	})
	if s.Cabinet {
		post = append(post, cabinetFilters(fs)...)
	}
	if len(post) == 0 {
		return nil
	}
	if peak := peakResponseDB(fs, post); peak > 0 {
		post = append(post, coeffs{b0: math.Pow(10, -peak/20)})
	}
	return post
}

// cabinetFilters renvoie les filtres d'un baffle : passe-haut, résonance,
// présence, puis passe-bas d'ordre 4. Les filtres dont la fréquence dépasse
// Nyquist sont ignorés.
func cabinetFilters(fs float64) []coeffs {
	var f []coeffs
	ok := func(hz float64) bool { return hz < fs/2 }
	if ok(cabHighPassHz) {
		f = append(f, highPass(fs, cabHighPassHz, defaultQ))
	}
	if ok(cabResonHz) {
		f = append(f, peaking(fs, cabResonHz, cabResonDB, cabResonQ))
	}
	if ok(cabPresenceHz) {
		f = append(f, peaking(fs, cabPresenceHz, cabPresenceDB, cabPresenceQ))
	}
	if ok(cabLowPassHz) {
		f = append(f, lowPass(fs, cabLowPassHz, 0.5412), lowPass(fs, cabLowPassHz, 1.3066)) // Butterworth d'ordre 4
	}
	return f
}
