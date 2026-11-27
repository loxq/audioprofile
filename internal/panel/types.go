package panel

import (
	"fmt"
	"math"

	"github.com/loxq/audioprofile"
)

// Les types de ce fichier sont ce qui circule en JSON entre la page et le serveur : tout ce que règle la page.
// Les conversions vers les réglages de la bibliothèque n'en vérifient pas les bornes (c'est le travail de l'hôte, qui
// connaît la fréquence d'échantillonnage) : elles ne refusent que ce qui n'a pas de sens, comme un type de bande inconnu.

// Band est une bande d'égalisation.
type Band struct {
	Type  string  `json:"type"`
	Freq  float64 `json:"freq"`
	Gain  float64 `json:"gain"`
	Q     float64 `json:"q"`
	Order int     `json:"order"`
}

// Comp est un compresseur.
type Comp struct {
	Threshold float64 `json:"threshold"`
	Ratio     float64 `json:"ratio"`
	Attack    float64 `json:"attack"`
	Release   float64 `json:"release"`
	Knee      float64 `json:"knee"`
	Makeup    float64 `json:"makeup"`
}

// Sat est la saturation d'un profil.
type Sat struct {
	Drive  float64 `json:"drive"`
	Bias   float64 `json:"bias"`
	Output float64 `json:"output"`
	Stages int     `json:"stages"`
}

// Multi est une compression multibande : len(Bands) = len(Crossovers)+1.
type Multi struct {
	Crossovers []float64 `json:"crossovers"`
	Bands      []Comp    `json:"bands"`
}

// Limiter est un limiteur de crête.
type Limiter struct {
	Ceiling   float64 `json:"ceiling"`
	Lookahead float64 `json:"lookahead"`
	Release   float64 `json:"release"`
}

// Profile est un profil complet ; un étage absent vaut null.
type Profile struct {
	Bands       []Band   `json:"bands"`
	Compression *Comp    `json:"compression"`
	Saturation  *Sat     `json:"saturation"`
	Multiband   *Multi   `json:"multiband"`
	Width       float64  `json:"width"`
	Limiter     *Limiter `json:"limiter"`
}

// Amp est un ampli à tubes.
type Amp struct {
	Drive   float64 `json:"drive"`
	Stages  int     `json:"stages"`
	Bias    float64 `json:"bias"`
	Bass    float64 `json:"bass"`
	Mid     float64 `json:"mid"`
	Treble  float64 `json:"treble"`
	Cabinet bool    `json:"cabinet"`
	Output  float64 `json:"output"`
}

// FX est un effet de la chaîne, avec tous ses paramètres.
type FX struct {
	Name    string             `json:"name"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params"`
}

// Kinds du traitement principal.
const (
	KindNone    = "none"
	KindProfile = "profile"
	KindAmp     = "amp"
)

// State est tout ce que règle la page.
type State struct {
	Kind      string   `json:"kind"` // KindProfile, KindAmp ou KindNone
	Name      string   `json:"name"`
	Intensity float64  `json:"intensity"`
	Bypass    bool     `json:"bypass"` // true : l'original, pour comparer
	Profile   *Profile `json:"profile,omitempty"`
	Amp       *Amp     `json:"amp,omitempty"`
	FX        []FX     `json:"fx"`
}

// CompFrom convertit un compresseur de la bibliothèque.
func CompFrom(c audioprofile.CompressorSettings) Comp {
	return Comp{c.ThresholdDB, c.Ratio, c.AttackMs, c.ReleaseMs, c.KneeDB, c.MakeupDB}
}

// Settings convertit vers la bibliothèque.
func (c Comp) Settings() audioprofile.CompressorSettings {
	return audioprofile.CompressorSettings{ThresholdDB: c.Threshold, Ratio: c.Ratio, AttackMs: c.Attack, ReleaseMs: c.Release, KneeDB: c.Knee, MakeupDB: c.Makeup}
}

// ProfileFrom convertit les réglages d'un profil. Les valeurs par défaut de la bibliothèque (Q et ordre nuls) sont
// rendues explicites, pour que la page ait une valeur à montrer : le son est le même.
func ProfileFrom(s audioprofile.ProfileSettings) *Profile {
	pp := &Profile{Bands: []Band{}, Width: s.Width}
	for _, b := range s.Bands {
		q, order := b.Q, b.Order
		if q <= 0 {
			q = math.Sqrt2 / 2
			if b.Type == audioprofile.Notch {
				q = 10
			}
		}
		if order <= 0 {
			order = 2
		}
		pp.Bands = append(pp.Bands, Band{Type: b.Type.String(), Freq: b.FreqHz, Gain: b.GainDB, Q: q, Order: order})
	}
	if s.Compression != nil {
		c := CompFrom(*s.Compression)
		pp.Compression = &c
	}
	if s.Saturation != (audioprofile.SaturationSettings{}) {
		pp.Saturation = &Sat{s.Saturation.DriveDB, s.Saturation.Bias, s.Saturation.OutputDB, s.Saturation.Stages}
	}
	if m := s.Multiband; m != nil {
		pm := &Multi{Crossovers: append([]float64{}, m.Crossovers...), Bands: []Comp{}}
		for _, c := range m.Bands {
			pm.Bands = append(pm.Bands, CompFrom(c))
		}
		pp.Multiband = pm
	}
	if l := s.Limiter; l != nil {
		pp.Limiter = &Limiter{l.CeilingDB, l.LookaheadMs, l.ReleaseMs}
	}
	return pp
}

// Settings convertit vers la bibliothèque.
func (pp *Profile) Settings() (audioprofile.ProfileSettings, error) {
	var s audioprofile.ProfileSettings
	for i, b := range pp.Bands {
		t, ok := audioprofile.ParseBandType(b.Type)
		if !ok {
			return s, fmt.Errorf("bands[%d] : type %q inconnu", i, b.Type)
		}
		s.Bands = append(s.Bands, audioprofile.Band{Type: t, FreqHz: b.Freq, GainDB: b.Gain, Q: b.Q, Order: b.Order})
	}
	if pp.Compression != nil {
		c := pp.Compression.Settings()
		s.Compression = &c
	}
	if sat := pp.Saturation; sat != nil {
		s.Saturation = audioprofile.SaturationSettings{DriveDB: sat.Drive, Bias: sat.Bias, OutputDB: sat.Output, Stages: sat.Stages}
	}
	if m := pp.Multiband; m != nil {
		ms := &audioprofile.MultibandSettings{Crossovers: append([]float64(nil), m.Crossovers...)}
		for _, c := range m.Bands {
			ms.Bands = append(ms.Bands, c.Settings())
		}
		s.Multiband = ms
	}
	s.Width = pp.Width
	if l := pp.Limiter; l != nil {
		s.Limiter = &audioprofile.LimiterSettings{CeilingDB: l.Ceiling, LookaheadMs: l.Lookahead, ReleaseMs: l.Release}
	}
	return s, nil
}

// AmpFrom convertit les réglages d'un ampli.
func AmpFrom(s audioprofile.TubeAmpSettings) *Amp {
	return &Amp{s.DriveDB, s.Stages, s.Bias, s.BassDB, s.MidDB, s.TrebleDB, s.Cabinet, s.OutputDB}
}

// Settings convertit vers la bibliothèque.
func (a *Amp) Settings() audioprofile.TubeAmpSettings {
	return audioprofile.TubeAmpSettings{DriveDB: a.Drive, Stages: a.Stages, Bias: a.Bias, BassDB: a.Bass, MidDB: a.Mid, TrebleDB: a.Treble, Cabinet: a.Cabinet, OutputDB: a.Output}
}
