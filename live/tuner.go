package live

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
	"github.com/loxq/audioprofile/fx"
	"github.com/loxq/audioprofile/panel"
)

// Tuner relie le panneau de réglage (paquet panel) à une chaîne vivante : il sait ce que veut dire un état (quel profil,
// quelles bornes, quels effets), le vérifie et change la chaîne du Switcher. Il implémente panel.Backend.
type Tuner struct {
	cfg *config.Config
	pl  Pipeline // la chaîne de départ : fréquence, canaux
	sw  *audioprofile.Switcher

	mu    sync.Mutex
	state panel.State
	in    *audioprofile.Meter // le flux avant la chaîne (nil : pas de mesure)
	out   *audioprofile.Meter // le flux après
}

var (
	_ panel.Backend = (*Tuner)(nil)
	_ panel.Metered = (*Tuner)(nil)
)

// NewTuner prépare le panneau : l'état de départ est celui de p (profil ou ampli, effets), avec les valeurs de cfg
// (qui peut être nil). sw doit être le Switcher dont la chaîne de départ est p.Build(cfg).
func NewTuner(cfg *config.Config, p Pipeline, sw *audioprofile.Switcher) (*Tuner, error) {
	st := panel.State{Kind: panel.KindNone, Intensity: p.Intensity, FX: []panel.FX{}}
	switch {
	case p.Amp != "":
		s, ok := cfg.Amp(p.Amp)
		if !ok {
			return nil, fmt.Errorf("ampli inconnu %q", p.Amp)
		}
		if p.Cabinet {
			s.Cabinet = true
		}
		st.Kind, st.Name, st.Amp = panel.KindAmp, p.Amp, panel.AmpFrom(s)
	case p.Profile != "":
		s, ok := cfg.Profile(p.Profile)
		if !ok {
			return nil, fmt.Errorf("profil inconnu %q", p.Profile)
		}
		st.Kind, st.Name, st.Profile = panel.KindProfile, p.Profile, panel.ProfileFrom(s)
	}
	for _, spec := range p.FX {
		e, err := fx.Parse(spec, cfg.FXDefaults())
		if err != nil {
			return nil, err
		}
		st.FX = append(st.FX, panel.FX{Name: e.Def.Name, Enabled: true, Params: e.Vals})
	}
	return &Tuner{cfg: cfg, pl: p, sw: sw, state: st}, nil
}

// SetMeters branche les mesures : in mesure le flux avant la chaîne (enveloppez la source avec in.Wrap), out le flux
// après (enveloppez le Switcher avec out.Wrap). La page du panneau affiche alors des vumètres, la sonie et le spectre
// avant et après le traitement, avec la courbe de l'égaliseur en vigueur. À appeler avant de servir le panneau. Les deux
// doivent avoir été créés avec la fréquence d'échantillonnage et le nombre de canaux de la chaîne.
func (t *Tuner) SetMeters(in, out *audioprofile.Meter) {
	t.mu.Lock()
	t.in, t.out = in, out
	t.mu.Unlock()
}

func (t *Tuner) meters() (in, out *audioprofile.Meter) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.in, t.out
}

func meterSide(s audioprofile.MeterSnapshot) panel.MeterSide {
	return panel.MeterSide{PeakDB: s.PeakDBFS, RMSDB: s.RMSDBFS, Clips: s.ClipCount,
		MomentaryLUFS: s.MomentaryLUFS, ShortTermLUFS: s.ShortTermLUFS, Spectrum: s.SpectrumDBFS}
}

// Meters renvoie les mesures actuelles, avec la courbe de l'égaliseur du profil en vigueur (rien pour un ampli, une
// chaîne sans profil ou l'original). ok est faux sans SetMeters.
func (t *Tuner) Meters() (panel.MeterFrame, bool) {
	in, out := t.meters()
	if in == nil || out == nil {
		return panel.MeterFrame{}, false
	}
	frame := panel.MeterFrame{In: meterSide(in.Snapshot()), Out: meterSide(out.Snapshot())}
	if st := t.State(); st.Kind == panel.KindProfile && !st.Bypass && st.Profile != nil {
		if ps, err := st.Profile.Settings(); err == nil {
			frame.Curve = ps.EQResponse(t.pl.Rate, st.Intensity, in.SpectrumFreqs())
		}
	}
	return frame, true
}

// Schema décrit les curseurs : profils, amplis et effets.
func (t *Tuner) Schema() panel.Schema {
	s := panel.NewSchema(t.pl.Rate, t.pl.Channels)
	comp, lim := config.StartCompressor(), config.StartLimiter() // les mêmes valeurs que le fichier de réglages
	c := panel.CompFrom(comp)
	s.Defaults.Compression = &c
	s.Defaults.Limiter = &panel.Limiter{Ceiling: lim.CeilingDB, Lookahead: lim.LookaheadMs, Release: lim.ReleaseMs}
	s.FX = fxSchemas()
	s.Profiles = t.cfg.ProfileNames()
	s.Amps = t.cfg.AmpNames()
	if in, _ := t.meters(); in != nil {
		s.MeterFreqs = in.SpectrumFreqs()
	}
	return s
}

// State renvoie l'état actuel.
func (t *Tuner) State() panel.State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

// resolve vérifie l'état et construit la chaîne qui lui correspond. Il renvoie aussi l'état complété (paramètres
// d'effet manquants remplis par leur défaut).
func (t *Tuner) resolve(s panel.State) (func(io.Reader) io.Reader, panel.State, error) {
	if math.IsNaN(s.Intensity) || s.Intensity < 0 || s.Intensity > audioprofile.MaxIntensity {
		return nil, s, fmt.Errorf("intensity doit être dans [0, %g]", audioprofile.MaxIntensity)
	}
	pl := t.pl
	pl.Intensity, pl.Profile, pl.Amp, pl.Cabinet = s.Intensity, "", "", false
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
		if err = config.ValidateProfile(ps, pl.Rate); err != nil {
			return nil, s, err
		}
		pl.Profile = s.Name
	case panel.KindAmp:
		if s.Amp == nil || strings.TrimSpace(s.Name) == "" {
			return nil, s, errors.New("un ampli demande un nom et ses réglages")
		}
		as = s.Amp.Settings()
		if err := config.ValidateAmp(as); err != nil {
			return nil, s, err
		}
		pl.Amp = s.Name
	case panel.KindNone:
	default:
		return nil, s, fmt.Errorf("kind %q inconnu (profile, amp ou none)", s.Kind)
	}
	var effects []fx.Effect
	canon := s
	canon.FX = make([]panel.FX, 0, len(s.FX))
	for i, f := range s.FX {
		e, err := fx.New(f.Name, f.Params, nil)
		if err != nil {
			return nil, s, fmt.Errorf("fx[%d] : %w", i, err)
		}
		if err := e.Validate(pl.Rate, pl.Channels); err != nil {
			return nil, s, err
		}
		canon.FX = append(canon.FX, panel.FX{Name: e.Def.Name, Enabled: f.Enabled, Params: e.Vals})
		if f.Enabled {
			effects = append(effects, e)
		}
	}
	if s.Bypass {
		return func(r io.Reader) io.Reader { return r }, canon, nil
	}
	return pl.Assemble(ps, as, effects), canon, nil
}

// Apply vérifie l'état, change la chaîne et le retient. Une erreur laisse tout en place.
func (t *Tuner) Apply(s panel.State) error {
	process, canon, err := t.resolve(s)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sw.Swap(process)
	t.state = canon
	return nil
}

// Preset renvoie les réglages de départ d'un profil ou d'un ampli (valeurs du fichier comprises).
func (t *Tuner) Preset(kind, name string) (*panel.Profile, *panel.Amp, error) {
	switch kind {
	case panel.KindProfile:
		if s, ok := t.cfg.Profile(name); ok {
			return panel.ProfileFrom(s), nil, nil
		}
		return nil, nil, fmt.Errorf("profil inconnu %q", name)
	case panel.KindAmp:
		if s, ok := t.cfg.Amp(name); ok {
			return nil, panel.AmpFrom(s), nil
		}
		return nil, nil, fmt.Errorf("ampli inconnu %q", name)
	}
	return nil, nil, fmt.Errorf("kind %q inconnu", kind)
}

// Export écrit l'état en YAML (profil ou ampli, au format du fichier de réglages) et la ligne de commande équivalente.
func (t *Tuner) Export() (string, error) {
	s := t.State()
	var out strings.Builder
	name := strings.ToLower(s.Name)
	switch s.Kind {
	case panel.KindProfile:
		ps, err := s.Profile.Settings()
		if err != nil {
			return "", err
		}
		b, err := config.MarshalProfile(name, ps)
		if err != nil {
			return "", err
		}
		out.Write(b)
	case panel.KindAmp:
		b, err := config.MarshalAmp(name, s.Amp.Settings())
		if err != nil {
			return "", err
		}
		out.Write(b)
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
