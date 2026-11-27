// Package live assemble ce qu'il faut pour régler un traitement audio en direct : la chaîne demandée (un profil ou un
// ampli, puis des effets), son rechargement à chaud depuis un fichier de réglages, et la liaison avec le panneau
// web (paquet panel). Tout passe par un audioprofile.Switcher, qui change de chaîne sans clic.
//
// Un programme de streaming qui veut offrir ces outils à ses développeurs écrit à peu près :
//
//	cfg, _ := config.Load("reglages.yaml")
//	pl := live.Pipeline{Profile: "rock", Intensity: 1, Rate: 44100, Channels: 2}
//	build, _ := pl.Build(cfg)
//	sw := audioprofile.NewSwitcher(src, pl.Rate, pl.Channels, build)
//	tuner, _ := live.NewTuner(cfg, pl, sw)
//	url, stop, _ := panel.Serve("127.0.0.1:8080", tuner) // ou live.Watch(...) pour recharger le fichier
//	defer stop()
//	// lire sw : le flux traité
package live

import (
	"fmt"
	"io"
	"strings"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
	"github.com/loxq/audioprofile/fx"
)

// Pipeline est la chaîne demandée : un profil ou un ampli (au plus un), puis les effets. Ses réglages viennent
// d'une configuration, qu'on peut donc remplacer à chaud (Watch, Tuner).
type Pipeline struct {
	Profile   string   // nom d'un profil (prédéfini ou du fichier) ; vide : aucun
	Amp       string   // nom d'un préréglage d'ampli ; vide : aucun. Prioritaire sur Profile.
	Cabinet   bool     // avec un ampli : force le baffle (sinon, le fichier ou le préréglage décide)
	Intensity float64  // intensité du profil ou de l'ampli, de 0 à audioprofile.MaxIntensity
	Rate      int      // fréquence d'échantillonnage du flux
	Channels  int      // nombre de canaux
	FX        []string // effets, dans l'ordre, au format de fx.Parse
}

// Build résout et vérifie les réglages dans cfg et renvoie la fonction qui enveloppe un flux dans la chaîne
// (la forme attendue par audioprofile.NewSwitcher et Switcher.Swap). cfg peut être nil.
func (p Pipeline) Build(cfg *config.Config) (func(io.Reader) io.Reader, error) {
	var (
		ps audioprofile.ProfileSettings
		as audioprofile.TubeAmpSettings
		ok bool
	)
	if p.Amp != "" {
		if as, ok = cfg.Amp(p.Amp); !ok {
			return nil, fmt.Errorf("ampli inconnu %q (amplis : %s)", p.Amp, strings.Join(cfg.AmpNames(), ", "))
		}
	} else if p.Profile != "" {
		if ps, ok = cfg.Profile(p.Profile); !ok {
			return nil, fmt.Errorf("profil inconnu %q (profils : %s)", p.Profile, strings.Join(cfg.ProfileNames(), ", "))
		}
	}
	if err := cfg.Validate(p.Rate); err != nil {
		return nil, err
	}
	var effects []fx.Effect
	for _, spec := range p.FX {
		e, err := fx.Parse(spec, cfg.FXDefaults())
		if err != nil {
			return nil, err
		}
		if err := e.Validate(p.Rate, p.Channels); err != nil {
			return nil, err
		}
		effects = append(effects, e)
	}
	return p.Assemble(ps, as, effects), nil
}

// Assemble construit la fonction qui enveloppe un flux dans la chaîne : le profil ou l'ampli aux réglages
// donnés, puis les effets, dans l'ordre. Les réglages doivent déjà être vérifiés.
func (p Pipeline) Assemble(ps audioprofile.ProfileSettings, as audioprofile.TubeAmpSettings, effects []fx.Effect) func(io.Reader) io.Reader {
	return func(src io.Reader) io.Reader {
		r := src
		switch {
		case p.Amp != "":
			s := as.Scaled(p.Intensity)
			if p.Cabinet { // sans Cabinet, le fichier de configuration décide
				s.Cabinet = true
			}
			r = audioprofile.TubeAmp(r, p.Rate, p.Channels, s)
		case p.Profile != "":
			r = audioprofile.ApplyProfileSettings(r, p.Rate, p.Channels, ps, p.Intensity)
		}
		for _, e := range effects {
			r = e.Apply(r, p.Rate, p.Channels)
		}
		return r
	}
}
