package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/internal/panel"
)

// panelBackend relie le panneau de réglage (internal/panel) à la chaîne de la ligne de commande : il sait ce que
// veut dire un état (quel profil, quelles bornes, quels effets) et change la chaîne du Switcher.
type panelBackend struct {
	cfg *config
	pl  pipeline // la ligne de commande : fréquence, canaux
	sw  *audioprofile.Switcher

	mu    sync.Mutex
	state panel.State
}

var _ panel.Backend = (*panelBackend)(nil)

// newPanelBackend prépare le panneau : l'état de départ est celui de la ligne de commande (profil ou ampli, effets).
func newPanelBackend(cfg *config, pl pipeline, sw *audioprofile.Switcher) (*panelBackend, error) {
	st := panel.State{Kind: panel.KindNone, Intensity: pl.intensity, FX: []panel.FX{}}
	switch {
	case pl.amp != "":
		s, ok := cfg.amp(pl.amp)
		if !ok {
			return nil, fmt.Errorf("ampli inconnu %q", pl.amp)
		}
		if pl.cabinet {
			s.Cabinet = true
		}
		st.Kind, st.Name, st.Amp = panel.KindAmp, pl.amp, panel.AmpFrom(s)
	case pl.profile != "":
		s, ok := cfg.profile(pl.profile)
		if !ok {
			return nil, fmt.Errorf("profil inconnu %q", pl.profile)
		}
		st.Kind, st.Name, st.Profile = panel.KindProfile, pl.profile, panel.ProfileFrom(s)
	}
	for _, spec := range pl.fxSpecs {
		e, err := parseFX(spec, cfg.fxDefaults)
		if err != nil {
			return nil, err
		}
		st.FX = append(st.FX, panel.FX{Name: e.def.name, Enabled: true, Params: e.vals})
	}
	return &panelBackend{cfg: cfg, pl: pl, sw: sw, state: st}, nil
}

func (b *panelBackend) Schema() panel.Schema {
	s := panel.NewSchema(b.pl.rate, b.pl.channels)
	s.FX = fxSchemas()
	s.Profiles = b.cfg.profileNames()
	s.Amps = b.cfg.ampNames()
	return s
}

func (b *panelBackend) State() panel.State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func fxByName(name string) *fxDef {
	for i := range fxDefs {
		if fxDefs[i].name == name {
			return &fxDefs[i]
		}
	}
	return nil
}

// resolve vérifie l'état et construit la chaîne qui lui correspond. Il renvoie aussi l'état complété (paramètres
// d'effet manquants remplis par leur défaut).
func (b *panelBackend) resolve(s panel.State) (func(io.Reader) io.Reader, panel.State, error) {
	if math.IsNaN(s.Intensity) || s.Intensity < 0 || s.Intensity > audioprofile.MaxIntensity {
		return nil, s, fmt.Errorf("intensity doit être dans [0, %g]", audioprofile.MaxIntensity)
	}
	pl := b.pl
	pl.intensity, pl.profile, pl.amp, pl.cabinet = s.Intensity, "", "", false
	var ps audioprofile.ProfileSettings
	var as audioprofile.TubeAmpSettings
	switch s.Kind {
	case panel.KindProfile:
		if s.Profile == nil || strings.TrimSpace(s.Name) == "" {
			return nil, s, errors.New("un profil demande un nom et ses réglages")
		}
		var err error
		if ps, err = s.Profile.Settings(); err != nil {
			return nil, s, err
		}
		if err = validateProfile(ps, pl.rate); err != nil {
			return nil, s, err
		}
		pl.profile = s.Name
	case panel.KindAmp:
		if s.Amp == nil || strings.TrimSpace(s.Name) == "" {
			return nil, s, errors.New("un ampli demande un nom et ses réglages")
		}
		as = s.Amp.Settings()
		if err := validateAmp(as); err != nil {
			return nil, s, err
		}
		pl.amp = s.Name
	case panel.KindNone:
	default:
		return nil, s, fmt.Errorf("kind %q inconnu (profile, amp ou none)", s.Kind)
	}
	var effects []fxEffect
	canon := s
	canon.FX = make([]panel.FX, 0, len(s.FX))
	for i, f := range s.FX {
		def := fxByName(f.Name)
		if def == nil {
			return nil, s, fmt.Errorf("fx[%d] : effet %q inconnu (effets : %s)", i, f.Name, fxNames())
		}
		vals := map[string]float64{}
		for k, v := range f.Params {
			ok := false
			for _, prm := range def.params {
				ok = ok || prm.key == k
			}
			if !ok {
				return nil, s, fmt.Errorf("fx %s : paramètre %q inconnu", f.Name, k)
			}
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, s, fmt.Errorf("fx %s : %s n'est pas un nombre fini", f.Name, k)
			}
			vals[k] = v
		}
		for _, prm := range def.params {
			if _, ok := vals[prm.key]; !ok {
				if prm.required {
					return nil, s, fmt.Errorf("fx %s : paramètre %q obligatoire", f.Name, prm.key)
				}
				vals[prm.key] = prm.def
			}
		}
		e := fxEffect{spec: f.Name, def: def, vals: vals}
		if err := e.validate(pl.rate, pl.channels); err != nil {
			return nil, s, err
		}
		canon.FX = append(canon.FX, panel.FX{Name: f.Name, Enabled: f.Enabled, Params: vals})
		if f.Enabled {
			effects = append(effects, e)
		}
	}
	if s.Bypass {
		return func(r io.Reader) io.Reader { return r }, canon, nil
	}
	return pl.assemble(ps, as, effects), canon, nil
}

// Apply vérifie l'état, change la chaîne et le retient. Une erreur laisse tout en place.
func (b *panelBackend) Apply(s panel.State) error {
	process, canon, err := b.resolve(s)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sw.Swap(process)
	b.state = canon
	return nil
}

// Preset renvoie les réglages de départ d'un profil ou d'un ampli (valeurs du fichier comprises).
func (b *panelBackend) Preset(kind, name string) (*panel.Profile, *panel.Amp, error) {
	switch kind {
	case panel.KindProfile:
		if s, ok := b.cfg.profile(name); ok {
			return panel.ProfileFrom(s), nil, nil
		}
		return nil, nil, fmt.Errorf("profil inconnu %q", name)
	case panel.KindAmp:
		if s, ok := b.cfg.amp(name); ok {
			return nil, panel.AmpFrom(s), nil
		}
		return nil, nil, fmt.Errorf("ampli inconnu %q", name)
	}
	return nil, nil, fmt.Errorf("kind %q inconnu", kind)
}

// Export écrit l'état en YAML (profil ou ampli, au format de -config) et la ligne de commande équivalente.
func (b *panelBackend) Export() (string, error) {
	s := b.State()
	var out strings.Builder
	f := configFile{}
	name := strings.ToLower(s.Name)
	switch s.Kind {
	case panel.KindProfile:
		ps, err := s.Profile.Settings()
		if err != nil {
			return "", err
		}
		f.Profiles = map[string]*profileConfig{name: profileToConfig(name, ps)}
	case panel.KindAmp:
		f.Amps = map[string]*ampConfig{name: ampToConfig(s.Amp.Settings())}
	}
	if f.Profiles != nil || f.Amps != nil {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(f); err != nil {
			return "", err
		}
		enc.Close()
		out.Write(buf.Bytes())
	}
	// ligne de commande équivalente (les effets ne se mettent pas dans le fichier : ils viennent de -fx)
	args := []string{"audioprofile"}
	switch s.Kind {
	case panel.KindProfile:
		args = append(args, "-config", "reglages.yaml", "-profile", name)
	case panel.KindAmp:
		args = append(args, "-config", "reglages.yaml", "-amp", name)
	}
	if s.Kind != panel.KindNone && s.Intensity != 1 {
		args = append(args, "-intensity", fmt.Sprint(s.Intensity))
	}
	for _, e := range s.FX {
		if !e.Enabled {
			continue
		}
		keys := make([]string, 0, len(e.Params))
		for k := range e.Params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var kv []string
		for _, k := range keys {
			kv = append(kv, fmt.Sprintf("%s=%g", k, e.Params[k]))
		}
		args = append(args, "-fx", e.Name+":"+strings.Join(kv, ","))
	}
	fmt.Fprintf(&out, "\n# ligne de commande équivalente :\n# %s\n", strings.Join(args, " "))
	return out.String(), nil
}
