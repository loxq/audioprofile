package fx

// Plages utiles des curseurs, unités et types des paramètres, appliquées aux définitions au chargement. Un test
// vérifie qu'aucun paramètre n'y manque et que les défauts et les extrémités des plages sont acceptés par la
// validation de l'effet.

// bound donne les bornes du curseur d'un paramètre d'effet.
type bound struct {
	lo, hi float64
	log    bool
}

// fxBounds : plage utile de chaque paramètre.
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

func init() {
	for i := range defs {
		for j := range defs[i].Params {
			p := &defs[i].Params[j]
			b := fxBounds[defs[i].Name][p.Key]
			p.Min, p.Max, p.Log = b.lo, b.hi, b.log
			p.Unit = fxUnit(defs[i].Name, p.Key)
			p.Int = p.Key == "order" || p.Key == "voices" || p.Key == "bits"
			p.Bool = defs[i].Name == "delay" && p.Key == "pingpong"
		}
	}
}
