package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/loxq/audioprofile"
)

// Le panneau de réglage (-serve) : un petit serveur HTTP local et une page de curseurs. Chaque changement est
// vérifié, transformé en chaîne de traitement et passé au Switcher : il s'entend sans clic, sans relancer.

//go:embed panel.html
var panelHTML []byte

// ---------------------------------------------------------------- état échangé avec la page

type panelBand struct {
	Type  string  `json:"type"`
	Freq  float64 `json:"freq"`
	Gain  float64 `json:"gain"`
	Q     float64 `json:"q"`
	Order int     `json:"order"`
}

type panelComp struct {
	Threshold float64 `json:"threshold"`
	Ratio     float64 `json:"ratio"`
	Attack    float64 `json:"attack"`
	Release   float64 `json:"release"`
	Knee      float64 `json:"knee"`
	Makeup    float64 `json:"makeup"`
}

type panelSat struct {
	Drive  float64 `json:"drive"`
	Bias   float64 `json:"bias"`
	Output float64 `json:"output"`
	Stages int     `json:"stages"`
}

type panelMulti struct {
	Crossovers []float64   `json:"crossovers"`
	Bands      []panelComp `json:"bands"`
}

type panelLimiter struct {
	Ceiling   float64 `json:"ceiling"`
	Lookahead float64 `json:"lookahead"`
	Release   float64 `json:"release"`
}

// panelProfile est un profil complet ; un étage absent vaut null.
type panelProfile struct {
	Bands       []panelBand   `json:"bands"`
	Compression *panelComp    `json:"compression"`
	Saturation  *panelSat     `json:"saturation"`
	Multiband   *panelMulti   `json:"multiband"`
	Width       float64       `json:"width"`
	Limiter     *panelLimiter `json:"limiter"`
}

type panelAmp struct {
	Drive   float64 `json:"drive"`
	Stages  int     `json:"stages"`
	Bias    float64 `json:"bias"`
	Bass    float64 `json:"bass"`
	Mid     float64 `json:"mid"`
	Treble  float64 `json:"treble"`
	Cabinet bool    `json:"cabinet"`
	Output  float64 `json:"output"`
}

type panelFX struct {
	Name    string             `json:"name"`
	Enabled bool               `json:"enabled"`
	Params  map[string]float64 `json:"params"`
}

// panelState est tout ce que règle la page.
type panelState struct {
	Kind      string        `json:"kind"` // "profile", "amp" ou "none"
	Name      string        `json:"name"`
	Intensity float64       `json:"intensity"`
	Bypass    bool          `json:"bypass"` // true : l'original, pour comparer
	Profile   *panelProfile `json:"profile,omitempty"`
	Amp       *panelAmp     `json:"amp,omitempty"`
	FX        []panelFX     `json:"fx"`
}

func panelCompFrom(c audioprofile.CompressorSettings) panelComp {
	return panelComp{c.ThresholdDB, c.Ratio, c.AttackMs, c.ReleaseMs, c.KneeDB, c.MakeupDB}
}

func (c panelComp) settings() audioprofile.CompressorSettings {
	return audioprofile.CompressorSettings{ThresholdDB: c.Threshold, Ratio: c.Ratio, AttackMs: c.Attack, ReleaseMs: c.Release, KneeDB: c.Knee, MakeupDB: c.Makeup}
}

func profileToPanel(s audioprofile.ProfileSettings) *panelProfile {
	pp := &panelProfile{Bands: []panelBand{}, Width: s.Width}
	for _, b := range s.Bands {
		q, order := b.Q, b.Order
		if q <= 0 { // les valeurs par défaut de la bibliothèque, rendues explicites pour le curseur
			q = math.Sqrt2 / 2
			if b.Type == audioprofile.Notch {
				q = 10
			}
		}
		if order <= 0 {
			order = 2
		}
		pp.Bands = append(pp.Bands, panelBand{Type: bandTypeName(b.Type), Freq: b.FreqHz, Gain: b.GainDB, Q: q, Order: order})
	}
	if s.Compression != nil {
		c := panelCompFrom(*s.Compression)
		pp.Compression = &c
	}
	if s.Saturation != (audioprofile.SaturationSettings{}) {
		pp.Saturation = &panelSat{s.Saturation.DriveDB, s.Saturation.Bias, s.Saturation.OutputDB, s.Saturation.Stages}
	}
	if m := s.Multiband; m != nil {
		pm := &panelMulti{Crossovers: append([]float64{}, m.Crossovers...), Bands: []panelComp{}}
		for _, c := range m.Bands {
			pm.Bands = append(pm.Bands, panelCompFrom(c))
		}
		pp.Multiband = pm
	}
	if l := s.Limiter; l != nil {
		pp.Limiter = &panelLimiter{l.CeilingDB, l.LookaheadMs, l.ReleaseMs}
	}
	return pp
}

func (pp *panelProfile) settings() (audioprofile.ProfileSettings, error) {
	var s audioprofile.ProfileSettings
	for i, b := range pp.Bands {
		t, ok := bandTypeNames[strings.ToLower(b.Type)]
		if !ok {
			return s, fmt.Errorf("bands[%d] : type %q inconnu", i, b.Type)
		}
		s.Bands = append(s.Bands, audioprofile.Band{Type: t, FreqHz: b.Freq, GainDB: b.Gain, Q: b.Q, Order: b.Order})
	}
	if pp.Compression != nil {
		c := pp.Compression.settings()
		s.Compression = &c
	}
	if sat := pp.Saturation; sat != nil {
		s.Saturation = audioprofile.SaturationSettings{DriveDB: sat.Drive, Bias: sat.Bias, OutputDB: sat.Output, Stages: sat.Stages}
	}
	if m := pp.Multiband; m != nil {
		ms := &audioprofile.MultibandSettings{Crossovers: append([]float64(nil), m.Crossovers...)}
		for _, c := range m.Bands {
			ms.Bands = append(ms.Bands, c.settings())
		}
		s.Multiband = ms
	}
	s.Width = pp.Width
	if l := pp.Limiter; l != nil {
		s.Limiter = &audioprofile.LimiterSettings{CeilingDB: l.Ceiling, LookaheadMs: l.Lookahead, ReleaseMs: l.Release}
	}
	return s, nil
}

func ampToPanel(s audioprofile.TubeAmpSettings) *panelAmp {
	return &panelAmp{s.DriveDB, s.Stages, s.Bias, s.BassDB, s.MidDB, s.TrebleDB, s.Cabinet, s.OutputDB}
}

func (a *panelAmp) settings() audioprofile.TubeAmpSettings {
	return audioprofile.TubeAmpSettings{DriveDB: a.Drive, Stages: a.Stages, Bias: a.Bias, BassDB: a.Bass, MidDB: a.Mid, TrebleDB: a.Treble, Cabinet: a.Cabinet, OutputDB: a.Output}
}

func fxByName(name string) *fxDef {
	for i := range fxDefs {
		if fxDefs[i].name == name {
			return &fxDefs[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------- le panneau

type panel struct {
	cfg *config
	pl  pipeline // la ligne de commande : fréquence, canaux
	sw  *audioprofile.Switcher

	mu    sync.Mutex
	state panelState
	allow func(host string) bool // Host acceptés (anti-rebond DNS)
}

// newPanel prépare le panneau : l'état de départ est celui de la ligne de commande (profil ou ampli, effets).
func newPanel(cfg *config, pl pipeline, sw *audioprofile.Switcher) (*panel, error) {
	p := &panel{cfg: cfg, pl: pl, sw: sw}
	st := panelState{Kind: "none", Intensity: pl.intensity, FX: []panelFX{}}
	switch {
	case pl.amp != "":
		s, ok := cfg.amp(pl.amp)
		if !ok {
			return nil, fmt.Errorf("ampli inconnu %q", pl.amp)
		}
		if pl.cabinet {
			s.Cabinet = true
		}
		st.Kind, st.Name, st.Amp = "amp", pl.amp, ampToPanel(s)
	case pl.profile != "":
		s, ok := cfg.profile(pl.profile)
		if !ok {
			return nil, fmt.Errorf("profil inconnu %q", pl.profile)
		}
		st.Kind, st.Name, st.Profile = "profile", pl.profile, profileToPanel(s)
	}
	for _, spec := range pl.fxSpecs {
		e, err := parseFX(spec, cfg.fxDefaults)
		if err != nil {
			return nil, err
		}
		st.FX = append(st.FX, panelFX{Name: e.def.name, Enabled: true, Params: e.vals})
	}
	p.state = st
	return p, nil
}

// resolve vérifie l'état et construit la chaîne qui lui correspond. Il renvoie aussi l'état complété (paramètres
// d'effet manquants remplis par leur défaut).
func (p *panel) resolve(s panelState) (func(io.Reader) io.Reader, panelState, error) {
	if math.IsNaN(s.Intensity) || s.Intensity < 0 || s.Intensity > audioprofile.MaxIntensity {
		return nil, s, fmt.Errorf("intensity doit être dans [0, %g]", audioprofile.MaxIntensity)
	}
	pl := p.pl
	pl.intensity, pl.profile, pl.amp, pl.cabinet = s.Intensity, "", "", false
	var ps audioprofile.ProfileSettings
	var as audioprofile.TubeAmpSettings
	switch s.Kind {
	case "profile":
		if s.Profile == nil || strings.TrimSpace(s.Name) == "" {
			return nil, s, errors.New("un profil demande un nom et ses réglages")
		}
		var err error
		if ps, err = s.Profile.settings(); err != nil {
			return nil, s, err
		}
		if err = validateProfile(ps, pl.rate); err != nil {
			return nil, s, err
		}
		pl.profile = s.Name
	case "amp":
		if s.Amp == nil || strings.TrimSpace(s.Name) == "" {
			return nil, s, errors.New("un ampli demande un nom et ses réglages")
		}
		as = s.Amp.settings()
		if err := validateAmp(as); err != nil {
			return nil, s, err
		}
		pl.amp = s.Name
	case "none":
	default:
		return nil, s, fmt.Errorf("kind %q inconnu (profile, amp ou none)", s.Kind)
	}
	var effects []fxEffect
	canon := s
	canon.FX = make([]panelFX, 0, len(s.FX))
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
		canon.FX = append(canon.FX, panelFX{Name: f.Name, Enabled: f.Enabled, Params: vals})
		if f.Enabled {
			effects = append(effects, e)
		}
	}
	if s.Bypass {
		return func(r io.Reader) io.Reader { return r }, canon, nil
	}
	return pl.assemble(ps, as, effects), canon, nil
}

// apply vérifie l'état, change la chaîne et le retient. Une erreur laisse tout en place.
func (p *panel) apply(s panelState) error {
	process, canon, err := p.resolve(s)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sw.Swap(process)
	p.state = canon
	return nil
}

func (p *panel) current() panelState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// preset renvoie les réglages de départ d'un profil ou d'un ampli (valeurs du fichier comprises).
func (p *panel) preset(kind, name string) (*panelProfile, *panelAmp, error) {
	switch kind {
	case "profile":
		if s, ok := p.cfg.profile(name); ok {
			return profileToPanel(s), nil, nil
		}
		return nil, nil, fmt.Errorf("profil inconnu %q", name)
	case "amp":
		if s, ok := p.cfg.amp(name); ok {
			return nil, ampToPanel(s), nil
		}
		return nil, nil, fmt.Errorf("ampli inconnu %q", name)
	}
	return nil, nil, fmt.Errorf("kind %q inconnu", kind)
}

// exportYAML écrit l'état en YAML (profil ou ampli, au format de -config) et la ligne de commande équivalente.
func (p *panel) exportYAML(s panelState) (string, error) {
	var b strings.Builder
	f := configFile{}
	switch s.Kind {
	case "profile":
		ps, err := s.Profile.settings()
		if err != nil {
			return "", err
		}
		f.Profiles = map[string]*profileConfig{strings.ToLower(s.Name): profileToConfig(strings.ToLower(s.Name), ps)}
	case "amp":
		f.Amps = map[string]*ampConfig{strings.ToLower(s.Name): ampToConfig(s.Amp.settings())}
	}
	if f.Profiles != nil || f.Amps != nil {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(f); err != nil {
			return "", err
		}
		enc.Close()
		b.Write(buf.Bytes())
	}
	// ligne de commande équivalente (les effets ne se mettent pas dans le fichier : ils viennent de -fx)
	args := []string{"audioprofile"}
	switch s.Kind {
	case "profile":
		args = append(args, "-config", "reglages.yaml", "-profile", strings.ToLower(s.Name))
	case "amp":
		args = append(args, "-config", "reglages.yaml", "-amp", strings.ToLower(s.Name))
	}
	if s.Kind != "none" && s.Intensity != 1 {
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
	fmt.Fprintf(&b, "\n# ligne de commande équivalente :\n# %s\n", strings.Join(args, " "))
	return b.String(), nil
}

// ---------------------------------------------------------------- HTTP

func (p *panel) handler() http.Handler {
	schema := newPanelSchema(p.pl.rate, p.pl.channels, p.cfg)
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, code int, err error) {
		writeJSON(w, code, map[string]string{"error": err.Error()})
	}

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(panelHTML)
	})
	mux.HandleFunc("GET /api/schema", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, schema) })
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, p.current()) })
	mux.HandleFunc("PUT /api/state", func(w http.ResponseWriter, r *http.Request) {
		if mt := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mt != "application/json" {
			fail(w, http.StatusUnsupportedMediaType, errors.New("Content-Type doit être application/json"))
			return
		}
		var s panelState
		dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := p.apply(s); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/preset", func(w http.ResponseWriter, r *http.Request) {
		pp, pa, err := p.preset(r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, 200, map[string]any{"profile": pp, "amp": pa})
	})
	mux.HandleFunc("GET /api/yaml", func(w http.ResponseWriter, r *http.Request) {
		out, err := p.exportYAML(p.current())
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, out)
	})

	// Le serveur n'a pas d'authentification : il ne parle qu'à une page de la même origine, sur une adresse de bouclage.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.allow != nil && !p.allow(r.Host) {
			http.Error(w, "hôte refusé", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "origine refusée", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// loopbackHost accepte les noms d'hôte d'une adresse de bouclage : une page d'un autre site qui ferait pointer son
// nom vers 127.0.0.1 (rebond DNS) présente son propre nom dans Host et est refusée.
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// isLoopbackAddr indique si l'adresse d'écoute n'est joignable que depuis cette machine.
func isLoopbackAddr(addr net.Addr) bool {
	if ta, ok := addr.(*net.TCPAddr); ok {
		return ta.IP.IsLoopback()
	}
	return false
}

// servePanel écoute sur addr et sert le panneau jusqu'à l'arrêt de stop. Il renvoie l'URL et la fonction d'arrêt.
func servePanel(addr string, p *panel) (url string, stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("-serve : %w", err)
	}
	if isLoopbackAddr(ln.Addr()) {
		p.allow = loopbackHost
	}
	srv := &http.Server{Handler: p.handler(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ln) }()
	var once sync.Once
	return "http://" + ln.Addr().String() + "/", func() {
		once.Do(func() { srv.Close(); <-done })
	}, nil
}
