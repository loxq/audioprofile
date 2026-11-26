package main

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/loxq/audioprofile"
)

// Les effets de l'option -fx, appliqués après le profil ou l'ampli, dans l'ordre de la ligne de
// commande : -fx nom ou -fx nom:cle=valeur,cle=valeur.

// fxParam décrit un paramètre d'effet.
type fxParam struct {
	key      string
	def      float64
	required bool
	doc      string
}

// fxDef décrit un effet de la liste.
type fxDef struct {
	name   string
	doc    string
	params []fxParam
	// check valide les valeurs (toutes présentes : les défauts sont déjà remplis) pour une
	// fréquence d'échantillonnage et un nombre de canaux donnés.
	check func(v map[string]float64, rate, channels int) error
	// build construit l'effet.
	build func(v map[string]float64) func(src io.Reader, rate, channels int) io.Reader
}

// fxDefs est la liste des effets, dans l'ordre d'affichage de -list-fx.
var fxDefs = []fxDef{
	{
		name:   "hpf",
		doc:    "filtre passe-haut de Butterworth (−3 dB à freq, 6 dB par octave et par ordre)",
		params: []fxParam{{"freq", 0, true, "fréquence de coupure en Hz"}, {"order", 2, false, "ordre du filtre, de 1 à 8"}},
		check:  checkFilter,
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.HighPassFilter(src, rate, channels, v["freq"], int(v["order"]))
			}
		},
	},
	{
		name:   "lpf",
		doc:    "filtre passe-bas de Butterworth (−3 dB à freq, 6 dB par octave et par ordre)",
		params: []fxParam{{"freq", 0, true, "fréquence de coupure en Hz"}, {"order", 2, false, "ordre du filtre, de 1 à 8"}},
		check:  checkFilter,
		build: func(v map[string]float64) func(io.Reader, int, int) io.Reader {
			return func(src io.Reader, rate, channels int) io.Reader {
				return audioprofile.LowPassFilter(src, rate, channels, v["freq"], int(v["order"]))
			}
		},
	},
	{
		name: "parcomp",
		doc:  "compression parallèle : un signal très comprimé (et compensé) mélangé au signal sec, pour plus de densité en gardant les transitoires",
		params: []fxParam{
			{"threshold", -30, false, "seuil du compresseur en dBFS, de -80 à 0"},
			{"ratio", 8, false, "rapport de compression, de 1 à 100"},
			{"attack", 10, false, "attaque en ms, de 0,1 à 1000"},
			{"release", 120, false, "relâchement en ms, de 1 à 5000"},
			{"makeup", 16, false, "compensation du comprimé en dB, de 0 à 24"},
			{"mix", 0.3, false, "part du comprimé, de 0 (sec) à 1"},
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
		name: "delay",
		doc:  "delay (écho) : échos du signal, avec rétroaction, amortissement des aigus et ping-pong stéréo ; la queue est coupée à la fin du flux",
		params: []fxParam{
			{"time", 350, false, "durée entre deux échos en ms, de 1 à 2000"},
			{"feedback", 0.4, false, "part réinjectée à chaque écho, de 0 à 0,95"},
			{"mix", 0.3, false, "niveau des échos, de 0 (sans effet) à 1"},
			{"damping", 0, false, "passe-bas dans la boucle en Hz (0 : aucun) : chaque écho est plus sombre"},
			{"pingpong", 0, false, "1 : les échos rebondissent entre gauche et droite (stéréo), 0 : non"},
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
		name: "crossfeed",
		doc:  "crossfeed pour casque : mélange un peu de chaque canal dans l'autre aux graves (stéréo seulement), milieu conservé",
		params: []fxParam{
			{"strength", 0.6, false, "force, de 0 (sans effet) à 1 (graves presque mono)"},
			{"cutoff", 700, false, "fréquence en Hz sous laquelle les canaux se mélangent"},
			{"delay", 0, false, "retard du signal croisé en ms, de 0 à 1 (0,25 donne de la profondeur mais colore le centre)"},
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
		name: "notch",
		doc:  "filtre coupe-bande étroit (zéro exact à freq, largeur à -3 dB de freq/q) : retire un ronflement à 50 ou 60 Hz, puis ses harmoniques avec d'autres -fx notch",
		params: []fxParam{
			{"freq", 0, true, "fréquence supprimée en Hz"},
			{"q", 10, false, "facteur de qualité, de 0,1 à 1000 : plus il est grand, plus la bande est étroite"},
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
		name: "gate",
		doc:  "gate / expandeur descendant : réduit les passages sous le seuil (bruit de fond entre les phrases ou les notes) ; ratio 100 = gate franc",
		params: []fxParam{
			{"threshold", -50, false, "seuil en dBFS, de -90 à 0 : en dessous, le signal est réduit"},
			{"ratio", 4, false, "rapport d'expansion, de 1 (sans effet) à 100 (gate)"},
			{"range", 30, false, "réduction maximale en dB, de 1 à 90"},
			{"attack", 2, false, "ouverture en ms, de 0,05 à 500"},
			{"release", 120, false, "fermeture en ms, de 1 à 5000"},
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
		name: "deess",
		doc:  "de-esser : atténue les sifflantes (au-dessus de freq) quand elles dépassent le seuil ; le seuil est absolu, à régler pour chaque source",
		params: []fxParam{
			{"freq", 5000, false, "fréquence de séparation en Hz : ce qui est au-dessus est surveillé"},
			{"threshold", -30, false, "seuil en dBFS, de -80 à 0"},
			{"ratio", 4, false, "rapport de compression de la bande haute, de 1 (sans effet) à 100"},
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
		name: "exciter",
		doc:  "exciter : ajoute des harmoniques aux aigus (au-dessus de freq) ; elles n'apparaissent que si les aigus sont assez forts, augmentez drive pour en avoir sur des aigus discrets",
		params: []fxParam{
			{"freq", 3000, false, "fréquence de séparation en Hz : seul ce qui est au-dessus est enrichi"},
			{"amount", 0.3, false, "part d'harmoniques ajoutée, de 0 (sans effet) à 1"},
			{"drive", 20, false, "gain poussé dans la saturation en dB, de 3 à 24"},
			{"warmth", 0, false, "asymétrie, de 0 (harmoniques impaires) à 1 (paires en plus)"},
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
		name: "reverb",
		doc:  "réverbération (Freeverb) : la queue est coupée à la fin du flux, ajoutez du silence pour la laisser mourir",
		params: []fxParam{
			{"room", 0.5, false, "taille de la pièce, de 0 (petite) à 1 (grande)"},
			{"damping", 0.5, false, "absorption des aigus, de 0 à 1"},
			{"width", 1, false, "largeur stéréo de la réverbération, de 0 (mono) à 1"},
			{"predelay", 0, false, "retard avant la réverbération, de 0 à 200 ms"},
			{"mix", 0.25, false, "part de réverbération, de 0 (sec) à 1"},
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
		name: "transient",
		doc:  "renforçateur de transitoires : accentue ou adoucit l'attaque et la queue des sons, sans seuil",
		params: []fxParam{
			{"attack", 0, false, "attaque, de -1 (aplatie) à 1 (marquée)"},
			{"sustain", 0, false, "queue des sons, de -1 (raccourcie) à 1 (allongée)"},
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

// fxList reçoit les occurrences de -fx, dans l'ordre. L'analyse se fait après coup, pour ne
// renvoyer qu'une erreur lisible.
type fxList []string

func (f *fxList) String() string     { return strings.Join(*f, " ") }
func (f *fxList) Set(v string) error { *f = append(*f, v); return nil }

// fxEffect est un effet analysé.
type fxEffect struct {
	spec string
	def  *fxDef
	vals map[string]float64
}

func fxNames() string {
	var names []string
	for _, d := range fxDefs {
		names = append(names, d.name)
	}
	return strings.Join(names, ", ")
}

// parseFX analyse « nom » ou « nom:cle=valeur,cle=valeur ».
func parseFX(spec string) (fxEffect, error) {
	spec = strings.TrimSpace(spec)
	name, rest, hasParams := strings.Cut(spec, ":")
	name = strings.ToLower(strings.TrimSpace(name))
	var def *fxDef
	for i := range fxDefs {
		if fxDefs[i].name == name {
			def = &fxDefs[i]
		}
	}
	if def == nil {
		return fxEffect{}, fmt.Errorf("-fx : effet inconnu %q (effets : %s)", name, fxNames())
	}
	keys := map[string]*fxParam{}
	var valid []string
	for i := range def.params {
		keys[def.params[i].key] = &def.params[i]
		valid = append(valid, def.params[i].key)
	}
	vals := map[string]float64{}
	if hasParams && strings.TrimSpace(rest) != "" {
		for _, kv := range strings.Split(rest, ",") {
			k, v, ok := strings.Cut(kv, "=")
			k = strings.ToLower(strings.TrimSpace(k))
			if !ok {
				return fxEffect{}, fmt.Errorf("-fx %s : %q n'est pas de la forme cle=valeur", name, strings.TrimSpace(kv))
			}
			if keys[k] == nil {
				return fxEffect{}, fmt.Errorf("-fx %s : paramètre inconnu %q (paramètres : %s)", name, k, strings.Join(valid, ", "))
			}
			if _, dup := vals[k]; dup {
				return fxEffect{}, fmt.Errorf("-fx %s : paramètre %q donné deux fois", name, k)
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return fxEffect{}, fmt.Errorf("-fx %s : %s=%q n'est pas un nombre fini", name, k, strings.TrimSpace(v))
			}
			vals[k] = f
		}
	}
	for _, p := range def.params {
		if _, ok := vals[p.key]; ok {
			continue
		}
		if p.required {
			return fxEffect{}, fmt.Errorf("-fx %s : paramètre %q obligatoire (exemple : -fx %s:%s=...)", name, p.key, name, p.key)
		}
		vals[p.key] = p.def
	}
	return fxEffect{spec: spec, def: def, vals: vals}, nil
}

// validate vérifie les valeurs pour une fréquence d'échantillonnage et un nombre de canaux donnés.
func (e fxEffect) validate(rate, channels int) error {
	if e.def.check == nil {
		return nil
	}
	if err := e.def.check(e.vals, rate, channels); err != nil {
		return fmt.Errorf("-fx %s : %w", e.def.name, err)
	}
	return nil
}

func (e fxEffect) apply(src io.Reader, rate, channels int) io.Reader {
	return e.def.build(e.vals)(src, rate, channels)
}

// fxListing décrit les effets, pour -list-fx.
func fxListing() string {
	var b strings.Builder
	for _, d := range fxDefs {
		fmt.Fprintf(&b, "%-10s %s\n", d.name, d.doc)
		for _, p := range d.params {
			req := fmt.Sprintf("défaut %g", p.def)
			if p.required {
				req = "obligatoire"
			}
			fmt.Fprintf(&b, "             %-10s %s (%s)\n", p.key, p.doc, req)
		}
	}
	return b.String()
}
