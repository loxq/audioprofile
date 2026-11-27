// Package fx est le registre des effets de la bibliothèque, par nom : « delay:time=300,feedback=0.4 » devient un
// effet construit et vérifié. C'est ce qui permet de composer une chaîne à partir d'un texte, d'un fichier de réglages
// ou d'une interface, sans connaître les structures de réglages de chaque effet.
//
// Chaque effet a un nom, des paramètres numériques (valeur par défaut, bornes utiles pour un curseur, unité) et une
// validation. Les bornes de Param servent à construire des curseurs ; la validation d'un effet (Effect.Validate) est
// la seule qui fasse foi.
package fx

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/loxq/audioprofile"
)

// Param décrit un paramètre d'effet.
type Param struct {
	Key      string
	Default  float64
	Required bool // sans valeur par défaut : à fournir
	Doc      string

	// Plage utile d'un curseur (pas une règle de validation), échelle et unité.
	Min, Max float64
	Log      bool // échelle logarithmique (fréquences, durées)
	Int      bool // nombre entier
	Bool     bool // 0 ou 1
	Unit     string
}

// Def décrit un effet du registre.
type Def struct {
	Name   string
	Doc    string
	Params []Param
	// check valide les valeurs (toutes présentes : les défauts sont déjà remplis) pour une
	// fréquence d'échantillonnage et un nombre de canaux donnés.
	check func(v map[string]float64, rate, channels int) error
	// build construit l'effet.
	build func(v map[string]float64) func(src io.Reader, rate, channels int) io.Reader
}

// defs est la liste des effets, dans l'ordre d'affichage de Listing.
var defs = []Def{
	{
		Name:   "hpf",
		Doc:    "filtre passe-haut de Butterworth (−3 dB à freq, 6 dB par octave et par ordre)",
		Params: []Param{{Key: "freq", Default: 0, Required: true, Doc: "fréquence de coupure en Hz"}, {Key: "order", Default: 2, Required: false, Doc: "ordre du filtre, de 1 à 8"}},
		check:  checkFilter,
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.HighPassFilter(src, rate, channels, v["freq"], int(v["order"]))
			}
		},
	},
	{
		Name:   "lpf",
		Doc:    "filtre passe-bas de Butterworth (−3 dB à freq, 6 dB par octave et par ordre)",
		Params: []Param{{Key: "freq", Default: 0, Required: true, Doc: "fréquence de coupure en Hz"}, {Key: "order", Default: 2, Required: false, Doc: "ordre du filtre, de 1 à 8"}},
		check:  checkFilter,
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.LowPassFilter(src, rate, channels, v["freq"], int(v["order"]))
			}
		},
	},
	{
		Name: "parcomp",
		Doc:  "compression parallèle : un signal très comprimé (et compensé) mélangé au signal sec, pour plus de densité en gardant les transitoires",
		Params: []Param{
			{Key: "threshold", Default: -30, Required: false, Doc: "seuil du compresseur en dBFS, de -80 à 0"},
			{Key: "ratio", Default: 8, Required: false, Doc: "rapport de compression, de 1 à 100"},
			{Key: "attack", Default: 10, Required: false, Doc: "attaque en ms, de 0,1 à 1000"},
			{Key: "release", Default: 120, Required: false, Doc: "relâchement en ms, de 1 à 5000"},
			{Key: "makeup", Default: 16, Required: false, Doc: "compensation du comprimé en dB, de 0 à 24"},
			{Key: "mix", Default: 0.3, Required: false, Doc: "part du comprimé, de 0 (sec) à 1"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, c := range []struct {
				key    string
				lo, hi float64
			}{{"threshold", -80, 0}, {"ratio", 1, 100}, {"attack", 0.1, 1000}, {"release", 1, 5000}, {"makeup", 0, 24}, {"mix", 0, 1}} {
				if v[c.key] < c.lo || v[c.key] > c.hi {
					return fmt.Errorf("%s doit être dans [%g, %g] (reçu %g)", c.key, c.lo, c.hi, v[c.key])
				}
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.ParallelCompressor(src, rate, channels, audioprofile.ParallelCompressionSettings{
					Compressor: audioprofile.CompressorSettings{ThresholdDB: v["threshold"], Ratio: v["ratio"], AttackMs: v["attack"], ReleaseMs: v["release"], KneeDB: 6, MakeupDB: v["makeup"]},
					Mix:        v["mix"]})
			}
		},
	},
	{
		Name: "lofi",
		Doc:  "lo-fi : un vieux support (bande passante réduite, saturation de bande, wow et flutter, quantification, craquements de vinyle, souffle) ; 0 désactive une partie",
		Params: []Param{
			{Key: "lowcut", Default: 100, Required: false, Doc: "passe-haut en Hz (0 : aucun), de 10 à Nyquist"},
			{Key: "highcut", Default: 5000, Required: false, Doc: "passe-bas en Hz (0 : aucun), de 100 à Nyquist"},
			{Key: "bits", Default: 12, Required: false, Doc: "quantification sur 1 à 15 bits (0 ou 16 : aucune)"},
			{Key: "samplerate", Default: 0, Required: false, Doc: "fréquence d'échantillonnage réduite en Hz, de 1000 à celle du flux (0 : aucune)"},
			{Key: "wobble", Default: 0.4, Required: false, Doc: "wow et flutter, de 0 à 1"},
			{Key: "drive", Default: 4, Required: false, Doc: "saturation de bande en dB, de 0 à 24"},
			{Key: "crackle", Default: 0.3, Required: false, Doc: "craquements de vinyle, de 0 à 1"},
			{Key: "hiss", Default: 0.2, Required: false, Doc: "souffle de bande, de 0 à 1 (1 = -40 dBFS)"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			nyq := float64(rate) / 2
			if v["lowcut"] != 0 && (v["lowcut"] < 10 || v["lowcut"] >= nyq) {
				return fmt.Errorf("lowcut doit valoir 0 ou être dans [10, %g[ Hz (reçu %g)", nyq, v["lowcut"])
			}
			if v["highcut"] != 0 && (v["highcut"] < 100 || v["highcut"] >= nyq) {
				return fmt.Errorf("highcut doit valoir 0 ou être dans [100, %g[ Hz (reçu %g)", nyq, v["highcut"])
			}
			if b := v["bits"]; b != math.Trunc(b) || b < 0 || b > 16 {
				return fmt.Errorf("bits doit être un entier de 0 à 16 (reçu %g)", b)
			}
			if sr := v["samplerate"]; sr != 0 && (sr < 1000 || sr >= float64(rate)) {
				return fmt.Errorf("samplerate doit valoir 0 ou être dans [1000, %d[ Hz (reçu %g)", rate, sr)
			}
			for _, k := range []string{"wobble", "crackle", "hiss"} {
				if v[k] < 0 || v[k] > 1 {
					return fmt.Errorf("%s doit être dans [0, 1] (reçu %g)", k, v[k])
				}
			}
			if v["drive"] < 0 || v["drive"] > 24 {
				return fmt.Errorf("drive doit être dans [0, 24] dB (reçu %g)", v["drive"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.LoFi(src, rate, channels, audioprofile.LoFiSettings{
					LowCutHz: v["lowcut"], HighCutHz: v["highcut"], Bits: int(v["bits"]), SampleRateHz: v["samplerate"],
					Wobble: v["wobble"], DriveDB: v["drive"], Crackle: v["crackle"], Hiss: v["hiss"]})
			}
		},
	},
	{
		Name: "chorus",
		Doc:  "chorus : plusieurs copies légèrement retardées dont le retard oscille (épaissit, élargit en stéréo)",
		Params: []Param{
			{Key: "delay", Default: 18, Required: false, Doc: "retard moyen en ms, de 1 à 50"},
			{Key: "depth", Default: 3, Required: false, Doc: "amplitude de la modulation en ms, de 0,01 à 49,5 (jamais plus que delay - 0,5)"},
			{Key: "rate", Default: 0.8, Required: false, Doc: "vitesse de la modulation en Hz, de 0,05 à 10"},
			{Key: "voices", Default: 3, Required: false, Doc: "nombre de voix, de 1 à 6"},
			{Key: "mix", Default: 0.5, Required: false, Doc: "niveau des voix, de 0 (sans effet) à 1"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, c := range []struct {
				key    string
				lo, hi float64
			}{{"delay", 1, 50}, {"depth", 0.01, 49.5}, {"rate", 0.05, 10}, {"voices", 1, 6}, {"mix", 0, 1}} {
				if v[c.key] < c.lo || v[c.key] > c.hi {
					return fmt.Errorf("%s doit être dans [%g, %g] (reçu %g)", c.key, c.lo, c.hi, v[c.key])
				}
			}
			if v["voices"] != math.Trunc(v["voices"]) {
				return fmt.Errorf("voices doit être un entier (reçu %g)", v["voices"])
			}
			if v["depth"] > v["delay"]-0.5 {
				return fmt.Errorf("depth (%g) ne doit pas dépasser delay - 0,5 (%g)", v["depth"], v["delay"]-0.5)
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Chorus(src, rate, channels, audioprofile.ChorusSettings{DelayMs: v["delay"], DepthMs: v["depth"], RateHz: v["rate"], Voices: int(v["voices"]), Mix: v["mix"]})
			}
		},
	},
	{
		Name: "flanger",
		Doc:  "flanger : une copie très courte dont le retard oscille, avec rétroaction (le « jet d'avion »)",
		Params: []Param{
			{Key: "delay", Default: 2, Required: false, Doc: "retard moyen en ms, de 0,3 à 10"},
			{Key: "depth", Default: 1.5, Required: false, Doc: "amplitude de la modulation en ms, de 0,01 à 9,8 (jamais plus que delay - 0,2)"},
			{Key: "rate", Default: 0.25, Required: false, Doc: "vitesse de la modulation en Hz, de 0,05 à 10"},
			{Key: "feedback", Default: 0.5, Required: false, Doc: "rétroaction, de -0,95 à 0,95"},
			{Key: "mix", Default: 0.5, Required: false, Doc: "niveau du signal retardé, de 0 (sans effet) à 1"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, c := range []struct {
				key    string
				lo, hi float64
			}{{"delay", 0.3, 10}, {"depth", 0.01, 9.8}, {"rate", 0.05, 10}, {"feedback", -0.95, 0.95}, {"mix", 0, 1}} {
				if v[c.key] < c.lo || v[c.key] > c.hi {
					return fmt.Errorf("%s doit être dans [%g, %g] (reçu %g)", c.key, c.lo, c.hi, v[c.key])
				}
			}
			if v["depth"] > v["delay"]-0.2 {
				return fmt.Errorf("depth (%g) ne doit pas dépasser delay - 0,2 (%g)", v["depth"], v["delay"]-0.2)
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Flanger(src, rate, channels, audioprofile.FlangerSettings{DelayMs: v["delay"], DepthMs: v["depth"], RateHz: v["rate"], Feedback: v["feedback"], Mix: v["mix"]})
			}
		},
	},
	{
		Name: "delay",
		Doc:  "delay (écho) : échos du signal, avec rétroaction, amortissement des aigus et ping-pong stéréo ; la queue est coupée à la fin du flux",
		Params: []Param{
			{Key: "time", Default: 350, Required: false, Doc: "durée entre deux échos en ms, de 1 à 2000"},
			{Key: "feedback", Default: 0.4, Required: false, Doc: "part réinjectée à chaque écho, de 0 à 0,95"},
			{Key: "mix", Default: 0.3, Required: false, Doc: "niveau des échos, de 0 (sans effet) à 1"},
			{Key: "damping", Default: 0, Required: false, Doc: "passe-bas dans la boucle en Hz (0 : aucun) : chaque écho est plus sombre"},
			{Key: "pingpong", Default: 0, Required: false, Doc: "1 : les échos rebondissent entre gauche et droite (stéréo), 0 : non"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			if v["time"] < 1 || v["time"] > 2000 {
				return fmt.Errorf("time doit être dans [1, 2000] ms (reçu %g)", v["time"])
			}
			if v["feedback"] < 0 || v["feedback"] > 0.95 {
				return fmt.Errorf("feedback doit être dans [0, 0.95] (reçu %g)", v["feedback"])
			}
			if v["mix"] < 0 || v["mix"] > 1 {
				return fmt.Errorf("mix doit être dans [0, 1] (reçu %g)", v["mix"])
			}
			if v["damping"] != 0 && (v["damping"] < 20 || v["damping"] >= float64(rate)/2) {
				return fmt.Errorf("damping doit valoir 0 ou être dans [20, %g[ Hz (reçu %g)", float64(rate)/2, v["damping"])
			}
			if v["pingpong"] != 0 && v["pingpong"] != 1 {
				return fmt.Errorf("pingpong doit valoir 0 ou 1 (reçu %g)", v["pingpong"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Delay(src, rate, channels, audioprofile.DelaySettings{
					TimeMs: v["time"], Feedback: v["feedback"], Mix: v["mix"], DampingHz: v["damping"], PingPong: v["pingpong"] == 1})
			}
		},
	},
	{
		Name: "crossfeed",
		Doc:  "crossfeed pour casque : mélange un peu de chaque canal dans l'autre aux graves (stéréo seulement), milieu conservé",
		Params: []Param{
			{Key: "strength", Default: 0.6, Required: false, Doc: "force, de 0 (sans effet) à 1 (graves presque mono)"},
			{Key: "cutoff", Default: 700, Required: false, Doc: "fréquence en Hz sous laquelle les canaux se mélangent"},
			{Key: "delay", Default: 0, Required: false, Doc: "retard du signal croisé en ms, de 0 à 1 (0,25 donne de la profondeur mais colore le centre)"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			if v["strength"] < 0 || v["strength"] > 1 {
				return fmt.Errorf("strength doit être dans [0, 1] (reçu %g)", v["strength"])
			}
			if !(v["cutoff"] > 0) || v["cutoff"] >= float64(rate)/2 {
				return fmt.Errorf("cutoff doit être dans ]0, %g[ Hz (reçu %g)", float64(rate)/2, v["cutoff"])
			}
			if v["delay"] < 0 || v["delay"] > 1 {
				return fmt.Errorf("delay doit être dans [0, 1] ms (reçu %g)", v["delay"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Crossfeed(src, rate, channels, audioprofile.CrossfeedSettings{Strength: v["strength"], CutoffHz: v["cutoff"], DelayMs: v["delay"]})
			}
		},
	},
	{
		Name: "notch",
		Doc:  "filtre coupe-bande étroit (zéro exact à freq, largeur à -3 dB de freq/q) : retire un ronflement à 50 ou 60 Hz, puis ses harmoniques avec d'autres -fx notch",
		Params: []Param{
			{Key: "freq", Default: 0, Required: true, Doc: "fréquence supprimée en Hz"},
			{Key: "q", Default: 10, Required: false, Doc: "facteur de qualité, de 0,1 à 1000 : plus il est grand, plus la bande est étroite"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			if !(v["freq"] > 0) || v["freq"] >= float64(rate)/2 {
				return fmt.Errorf("freq doit être dans ]0, %g[ Hz (reçu %g)", float64(rate)/2, v["freq"])
			}
			if v["q"] < 0.1 || v["q"] > 1000 {
				return fmt.Errorf("q doit être dans [0.1, 1000] (reçu %g)", v["q"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.NotchFilter(src, rate, channels, v["freq"], v["q"])
			}
		},
	},
	{
		Name: "gate",
		Doc:  "gate / expandeur descendant : réduit les passages sous le seuil (bruit de fond entre les phrases ou les notes) ; ratio 100 = gate franc",
		Params: []Param{
			{Key: "threshold", Default: -50, Required: false, Doc: "seuil en dBFS, de -90 à 0 : en dessous, le signal est réduit"},
			{Key: "ratio", Default: 4, Required: false, Doc: "rapport d'expansion, de 1 (sans effet) à 100 (gate)"},
			{Key: "range", Default: 30, Required: false, Doc: "réduction maximale en dB, de 1 à 90"},
			{Key: "attack", Default: 2, Required: false, Doc: "ouverture en ms, de 0,05 à 500"},
			{Key: "release", Default: 120, Required: false, Doc: "fermeture en ms, de 1 à 5000"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, c := range []struct {
				key    string
				lo, hi float64
			}{{"threshold", -90, 0}, {"ratio", 1, 100}, {"range", 1, 90}, {"attack", 0.05, 500}, {"release", 1, 5000}} {
				if v[c.key] < c.lo || v[c.key] > c.hi {
					return fmt.Errorf("%s doit être dans [%g, %g] (reçu %g)", c.key, c.lo, c.hi, v[c.key])
				}
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Gate(src, rate, channels, audioprofile.GateSettings{ThresholdDB: v["threshold"], Ratio: v["ratio"], RangeDB: v["range"], AttackMs: v["attack"], ReleaseMs: v["release"]})
			}
		},
	},
	{
		Name: "deess",
		Doc:  "de-esser : atténue les sifflantes (au-dessus de freq) quand elles dépassent le seuil ; le seuil est absolu, à régler pour chaque source",
		Params: []Param{
			{Key: "freq", Default: 5000, Required: false, Doc: "fréquence de séparation en Hz : ce qui est au-dessus est surveillé"},
			{Key: "threshold", Default: -30, Required: false, Doc: "seuil en dBFS, de -80 à 0"},
			{Key: "ratio", Default: 4, Required: false, Doc: "rapport de compression de la bande haute, de 1 (sans effet) à 100"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			if !(v["freq"] > 0) || v["freq"] >= float64(rate)/2 {
				return fmt.Errorf("freq doit être dans ]0, %g[ Hz (reçu %g)", float64(rate)/2, v["freq"])
			}
			if v["threshold"] < -80 || v["threshold"] > 0 {
				return fmt.Errorf("threshold doit être dans [-80, 0] dBFS (reçu %g)", v["threshold"])
			}
			if v["ratio"] < 1 || v["ratio"] > 100 {
				return fmt.Errorf("ratio doit être dans [1, 100] (reçu %g)", v["ratio"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.DeEsser(src, rate, channels, audioprofile.DeEsserSettings{FreqHz: v["freq"], ThresholdDB: v["threshold"], Ratio: v["ratio"]})
			}
		},
	},
	{
		Name: "exciter",
		Doc:  "exciter : ajoute des harmoniques aux aigus (au-dessus de freq) ; elles n'apparaissent que si les aigus sont assez forts, augmentez drive pour en avoir sur des aigus discrets",
		Params: []Param{
			{Key: "freq", Default: 3000, Required: false, Doc: "fréquence de séparation en Hz : seul ce qui est au-dessus est enrichi"},
			{Key: "amount", Default: 0.3, Required: false, Doc: "part d'harmoniques ajoutée, de 0 (sans effet) à 1"},
			{Key: "drive", Default: 20, Required: false, Doc: "gain poussé dans la saturation en dB, de 3 à 24"},
			{Key: "warmth", Default: 0, Required: false, Doc: "asymétrie, de 0 (harmoniques impaires) à 1 (paires en plus)"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			if !(v["freq"] > 0) || v["freq"] >= float64(rate)/2 {
				return fmt.Errorf("freq doit être dans ]0, %g[ Hz (reçu %g)", float64(rate)/2, v["freq"])
			}
			for _, k := range []string{"amount", "warmth"} {
				if v[k] < 0 || v[k] > 1 {
					return fmt.Errorf("%s doit être dans [0, 1] (reçu %g)", k, v[k])
				}
			}
			if v["drive"] < 3 || v["drive"] > 24 {
				return fmt.Errorf("drive doit être dans [3, 24] dB (reçu %g)", v["drive"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Exciter(src, rate, channels, audioprofile.ExciterSettings{FreqHz: v["freq"], Amount: v["amount"], DriveDB: v["drive"], Warmth: v["warmth"]})
			}
		},
	},
	{
		Name: "reverb",
		Doc:  "réverbération (Freeverb) : la queue est coupée à la fin du flux, ajoutez du silence pour la laisser mourir",
		Params: []Param{
			{Key: "room", Default: 0.5, Required: false, Doc: "taille de la pièce, de 0 (petite) à 1 (grande)"},
			{Key: "damping", Default: 0.5, Required: false, Doc: "absorption des aigus, de 0 à 1"},
			{Key: "width", Default: 1, Required: false, Doc: "largeur stéréo de la réverbération, de 0 (mono) à 1"},
			{Key: "predelay", Default: 0, Required: false, Doc: "retard avant la réverbération, de 0 à 200 ms"},
			{Key: "mix", Default: 0.25, Required: false, Doc: "part de réverbération, de 0 (sec) à 1"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, k := range []string{"room", "damping", "width", "mix"} {
				if v[k] < 0 || v[k] > 1 {
					return fmt.Errorf("%s doit être dans [0, 1] (reçu %g)", k, v[k])
				}
			}
			if v["predelay"] < 0 || v["predelay"] > 200 {
				return fmt.Errorf("predelay doit être dans [0, 200] ms (reçu %g)", v["predelay"])
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.Reverb(src, rate, channels, audioprofile.ReverbSettings{
					RoomSize: v["room"], Damping: v["damping"], Width: v["width"], PreDelayMs: v["predelay"], Mix: v["mix"]})
			}
		},
	},
	{
		Name: "transient",
		Doc:  "renforçateur de transitoires : accentue ou adoucit l'attaque et la queue des sons, sans seuil",
		Params: []Param{
			{Key: "attack", Default: 0, Required: false, Doc: "attaque, de -1 (aplatie) à 1 (marquée)"},
			{Key: "sustain", Default: 0, Required: false, Doc: "queue des sons, de -1 (raccourcie) à 1 (allongée)"},
		},
		check: func(v map[string]float64, rate, channels int) error {
			for _, k := range []string{"attack", "sustain"} {
				if v[k] < -1 || v[k] > 1 {
					return fmt.Errorf("%s doit être dans [-1, 1] (reçu %g)", k, v[k])
				}
			}
			return nil
		},
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.TransientShaper(src, rate, channels, audioprofile.TransientSettings{Attack: v["attack"], Sustain: v["sustain"]})
			}
		},
	},
}

func checkFilter(v map[string]float64, rate, channels int) error {
	if !(v["freq"] > 0) || v["freq"] >= float64(rate)/2 {
		return fmt.Errorf("freq doit être dans ]0, %g[ Hz, la moitié de la fréquence d'échantillonnage (reçu %g)", float64(rate)/2, v["freq"])
	}
	if o := v["order"]; o != math.Trunc(o) || o < 1 || o > 8 {
		return fmt.Errorf("order doit être un entier de 1 à 8 (reçu %g)", o)
	}
	return nil
}

// Defs renvoie les effets du registre, dans l'ordre d'affichage. La liste est partagée : ne la modifiez pas.
func Defs() []Def { return defs }

// Lookup renvoie l'effet de ce nom (insensible à la casse), ou nil.
func Lookup(name string) *Def {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}

// Names renvoie les noms des effets, dans l'ordre du registre.
func Names() []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// Effect est un effet choisi, avec toutes ses valeurs : prêt à être validé puis appliqué.
type Effect struct {
	Spec string // texte d'origine (« delay:time=300 »), ou le nom si construit par New
	Def  *Def
	Vals map[string]float64 // une valeur pour chaque paramètre
}

// New construit un effet à partir de valeurs nommées. Les paramètres absents prennent leur valeur dans defaults
// (par nom d'effet, puis de paramètre ; nil : aucune) puis leur valeur par défaut ; un paramètre obligatoire que
// personne ne donne est une erreur. Les noms inconnus et les valeurs non finies sont refusés. Les bornes (Validate)
// ne sont pas vérifiées ici.
func New(name string, vals map[string]float64, defaults map[string]map[string]float64) (Effect, error) {
	def := Lookup(name)
	if def == nil {
		return Effect{}, fmt.Errorf("fx : effet inconnu %q (effets : %s)", strings.TrimSpace(name), strings.Join(Names(), ", "))
	}
	known := map[string]bool{}
	var valid []string
	for _, p := range def.Params {
		known[p.Key] = true
		valid = append(valid, p.Key)
	}
	out := map[string]float64{}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := vals[k]
		if !known[k] {
			return Effect{}, fmt.Errorf("fx %s : paramètre inconnu %q (paramètres : %s)", def.Name, k, strings.Join(valid, ", "))
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return Effect{}, fmt.Errorf("fx %s : %s=%v n'est pas un nombre fini", def.Name, k, v)
		}
		out[k] = v
	}
	for _, p := range def.Params {
		if _, ok := out[p.Key]; ok {
			continue
		}
		if v, ok := defaults[def.Name][p.Key]; ok { // valeur par défaut du fichier de configuration
			out[p.Key] = v
			continue
		}
		if p.Required {
			return Effect{}, fmt.Errorf("fx %s : paramètre %q obligatoire (exemple : %s:%s=...)", def.Name, p.Key, def.Name, p.Key)
		}
		out[p.Key] = p.Default
	}
	return Effect{Spec: def.Name, Def: def, Vals: out}, nil
}

// Parse analyse « nom » ou « nom:cle=valeur,cle=valeur ». defaults donne, par effet, des valeurs par défaut
// qui remplacent celles du code (nil : celles du code).
func Parse(spec string, defaults map[string]map[string]float64) (Effect, error) {
	spec = strings.TrimSpace(spec)
	name, rest, hasParams := strings.Cut(spec, ":")
	name = strings.ToLower(strings.TrimSpace(name))
	if Lookup(name) == nil {
		return Effect{}, fmt.Errorf("fx : effet inconnu %q (effets : %s)", name, strings.Join(Names(), ", "))
	}
	vals := map[string]float64{}
	if hasParams && strings.TrimSpace(rest) != "" {
		for _, kv := range strings.Split(rest, ",") {
			k, v, ok := strings.Cut(kv, "=")
			k = strings.ToLower(strings.TrimSpace(k))
			if !ok {
				return Effect{}, fmt.Errorf("fx %s : %q n'est pas de la forme cle=valeur", name, strings.TrimSpace(kv))
			}
			if _, dup := vals[k]; dup {
				return Effect{}, fmt.Errorf("fx %s : paramètre %q donné deux fois", name, k)
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return Effect{}, fmt.Errorf("fx %s : %s=%q n'est pas un nombre fini", name, k, strings.TrimSpace(v))
			}
			vals[k] = f
		}
	}
	e, err := New(name, vals, defaults)
	if err != nil {
		return Effect{}, err
	}
	e.Spec = spec
	return e, nil
}

// Validate vérifie les valeurs pour une fréquence d'échantillonnage et un nombre de canaux donnés.
func (e Effect) Validate(rate, channels int) error {
	if e.Def.check == nil {
		return nil
	}
	if err := e.Def.check(e.Vals, rate, channels); err != nil {
		return fmt.Errorf("fx %s : %w", e.Def.Name, err)
	}
	return nil
}

// Apply enveloppe src dans l'effet. Les valeurs doivent avoir été validées.
func (e Effect) Apply(src io.Reader, rate, channels int) io.Reader {
	return e.Def.build(e.Vals)(src, rate, channels)
}

// Listing décrit les effets et leurs paramètres, en texte (pour une option -list-fx, par exemple).
func Listing() string {
	var b strings.Builder
	for _, d := range defs {
		fmt.Fprintf(&b, "%-10s %s\n", d.Name, d.Doc)
		for _, p := range d.Params {
			req := fmt.Sprintf("défaut %g", p.Default)
			if p.Required {
				req = "obligatoire"
			}
			fmt.Fprintf(&b, "             %-10s %s (%s)\n", p.Key, p.Doc, req)
		}
	}
	return b.String()
}
