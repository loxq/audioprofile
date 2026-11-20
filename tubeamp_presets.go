package audioprofile

import (
	"math"
	"strings"
)

// TubeAmpPreset est un préréglage d'ampli à tubes pour TubeAmp.
//
// Les trois préréglages sont réglés pour un morceau complet : le baffle est
// désactivé (activez TubeAmpSettings.Cabinet vous-même, avec le son étouffé que
// cela donne sur un mix), et la saturation reste plus mesurée que sur une guitare
// seule. Leur niveau est calibré pour rester proche de celui de l'entrée.
type TubeAmpPreset int

const (
	AmpClean  TubeAmpPreset = iota // chaleur discrète : saturation à peine audible
	AmpCrunch                      // saturation franche mais encore musicale
	AmpLead                        // forte saturation, très marquée sur un mix
	numAmpPresets
)

var ampPresetNames = [numAmpPresets]string{AmpClean: "clean", AmpCrunch: "crunch", AmpLead: "lead"}

// ampPresets donne les réglages de chaque préréglage à intensité 1. Ces valeurs
// sont des points de départ, vérifiés par mesure et non à l'oreille.
var ampPresets = [numAmpPresets]TubeAmpSettings{
	AmpClean:  {DriveDB: 4, Stages: 2, Bias: 0.10, BassDB: 1, OutputDB: -3.5},
	AmpCrunch: {DriveDB: 9, Stages: 2, Bias: 0.15, BassDB: 2, MidDB: -1, TrebleDB: 1, OutputDB: -2.5},
	AmpLead:   {DriveDB: 14, Stages: 3, Bias: 0.20, BassDB: 1, MidDB: 2, TrebleDB: 1, OutputDB: -5},
}

// TubeAmpPresets renvoie tous les préréglages disponibles.
func TubeAmpPresets() []TubeAmpPreset {
	ps := make([]TubeAmpPreset, numAmpPresets)
	for i := range ps {
		ps[i] = TubeAmpPreset(i)
	}
	return ps
}

// String renvoie le nom du préréglage ("clean", "crunch", "lead"), ou "unknown".
func (p TubeAmpPreset) String() string {
	if p < 0 || p >= numAmpPresets {
		return "unknown"
	}
	return ampPresetNames[p]
}

// ParseTubeAmpPreset retrouve un préréglage par son nom (insensible à la casse).
func ParseTubeAmpPreset(name string) (TubeAmpPreset, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, n := range ampPresetNames {
		if n == name {
			return TubeAmpPreset(i), true
		}
	}
	return AmpClean, false
}

// Settings renvoie les réglages du préréglage à intensité 1 (réglages nuls pour
// un préréglage inconnu). Le résultat peut être modifié librement, par exemple
// pour activer Cabinet, puis passé à TubeAmp.
func (p TubeAmpPreset) Settings() TubeAmpSettings {
	if p < 0 || p >= numAmpPresets {
		return TubeAmpSettings{}
	}
	return ampPresets[p]
}

// Scaled met les réglages à l'échelle d'une intensité : 0 = aucun effet,
// 1 = réglages inchangés, au-delà de 1 = exagéré. L'intensité est ramenée dans
// [0, MaxIntensity] ; NaN vaut 0.
//
// Le drive, l'asymétrie et le correcteur de timbre sont multipliés par
// l'intensité. Le gain de sortie négatif l'est aussi ; un gain de sortie positif
// ne croît pas au-delà de l'intensité 1 (il dépasserait la marge). Le nombre
// d'étages et Cabinet ne changent pas.
func (s TubeAmpSettings) Scaled(intensity float64) TubeAmpSettings {
	if math.IsNaN(intensity) || intensity < 0 {
		intensity = 0
	}
	intensity = math.Min(MaxIntensity, intensity)
	s.DriveDB *= intensity
	s.Bias *= intensity
	s.BassDB *= intensity
	s.MidDB *= intensity
	s.TrebleDB *= intensity
	if s.OutputDB > 0 {
		s.OutputDB *= math.Min(intensity, 1)
	} else {
		s.OutputDB *= intensity
	}
	return s
}
