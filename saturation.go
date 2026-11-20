package bassboost

import "math"

const (
	maxDriveDB  = 24.0 // |drive| maximal accepté
	maxOutputDB = 24.0 // |gain de sortie| maximal accepté
)

// saturator applique une saturation douce (tangente hyperbolique) puis un
// gain de sortie, échantillon par échantillon (sans état, donc identique sur
// tous les canaux).
//
// Avec g = 10^(driveDB/20), y = tanh(g·x) / tanh(g) sur x normalisé dans
// [-1, 1] : la pleine échelle reste à la pleine échelle, les signaux faibles
// sont amplifiés d'environ g/tanh(g) et les pics sont arrondis au lieu d'être
// écrêtés net, ce qui ajoute des harmoniques impaires (grain, chaleur).
//
// Pas de suréchantillonnage : à fort drive sur des aigus très forts, un peu
// de repliement de spectre (aliasing) est possible.
type saturator struct {
	drive float64 // g ; 0 = pas de saturation
	norm  float64 // 1/tanh(g)
	out   float64 // gain de sortie linéaire
}

// newSaturator renvoie nil si l'étage est sans effet (drive <= 0 et gain nul).
func newSaturator(driveDB, outputDB float64) *saturator {
	if math.IsNaN(driveDB) || math.IsNaN(outputDB) {
		return nil
	}
	driveDB = math.Max(0, math.Min(maxDriveDB, driveDB))
	outputDB = math.Max(-maxOutputDB, math.Min(maxOutputDB, outputDB))
	if driveDB == 0 && outputDB == 0 {
		return nil
	}
	s := &saturator{out: math.Pow(10, outputDB/20)}
	if driveDB > 0 {
		s.drive = math.Pow(10, driveDB/20)
		s.norm = 1 / math.Tanh(s.drive)
	}
	return s
}

// apply traite un échantillon exprimé dans l'échelle int16.
func (s *saturator) apply(y float64) float64 {
	x := y / 32768
	if s.drive > 0 {
		x = math.Tanh(s.drive*x) * s.norm
	}
	return x * s.out * 32768
}
