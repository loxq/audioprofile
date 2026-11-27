package live

import (
	"math"

	"github.com/loxq/audioprofile/fx"
	"github.com/loxq/audioprofile/panel"
)

// fxSchemas décrit les effets du registre à la page : paramètres, valeurs par défaut, plages des curseurs.
func fxSchemas() []panel.FXSchema {
	var out []panel.FXSchema
	for _, d := range fx.Defs() {
		fs := panel.FXSchema{Name: d.Name, Doc: d.Doc}
		for _, p := range d.Params {
			f := panel.NumField(p.Key, p.Key, p.Unit, p.Min, p.Max, p.Default, p.Log, p.Int)
			f.Bool = p.Bool
			f.Required, f.Doc = p.Required, p.Doc
			if p.Required { // un effet ajouté depuis le panneau doit être valide : valeur de départ audible
				f.Def = math.Min(math.Max(1000, p.Min), p.Max)
			}
			fs.Params = append(fs.Params, f)
		}
		out = append(out, fs)
	}
	return out
}
