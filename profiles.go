package bassboost

import (
	"io"
	"math"
	"math/cmplx"
	"strings"
)

// Profile est un profil d'égaliseur prédéfini, adapté à un genre musical.
type Profile int

const (
	Flat       Profile = iota // aucune correction
	Rock                      // sourire : basses et aigus relevés, médiums creusés
	Pop                       // voix en avant, basses et brillance modérées
	Classical                 // naturel, léger renfort des extrêmes
	Jazz                      // chaud, médiums doux, aigus aérés
	HipHop                    // basses profondes, voix détachée
	Electronic                // sub-basses et aigus marqués, médiums creusés
	Acoustic                  // naturel, présence des cordes et de l'air
	Vocal                     // voix parlée / chantée : intelligibilité, graves réduits
	numProfiles
)

var profileNames = [numProfiles]string{
	Flat: "flat", Rock: "rock", Pop: "pop", Classical: "classical", Jazz: "jazz",
	HipHop: "hiphop", Electronic: "electronic", Acoustic: "acoustic", Vocal: "vocal",
}

// profileBands donne la courbe de chaque profil à intensité 1. Ces valeurs
// sont des points de départ raisonnables, pas une norme : il n'existe pas de
// standard officiel de ces courbes.
var profileBands = [numProfiles][]Band{
	Flat: nil,
	Rock: {
		{Type: LowShelf, FreqHz: 100, GainDB: 5},
		{Type: Peaking, FreqHz: 400, GainDB: -4, Q: 1},
		{Type: Peaking, FreqHz: 3000, GainDB: 4, Q: 1},
		{Type: HighShelf, FreqHz: 8000, GainDB: 3},
	},
	Pop: {
		{Type: LowShelf, FreqHz: 80, GainDB: 2},
		{Type: Peaking, FreqHz: 250, GainDB: -2, Q: 1},
		{Type: Peaking, FreqHz: 2500, GainDB: 3, Q: 1},
		{Type: HighShelf, FreqHz: 8000, GainDB: 2},
	},
	Classical: {
		{Type: LowShelf, FreqHz: 80, GainDB: 1.5},
		{Type: Peaking, FreqHz: 2000, GainDB: 1, Q: 0.7},
		{Type: HighShelf, FreqHz: 8000, GainDB: 2},
	},
	Jazz: {
		{Type: LowShelf, FreqHz: 120, GainDB: 3},
		{Type: Peaking, FreqHz: 800, GainDB: -1, Q: 1},
		{Type: Peaking, FreqHz: 3000, GainDB: 2, Q: 1},
		{Type: HighShelf, FreqHz: 10000, GainDB: 1.5},
	},
	HipHop: {
		{Type: LowShelf, FreqHz: 60, GainDB: 6},
		{Type: Peaking, FreqHz: 300, GainDB: -2, Q: 1},
		{Type: Peaking, FreqHz: 3000, GainDB: 2, Q: 1},
		{Type: HighShelf, FreqHz: 10000, GainDB: 2},
	},
	Electronic: {
		{Type: LowShelf, FreqHz: 60, GainDB: 5},
		{Type: Peaking, FreqHz: 500, GainDB: -3, Q: 1},
		{Type: Peaking, FreqHz: 4000, GainDB: 2, Q: 1},
		{Type: HighShelf, FreqHz: 9000, GainDB: 4},
	},
	Acoustic: {
		{Type: LowShelf, FreqHz: 100, GainDB: 2},
		{Type: Peaking, FreqHz: 3500, GainDB: 2.5, Q: 0.8},
		{Type: HighShelf, FreqHz: 9000, GainDB: 2.5},
	},
	Vocal: {
		{Type: LowShelf, FreqHz: 120, GainDB: -4},
		{Type: Peaking, FreqHz: 1500, GainDB: 2, Q: 0.8},
		{Type: Peaking, FreqHz: 3500, GainDB: 3, Q: 1},
	},
}

// profileSaturation donne, pour les profils qui en ont, la saturation douce et
// le gain de sortie appliqués après l'égalisation, à intensité 1.
// driveDB : gain poussé dans la saturation (plus c'est haut, plus le grain est marqué) ;
// outputDB : gain de sortie final, pour régler le niveau après saturation.
var profileSaturation = [numProfiles]struct{ driveDB, outputDB float64 }{
	Rock: {driveDB: 6, outputDB: -1},
}

// Profiles renvoie tous les profils disponibles.
func Profiles() []Profile {
	ps := make([]Profile, numProfiles)
	for i := range ps {
		ps[i] = Profile(i)
	}
	return ps
}

// String renvoie le nom du profil ("rock", "pop"...), ou "unknown".
func (p Profile) String() string {
	if p < 0 || p >= numProfiles {
		return "unknown"
	}
	return profileNames[p]
}

// ParseProfile retrouve un profil par son nom (insensible à la casse).
func ParseProfile(name string) (Profile, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, n := range profileNames {
		if n == name {
			return Profile(i), true
		}
	}
	return Flat, false
}

// Bands renvoie une copie des bandes du profil à intensité 1 (nil pour Flat
// ou un profil inconnu). Utile pour les afficher ou partir d'un profil pour
// construire le sien avec Equalizer.
func (p Profile) Bands() []Band {
	if p < 0 || p >= numProfiles {
		return nil
	}
	return append([]Band(nil), profileBands[p]...)
}

// ApplyProfile applique un profil d'égaliseur à un flux PCM (s16le, entrelacé).
//
// intensity met la courbe à l'échelle : 0 = aucun effet, 1 = profil complet
// (valeurs ramenées dans [0, 1]).
//
// Certains profils (Rock) ajoutent aussi une saturation douce suivie d'un
// gain de sortie : leur dosage suit l'intensité (0 = aucun effet).
//
// Pour éviter l'écrêtage, un gain global négatif est ajouté automatiquement :
// il ramène à 0 dB la fréquence la plus renforcée par le profil. Le volume
// perçu baisse donc d'autant ; compensez-le au besoin en aval.
func ApplyProfile(src io.Reader, sampleRate, channels int, p Profile, intensity float64) io.Reader {
	bands := p.Bands()
	if sampleRate <= 0 || len(bands) == 0 || intensity <= 0 || math.IsNaN(intensity) {
		return Equalizer(src, sampleRate, channels, nil)
	}
	intensity = math.Min(1, intensity)
	for i := range bands {
		bands[i].GainDB *= intensity
	}
	if peak := peakResponseDB(float64(sampleRate), designBands(float64(sampleRate), bands)); peak > 0 {
		bands = append(bands, Band{Type: Gain, GainDB: -peak})
	}
	sat := profileSaturation[p]
	return newEqualizerReader(src, sampleRate, channels, bands,
		newSaturator(sat.driveDB*intensity, sat.outputDB*intensity))
}

// peakResponseDB renvoie le gain maximal (en dB) de la chaîne de filtres,
// mesuré sur une grille logarithmique de 20 Hz à presque Nyquist.
func peakResponseDB(fs float64, chain []coeffs) float64 {
	const points = 400
	lo, hi := 20.0, math.Min(20000, fs/2*0.99)
	if hi <= lo {
		return 0
	}
	peak := math.Inf(-1)
	for i := 0; i < points; i++ {
		f := lo * math.Pow(hi/lo, float64(i)/(points-1))
		z := cmplx.Rect(1, -2*math.Pi*f/fs) // z^-1
		h := complex(1, 0)
		for _, c := range chain {
			num := complex(c.b0, 0) + complex(c.b1, 0)*z + complex(c.b2, 0)*z*z
			den := 1 + complex(c.a1, 0)*z + complex(c.a2, 0)*z*z
			h *= num / den
		}
		peak = math.Max(peak, 20*math.Log10(cmplx.Abs(h)))
	}
	return peak
}
