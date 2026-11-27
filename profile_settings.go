package audioprofile

import (
	"io"
	"math"
)

// SaturationSettings règle l'étage de saturation douce d'un profil, appliqué après le
// compresseur. Tous les champs nuls : sans effet.
type SaturationSettings struct {
	DriveDB  float64 // gain poussé dans la saturation (plus c'est haut, plus le grain est marqué)
	Bias     float64 // asymétrie β, de 0 (harmoniques impaires) à 1 (ajoute des harmoniques paires)
	OutputDB float64 // gain de sortie final, pour régler le niveau après saturation
	Stages   int     // étages en cascade (0 ou 1 = un seul) ; le drive est réparti entre eux
}

// ProfileSettings décrit un profil complet, à intensité 1 : c'est ce que ApplyProfile lit dans ses
// tables. Les étages sont appliqués dans l'ordre
//
//	égalisation → compresseur → multibande → saturation → élargissement stéréo → limiteur
//
// et un étage nul (nil, 0 ou vide) est absent. Obtenez le point de départ avec Profile.Settings, modifiez-le,
// puis passez-le à ApplyProfileSettings.
type ProfileSettings struct {
	Bands       []Band              // égalisation (le gain automatique n'en fait pas partie : il est calculé à l'application)
	Compression *CompressorSettings // compresseur après l'égalisation (nil : aucun)
	Saturation  SaturationSettings
	Multiband   *MultibandSettings // compression multibande (nil : aucune)
	Width       float64            // largeur stéréo à intensité 1 (0 : pas d'élargissement)
	Limiter     *LimiterSettings   // limiteur de crête, dernier étage (nil : aucun)
}

// Settings renvoie une copie des réglages du profil à intensité 1 (réglages vides pour Flat ou un profil
// inconnu). Contrairement à Bands, tous les étages y sont : compresseur, saturation, multibande, largeur
// et limiteur. Le résultat peut être modifié librement.
func (p Profile) Settings() ProfileSettings {
	if p < 0 || p >= numProfiles {
		return ProfileSettings{}
	}
	s := ProfileSettings{Bands: p.Bands()}
	if c := profileCompression[p]; c != nil {
		cc := *c
		s.Compression = &cc
	}
	sat := profileSaturation[p]
	s.Saturation = SaturationSettings{DriveDB: sat.driveDB, Bias: sat.bias, OutputDB: sat.outputDB, Stages: sat.stages}
	r := profileRadio[p]
	if r.multi != nil {
		m := cloneMultiband(*r.multi)
		s.Multiband = &m
	}
	s.Width = r.width
	if r.limit != nil {
		l := *r.limit
		s.Limiter = &l
	}
	return s
}

func cloneMultiband(m MultibandSettings) MultibandSettings {
	return MultibandSettings{Crossovers: append([]float64(nil), m.Crossovers...), Bands: append([]CompressorSettings(nil), m.Bands...)}
}

// active indique si les réglages modifient le son.
func (s ProfileSettings) active() bool {
	return len(s.Bands) > 0 || s.Compression != nil || s.Saturation != SaturationSettings{} ||
		s.Multiband != nil || s.Width != 0 || s.Limiter != nil
}

// ApplyProfileSettings est ApplyProfile avec des réglages libres : ApplyProfile(src, …, p, i) équivaut à
// ApplyProfileSettings(src, …, p.Settings(), i). L'intensité et le gain automatique se comportent de la même façon
// (le gain automatique est recalculé pour les bandes données, sauf si un limiteur est présent). Le flux est renvoyé
// tel quel si les réglages sont vides, si l'intensité est <= 0 ou NaN, ou si sampleRate <= 0.
func ApplyProfileSettings(src io.Reader, sampleRate, channels int, s ProfileSettings, intensity float64) io.Reader {
	if sampleRate <= 0 || !s.active() || intensity <= 0 || math.IsNaN(intensity) {
		return Equalizer(src, sampleRate, channels, nil)
	}
	bands := append([]Band(nil), s.Bands...)
	intensity = math.Min(MaxIntensity, intensity)
	for i := range bands {
		bands[i].GainDB *= intensity
	}
	// Le gain automatique ramène le pic de l'égalisation à 0 dB. Les profils avec limiteur
	// n'en ont pas besoin : le limiteur garantit déjà leur plafond.
	if peak := peakResponseDB(float64(sampleRate), designBands(float64(sampleRate), bands)); peak > 0 && s.Limiter == nil {
		bands = append(bands, Band{Type: Gain, GainDB: -peak})
	}
	var comp *CompressorSettings
	if s.Compression != nil {
		sc := scaleCompression(*s.Compression, intensity)
		comp = &sc
	}
	// L'asymétrie ne croît pas au-delà de l'intensité 1 : à fort drive, elle repousserait
	// le point de fonctionnement dans la zone saturée et les signaux faibles s'effondreraient.
	sat := s.Saturation
	out := sat.OutputDB * intensity
	if sat.OutputDB > 0 { // un gain positif ne croît pas au-delà de l'intensité 1 : il dépasserait la marge
		out = sat.OutputDB * math.Min(intensity, 1)
	}
	c := chain{
		bands: bands,
		comp:  comp,
		sat:   &saturation{driveDB: sat.DriveDB * intensity, bias: sat.Bias * math.Min(intensity, 1), outputDB: out, stages: sat.Stages},
	}
	if s.Multiband != nil {
		m := scaleMultiband(*s.Multiband, intensity)
		c.multi = &m
	}
	if s.Width != 0 {
		c.stereo = &stereoSettings{width: 1 + (s.Width-1)*intensity}
	}
	if s.Limiter != nil {
		l := *s.Limiter
		c.limit = &l
	}
	return newChainReader(src, sampleRate, channels, c)
}
