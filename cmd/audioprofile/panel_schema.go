package main

import (
	"math"

	"github.com/loxq/audioprofile"
)

// Le schéma décrit au panneau de réglage (-serve) les paramètres : libellé, bornes du curseur, pas, échelle.
// Les bornes d'un curseur sont celles où le réglage a du sens ; la validation, elle, reste celle du serveur (une valeur
// tapée hors du curseur est acceptée si le serveur l'accepte).

// fieldSpec décrit un paramètre numérique (ou booléen).
type fieldSpec struct {
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

type fxSchema struct {
	Name   string      `json:"name"`
	Doc    string      `json:"doc"`
	Params []fieldSpec `json:"params"`
}

type panelSchema struct {
	Rate       int         `json:"rate"`
	Channels   int         `json:"channels"`
	Intensity  fieldSpec   `json:"intensity"`
	Width      fieldSpec   `json:"width"`
	BandTypes  []string    `json:"bandTypes"`
	Band       []fieldSpec `json:"band"`
	Compressor []fieldSpec `json:"compressor"`
	Saturation []fieldSpec `json:"saturation"`
	Limiter    []fieldSpec `json:"limiter"`
	Amp        []fieldSpec `json:"amp"`
	FX         []fxSchema  `json:"fx"`
	Profiles   []string    `json:"profiles"`
	Amps       []string    `json:"amps"`
	// Defaults : valeurs d'un étage qu'on active alors que le profil n'en a pas.
	Defaults struct {
		Compression *panelComp    `json:"compression"`
		Saturation  *panelSat     `json:"saturation"`
		Multiband   *panelMulti   `json:"multiband"`
		Limiter     *panelLimiter `json:"limiter"`
	} `json:"defaults"`
}

// bound donne les bornes du curseur d'un paramètre d'effet.
type bound struct {
	lo, hi float64
	log    bool
}

// fxBounds : bornes des curseurs des paramètres de -fx. Un test vérifie qu'aucun paramètre n'y manque et que
// les défauts et les extrémités des curseurs sont acceptés par la validation de l'effet.
var fxBounds = map[string]map[string]bound{
	"hpf":       {"freq": {20, 2000, true}, "order": {1, 8, false}},
	"lpf":       {"freq": {200, 20000, true}, "order": {1, 8, false}},
	"parcomp":   {"threshold": {-80, 0, false}, "ratio": {1, 30, false}, "attack": {0.1, 1000, true}, "release": {1, 5000, true}, "makeup": {0, 24, false}, "mix": {0, 1, false}},
	"lofi":      {"lowcut": {0, 2000, false}, "highcut": {0, 20000, false}, "bits": {0, 16, false}, "samplerate": {0, 44100, false}, "wobble": {0, 1, false}, "drive": {0, 24, false}, "crackle": {0, 1, false}, "hiss": {0, 1, false}},
	"chorus":    {"delay": {1, 50, false}, "depth": {0.01, 20, false}, "rate": {0.05, 10, true}, "voices": {1, 6, false}, "mix": {0, 1, false}},
	"flanger":   {"delay": {0.3, 10, false}, "depth": {0.01, 9.8, false}, "rate": {0.05, 10, true}, "feedback": {-0.95, 0.95, false}, "mix": {0, 1, false}},
	"delay":     {"time": {1, 2000, true}, "feedback": {0, 0.95, false}, "mix": {0, 1, false}, "damping": {0, 20000, false}, "pingpong": {0, 1, false}},
	"crossfeed": {"strength": {0, 1, false}, "cutoff": {100, 2000, true}, "delay": {0, 1, false}},
	"notch":     {"freq": {20, 20000, true}, "q": {0.1, 1000, true}},
	"gate":      {"threshold": {-90, 0, false}, "ratio": {1, 30, false}, "range": {1, 90, false}, "attack": {0.05, 500, true}, "release": {1, 5000, true}},
	"deess":     {"freq": {2000, 12000, true}, "threshold": {-80, 0, false}, "ratio": {1, 30, false}},
	"exciter":   {"freq": {500, 12000, true}, "amount": {0, 1, false}, "drive": {3, 24, false}, "warmth": {0, 1, false}},
	"reverb":    {"room": {0, 1, false}, "damping": {0, 1, false}, "width": {0, 1, false}, "predelay": {0, 200, false}, "mix": {0, 1, false}},
	"transient": {"attack": {-1, 1, false}, "sustain": {-1, 1, false}},
}

// fxUnits : unité affichée, par effet et paramètre (le reste n'en a pas).
var fxUnits = map[string]string{
	"freq": "Hz", "lowcut": "Hz", "highcut": "Hz", "samplerate": "Hz", "cutoff": "Hz", "damping@delay": "Hz",
	"threshold": "dB", "makeup": "dB", "drive": "dB", "range": "dB",
	"attack@parcomp": "ms", "release": "ms", "delay": "ms", "depth": "ms", "time": "ms", "predelay": "ms", "rate": "Hz",
	"attack@gate": "ms", "attack@deess": "ms",
}

func fxUnit(fx, key string) string {
	if u, ok := fxUnits[key+"@"+fx]; ok {
		return u
	}
	if key == "delay" && fx == "crossfeed" {
		return "ms"
	}
	return fxUnits[key]
}

// stepFor choisit un pas de curseur lisible pour une plage.
func stepFor(lo, hi float64, integer bool) float64 {
	if integer {
		return 1
	}
	span := hi - lo
	switch {
	case span <= 2:
		return 0.01
	case span <= 30:
		return 0.1
	case span <= 300:
		return 0.5
	}
	return 1
}

func num(key, label, unit string, lo, hi, def float64, opts ...string) fieldSpec {
	f := fieldSpec{Key: key, Label: label, Unit: unit, Min: lo, Max: hi, Def: def}
	for _, o := range opts {
		switch o {
		case "log":
			f.Log = true
		case "int":
			f.Int = true
		}
	}
	f.Step = stepFor(lo, hi, f.Int)
	if f.Log { // pas fin : la position du curseur donne déjà la progression
		f.Step = 0
	}
	return f
}

// newPanelSchema construit le schéma pour une fréquence d'échantillonnage et des profils donnés.
func newPanelSchema(rate, channels int, cfg *config) panelSchema {
	nyq := float64(rate) / 2
	fmax := math.Min(20000, math.Floor(nyq*0.95))
	s := panelSchema{Rate: rate, Channels: channels}
	s.Intensity = num("intensity", "Intensité", "", 0, audioprofile.MaxIntensity, 1)
	s.Width = num("width", "Largeur stéréo", "×", 0, audioprofile.MaxWidth, 1)
	s.BandTypes = bandTypeOrder
	s.Band = []fieldSpec{
		num("freq", "Fréquence", "Hz", 20, fmax, 1000, "log"),
		num("gain", "Gain", "dB", -24, 24, 0),
		num("q", "Q", "", 0.1, 20, 1, "log"),
		num("order", "Ordre", "", 1, 8, 2, "int"),
	}
	s.Compressor = []fieldSpec{
		num("threshold", "Seuil", "dBFS", -80, 0, -20),
		num("ratio", "Ratio", ":1", 1, 20, 2),
		num("attack", "Attaque", "ms", 0.1, 1000, 10, "log"),
		num("release", "Relâchement", "ms", 1, 5000, 100, "log"),
		num("knee", "Genou", "dB", 0, 24, 6),
		num("makeup", "Compensation", "dB", -12, 24, 0),
	}
	s.Saturation = []fieldSpec{
		num("drive", "Drive", "dB", 0, 36, 3),
		num("bias", "Asymétrie", "", 0, 1, 0),
		num("output", "Sortie", "dB", -24, 24, 0),
		num("stages", "Étages", "", 0, 4, 1, "int"),
	}
	s.Limiter = []fieldSpec{
		num("ceiling", "Plafond", "dBFS", -40, 0, -1),
		num("lookahead", "Anticipation", "ms", 0.1, 50, 5, "log"),
		num("release", "Relâchement", "ms", 1, 1000, 100, "log"),
	}
	s.Amp = []fieldSpec{
		num("drive", "Drive", "dB", 0, 36, 6),
		num("stages", "Étages", "", 1, 4, 2, "int"),
		num("bias", "Asymétrie", "", 0, 1, 0.2),
		num("bass", "Basses", "dB", -12, 12, 0),
		num("mid", "Médiums", "dB", -12, 12, 0),
		num("treble", "Aigus", "dB", -12, 12, 0),
		num("output", "Sortie", "dB", -24, 12, 0),
	}
	for _, d := range fxDefs {
		fs := fxSchema{Name: d.name, Doc: d.doc}
		for _, p := range d.params {
			b := fxBounds[d.name][p.key]
			f := num(p.key, p.key, fxUnit(d.name, p.key), b.lo, b.hi, p.def)
			f.Log = b.log
			f.Int = isIntParam(d.name, p.key)
			f.Bool = d.name == "delay" && p.key == "pingpong"
			f.Step = stepFor(b.lo, b.hi, f.Int)
			if f.Log {
				f.Step = 0
			}
			f.Required, f.Doc = p.required, p.doc
			if p.required { // un effet ajouté depuis le panneau doit être valide : valeur de départ audible
				f.Def = math.Min(math.Max(1000, b.lo), b.hi)
			}
			fs.Params = append(fs.Params, f)
		}
		s.FX = append(s.FX, fs)
	}
	s.Profiles = cfg.profileNames()
	s.Amps = cfg.ampNames()
	comp := panelCompFrom(compressorStart)
	s.Defaults.Compression = &comp
	s.Defaults.Saturation = &panelSat{Drive: 3, Stages: 1}
	s.Defaults.Limiter = &panelLimiter{Ceiling: -1, Lookahead: 5, Release: 100}
	s.Defaults.Multiband = profileToPanel(audioprofile.RadioNatural.Settings()).Multiband
	return s
}

// isIntParam indique si le paramètre est un nombre entier (voix, ordre, bits).
func isIntParam(fx, key string) bool {
	switch key {
	case "order", "voices", "bits":
		return true
	}
	return false
}

// bandTypeOrder est l'ordre des types de bande dans le sélecteur.
var bandTypeOrder = []string{"lowshelf", "peaking", "highshelf", "highpass", "lowpass", "notch", "gain"}
