package panel

import (
	"math"

	"github.com/loxq/audioprofile"
)

// Le schéma décrit à la page les paramètres : libellé, bornes du curseur, pas, échelle. Les bornes d'un curseur sont
// celles où le réglage a du sens ; la validation, elle, reste celle de l'hôte (une valeur tapée hors du curseur est
// acceptée si l'hôte l'accepte).

// Field décrit un paramètre numérique (ou booléen).
type Field struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Unit     string  `json:"unit,omitempty"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Step     float64 `json:"step"`
	Log      bool    `json:"log,omitempty"`  // curseur à échelle logarithmique (fréquences, temps)
	Int      bool    `json:"int,omitempty"`  // entier
	Bool     bool    `json:"bool,omitempty"` // 0 ou 1 : case à cocher
	Def      float64 `json:"def"`
	Required bool    `json:"required,omitempty"`
	Doc      string  `json:"doc,omitempty"`
}

// FXSchema décrit un effet.
type FXSchema struct {
	Name   string  `json:"name"`
	Doc    string  `json:"doc"`
	Params []Field `json:"params"`
}

// Schema est tout ce que la page doit savoir pour construire ses curseurs. NewSchema remplit ce qui est propre
// aux profils et aux amplis ; l'hôte ajoute les effets et les noms (FX, Profiles, Amps).
type Schema struct {
	Rate       int        `json:"rate"`
	Channels   int        `json:"channels"`
	Intensity  Field      `json:"intensity"`
	Width      Field      `json:"width"`
	BandTypes  []string   `json:"bandTypes"` // dans l'ordre du sélecteur
	Band       []Field    `json:"band"`
	Compressor []Field    `json:"compressor"`
	Saturation []Field    `json:"saturation"`
	Limiter    []Field    `json:"limiter"`
	Amp        []Field    `json:"amp"`
	FX         []FXSchema `json:"fx"`
	Profiles   []string   `json:"profiles"`
	Amps       []string   `json:"amps"`
	// MeterFreqs : fréquences centrales (Hz) des bandes du spectre de MeterFrame ; vide si le Backend ne mesure pas, et
	// la page n'affiche alors pas les vumètres.
	MeterFreqs []float64 `json:"meterFreqs,omitempty"`
	// Defaults : valeurs d'un étage qu'on active alors que le profil n'en a pas.
	Defaults struct {
		Compression *Comp    `json:"compression"`
		Saturation  *Sat     `json:"saturation"`
		Multiband   *Multi   `json:"multiband"`
		Limiter     *Limiter `json:"limiter"`
	} `json:"defaults"`
}

// NumField décrit un paramètre numérique de bornes [lo, hi] et de valeur de départ def ; le pas est choisi selon la
// plage. log : échelle logarithmique (lo > 0) ; integer : valeurs entières.
func NumField(key, label, unit string, lo, hi, def float64, log, integer bool) Field {
	f := Field{Key: key, Label: label, Unit: unit, Min: lo, Max: hi, Def: def, Log: log, Int: integer}
	switch span := hi - lo; {
	case integer:
		f.Step = 1
	case log: // la position du curseur donne déjà la progression
	case span <= 2:
		f.Step = 0.01
	case span <= 30:
		f.Step = 0.1
	case span <= 300:
		f.Step = 0.5
	default:
		f.Step = 1
	}
	return f
}

// NewSchema construit la partie du schéma qui décrit les profils et les amplis, pour une fréquence d'échantillonnage donnée
// (la fréquence maximale d'une bande reste sous Nyquist).
func NewSchema(rate, channels int) Schema {
	fmax := math.Min(20000, math.Floor(float64(rate)/2*0.95))
	s := Schema{Rate: rate, Channels: channels}
	s.Intensity = NumField("intensity", "Intensité", "", 0, audioprofile.MaxIntensity, 1, false, false)
	s.Width = NumField("width", "Largeur stéréo", "×", 0, audioprofile.MaxWidth, 1, false, false)
	s.BandTypes = []string{"lowshelf", "peaking", "highshelf", "highpass", "lowpass", "notch", "gain"}
	s.Band = []Field{
		NumField("freq", "Fréquence", "Hz", 20, fmax, 1000, true, false),
		NumField("gain", "Gain", "dB", -24, 24, 0, false, false),
		NumField("q", "Q", "", 0.1, 20, 1, true, false),
		NumField("order", "Ordre", "", 1, 8, 2, false, true),
	}
	s.Compressor = []Field{
		NumField("threshold", "Seuil", "dBFS", -80, 0, -20, false, false),
		NumField("ratio", "Ratio", ":1", 1, 20, 2, false, false),
		NumField("attack", "Attaque", "ms", 0.1, 1000, 10, true, false),
		NumField("release", "Relâchement", "ms", 1, 5000, 100, true, false),
		NumField("knee", "Genou", "dB", 0, 24, 6, false, false),
		NumField("makeup", "Compensation", "dB", -12, 24, 0, false, false),
	}
	s.Saturation = []Field{
		NumField("drive", "Drive", "dB", 0, 36, 3, false, false),
		NumField("bias", "Asymétrie", "", 0, 1, 0, false, false),
		NumField("output", "Sortie", "dB", -24, 24, 0, false, false),
		NumField("stages", "Étages", "", 0, 4, 1, false, true),
	}
	s.Limiter = []Field{
		NumField("ceiling", "Plafond", "dBFS", -40, 0, -1, false, false),
		NumField("lookahead", "Anticipation", "ms", 0.1, 50, 5, true, false),
		NumField("release", "Relâchement", "ms", 1, 1000, 100, true, false),
	}
	s.Amp = []Field{
		NumField("drive", "Drive", "dB", 0, 36, 6, false, false),
		NumField("stages", "Étages", "", 1, 4, 2, false, true),
		NumField("bias", "Asymétrie", "", 0, 1, 0.2, false, false),
		NumField("bass", "Basses", "dB", -12, 12, 0, false, false),
		NumField("mid", "Médiums", "dB", -12, 12, 0, false, false),
		NumField("treble", "Aigus", "dB", -12, 12, 0, false, false),
		NumField("output", "Sortie", "dB", -24, 12, 0, false, false),
	}
	s.Defaults.Compression = &Comp{Threshold: -20, Ratio: 2, Attack: 10, Release: 100, Knee: 6}
	s.Defaults.Saturation = &Sat{Drive: 3, Stages: 1}
	s.Defaults.Limiter = &Limiter{Ceiling: -1, Lookahead: 5, Release: 100}
	s.Defaults.Multiband = ProfileFrom(audioprofile.RadioNatural.Settings()).Multiband
	return s
}
