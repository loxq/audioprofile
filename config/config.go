// Package config lit et écrit le fichier de réglages YAML : réglages des profils, des préréglages d'ampli et
// valeurs par défaut des effets. Tout ce que le fichier ne précise pas garde sa valeur codée en dur dans la
// bibliothèque, donc un fichier absent ou vide ne change rien. Voir audioprofile.example.yaml pour le format.
//
// Un *Config est en lecture seule une fois construit. nil est la configuration vide : toutes les méthodes y
// répondent avec les valeurs par défaut.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/fx"
)

// ---------------------------------------------------------------- schéma YAML

// configFile est le contenu du fichier. Les clés inconnues sont refusées (une faute de frappe ne doit
// pas être ignorée en silence). Les champs sont des pointeurs : nil = non précisé = valeur par défaut.
type configFile struct {
	Profiles map[string]*profileConfig `yaml:"profiles,omitempty"`
	Amps     map[string]*ampConfig     `yaml:"amps,omitempty"`
	FX       map[string]map[string]any `yaml:"fx,omitempty"`
}

type profileConfig struct {
	Base        string            `yaml:"base,omitempty"` // profil prédéfini de départ ; par défaut, le profil de même nom
	Bands       *[]bandConfig     `yaml:"bands,omitempty"`
	Compression *compressorConfig `yaml:"compression,omitempty"`
	Saturation  *saturationConfig `yaml:"saturation,omitempty"`
	Multiband   *multibandConfig  `yaml:"multiband,omitempty"`
	Width       *float64          `yaml:"width,omitempty"`
	Limiter     *limiterConfig    `yaml:"limiter,omitempty"`
}

type bandConfig struct {
	Type  string  `yaml:"type"`
	Freq  float64 `yaml:"freq,omitempty"`
	Gain  float64 `yaml:"gain,omitempty"`
	Q     float64 `yaml:"q,omitempty"`
	Order int     `yaml:"order,omitempty"`
}

type compressorConfig struct {
	Enabled   *bool    `yaml:"enabled,omitempty"` // false : supprime l'étage
	Threshold *float64 `yaml:"threshold,omitempty"`
	Ratio     *float64 `yaml:"ratio,omitempty"`
	Attack    *float64 `yaml:"attack,omitempty"`
	Release   *float64 `yaml:"release,omitempty"`
	Knee      *float64 `yaml:"knee,omitempty"`
	Makeup    *float64 `yaml:"makeup,omitempty"`
}

type saturationConfig struct {
	Enabled *bool    `yaml:"enabled,omitempty"`
	Drive   *float64 `yaml:"drive,omitempty"`
	Bias    *float64 `yaml:"bias,omitempty"`
	Output  *float64 `yaml:"output,omitempty"`
	Stages  *int     `yaml:"stages,omitempty"`
}

type multibandConfig struct {
	Enabled    *bool               `yaml:"enabled,omitempty"`
	Crossovers *[]float64          `yaml:"crossovers,omitempty"`
	Bands      *[]compressorConfig `yaml:"bands,omitempty"` // fusionnées bande par bande avec celles de départ
}

type limiterConfig struct {
	Enabled   *bool    `yaml:"enabled,omitempty"`
	Ceiling   *float64 `yaml:"ceiling,omitempty"`
	Lookahead *float64 `yaml:"lookahead,omitempty"`
	Release   *float64 `yaml:"release,omitempty"`
}

type ampConfig struct {
	Base    string   `yaml:"base,omitempty"` // préréglage prédéfini de départ ; par défaut, celui de même nom
	Drive   *float64 `yaml:"drive,omitempty"`
	Stages  *int     `yaml:"stages,omitempty"`
	Bias    *float64 `yaml:"bias,omitempty"`
	Bass    *float64 `yaml:"bass,omitempty"`
	Mid     *float64 `yaml:"mid,omitempty"`
	Treble  *float64 `yaml:"treble,omitempty"`
	Cabinet *bool    `yaml:"cabinet,omitempty"`
	Output  *float64 `yaml:"output,omitempty"`
}

// bandTypeList liste les types de bande, triés, pour les messages d'erreur.
func bandTypeList() string {
	var names []string
	for _, t := range audioprofile.BandTypes() {
		names = append(names, t.String())
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// ---------------------------------------------------------------- configuration résolue

// Config est la configuration résolue : chaque profil et chaque ampli du fichier est complet (fusionné avec
// sa base prédéfinie). Le zéro (ou nil) est la configuration vide : tout reste aux valeurs par défaut.
type Config struct {
	profiles   map[string]audioprofile.ProfileSettings
	amps       map[string]audioprofile.TubeAmpSettings
	fxDefaults map[string]map[string]float64
}

// FXDefaults renvoie les valeurs par défaut des effets du fichier, par nom d'effet puis de paramètre (nil si le
// fichier n'en précise aucune). C'est ce qu'attendent fx.Parse et fx.New. La table est partagée : ne la modifiez pas.
func (c *Config) FXDefaults() map[string]map[string]float64 {
	if c == nil {
		return nil
	}
	return c.fxDefaults
}

// Load lit le fichier YAML (path vide : configuration vide).
func Load(path string) (*Config, error) {
	if path == "" {
		return &Config{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config : %w", err)
	}
	c, err := Parse(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("config %s : %w", path, err)
	}
	return c, nil
}

// Parse lit une configuration YAML. Les clés inconnues sont refusées.
func Parse(r io.Reader) (*Config, error) {
	var f configFile
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) { // io.EOF : fichier vide
		return nil, err
	}
	c := &Config{}
	for _, name := range sortedKeys(f.Profiles) {
		pc := f.Profiles[name]
		if pc == nil {
			pc = &profileConfig{}
		}
		s, err := pc.resolve(strings.ToLower(name))
		if err != nil {
			return nil, fmt.Errorf("profil %q : %w", name, err)
		}
		if c.profiles == nil {
			c.profiles = map[string]audioprofile.ProfileSettings{}
		}
		c.profiles[strings.ToLower(name)] = s
	}
	for _, name := range sortedKeys(f.Amps) {
		ac := f.Amps[name]
		if ac == nil {
			ac = &ampConfig{}
		}
		s, err := ac.resolve(strings.ToLower(name))
		if err != nil {
			return nil, fmt.Errorf("ampli %q : %w", name, err)
		}
		if c.amps == nil {
			c.amps = map[string]audioprofile.TubeAmpSettings{}
		}
		c.amps[strings.ToLower(name)] = s
	}
	for _, name := range sortedKeys(f.FX) {
		d, err := fxDefaultsFor(strings.ToLower(name), f.FX[name])
		if err != nil {
			return nil, fmt.Errorf("fx %q : %w", name, err)
		}
		if c.fxDefaults == nil {
			c.fxDefaults = map[string]map[string]float64{}
		}
		c.fxDefaults[strings.ToLower(name)] = d
	}
	return c, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Profile renvoie les réglages du profil : ceux du fichier s'il y est, sinon le profil prédéfini.
func (c *Config) Profile(name string) (audioprofile.ProfileSettings, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if c != nil {
		if s, ok := c.profiles[name]; ok {
			return s, true
		}
	}
	if p, ok := audioprofile.ParseProfile(name); ok {
		return p.Settings(), true
	}
	return audioprofile.ProfileSettings{}, false
}

// Amp renvoie les réglages du préréglage d'ampli (à intensité 1, baffle compris).
func (c *Config) Amp(name string) (audioprofile.TubeAmpSettings, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if c != nil {
		if s, ok := c.amps[name]; ok {
			return s, true
		}
	}
	if p, ok := audioprofile.ParseTubeAmpPreset(name); ok {
		return p.Settings(), true
	}
	return audioprofile.TubeAmpSettings{}, false
}

// ProfileNames liste les profils prédéfinis, puis ceux que seul le fichier définit.
func (c *Config) ProfileNames() []string {
	var names []string
	for _, p := range audioprofile.Profiles() {
		names = append(names, p.String())
	}
	if c != nil {
		for _, n := range sortedKeys(c.profiles) {
			if _, builtin := audioprofile.ParseProfile(n); !builtin {
				names = append(names, n)
			}
		}
	}
	return names
}

// AmpNames liste les préréglages d'ampli prédéfinis, puis ceux que seul le fichier définit.
func (c *Config) AmpNames() []string {
	var names []string
	for _, p := range audioprofile.TubeAmpPresets() {
		names = append(names, p.String())
	}
	if c != nil {
		for _, n := range sortedKeys(c.amps) {
			if _, builtin := audioprofile.ParseTubeAmpPreset(n); !builtin {
				names = append(names, n)
			}
		}
	}
	return names
}

// Validate vérifie les profils et amplis du fichier pour une fréquence d'échantillonnage donnée.
func (c *Config) Validate(rate int) error {
	if c == nil {
		return nil
	}
	for _, n := range sortedKeys(c.profiles) {
		if err := ValidateProfile(c.profiles[n], rate); err != nil {
			return fmt.Errorf("config : profil %q : %w", n, err)
		}
	}
	for _, n := range sortedKeys(c.amps) {
		if err := ValidateAmp(c.amps[n]); err != nil {
			return fmt.Errorf("config : ampli %q : %w", n, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- fusion avec les valeurs par défaut

func (pc *profileConfig) resolve(name string) (audioprofile.ProfileSettings, error) {
	var s audioprofile.ProfileSettings
	base := strings.ToLower(strings.TrimSpace(pc.Base))
	if base == "" {
		base = name
	}
	if p, ok := audioprofile.ParseProfile(base); ok {
		s = p.Settings()
	} else if pc.Base != "" {
		return s, fmt.Errorf("base %q inconnue (profils prédéfinis : %s)", pc.Base, strings.Join((*Config)(nil).ProfileNames(), ", "))
	} // sinon : un profil nouveau part de rien
	if pc.Bands != nil {
		s.Bands = nil
		for i, b := range *pc.Bands {
			t, ok := audioprofile.ParseBandType(b.Type)
			if !ok {
				return s, fmt.Errorf("bands[%d] : type %q inconnu (types : %s)", i, b.Type, bandTypeList())
			}
			s.Bands = append(s.Bands, audioprofile.Band{Type: t, FreqHz: b.Freq, GainDB: b.Gain, Q: b.Q, Order: b.Order})
		}
	}
	s.Compression = pc.Compression.applyTo(s.Compression)
	if sc := pc.Saturation; sc != nil {
		if sc.Enabled != nil && !*sc.Enabled {
			s.Saturation = audioprofile.SaturationSettings{}
		} else {
			set(&s.Saturation.DriveDB, sc.Drive)
			set(&s.Saturation.Bias, sc.Bias)
			set(&s.Saturation.OutputDB, sc.Output)
			if sc.Stages != nil {
				s.Saturation.Stages = *sc.Stages
			}
		}
	}
	if mc := pc.Multiband; mc != nil {
		var err error
		if s.Multiband, err = mc.applyTo(s.Multiband); err != nil {
			return s, fmt.Errorf("multiband : %w", err)
		}
	}
	set(&s.Width, pc.Width)
	if lc := pc.Limiter; lc != nil {
		if lc.Enabled != nil && !*lc.Enabled {
			s.Limiter = nil
		} else {
			l := limiterStart
			if s.Limiter != nil {
				l = *s.Limiter
			}
			set(&l.CeilingDB, lc.Ceiling)
			set(&l.LookaheadMs, lc.Lookahead)
			set(&l.ReleaseMs, lc.Release)
			s.Limiter = &l
		}
	}
	return s, nil
}

// compressorStart est le point de départ d'un compresseur que le profil de base n'a pas.
var compressorStart = audioprofile.CompressorSettings{ThresholdDB: -20, Ratio: 2, AttackMs: 10, ReleaseMs: 100, KneeDB: 6}

// StartCompressor renvoie les valeurs d'un compresseur que le fichier active pour un profil qui n'en a pas.
func StartCompressor() audioprofile.CompressorSettings { return compressorStart }

// limiterStart est le point de départ d'un limiteur que le profil de base n'a pas.
var limiterStart = audioprofile.LimiterSettings{CeilingDB: -1, LookaheadMs: 5, ReleaseMs: 100}

// StartLimiter renvoie les valeurs d'un limiteur que le fichier active pour un profil qui n'en a pas.
func StartLimiter() audioprofile.LimiterSettings { return limiterStart }

func (cc compressorConfig) merge(c audioprofile.CompressorSettings) audioprofile.CompressorSettings {
	set(&c.ThresholdDB, cc.Threshold)
	set(&c.Ratio, cc.Ratio)
	set(&c.AttackMs, cc.Attack)
	set(&c.ReleaseMs, cc.Release)
	set(&c.KneeDB, cc.Knee)
	set(&c.MakeupDB, cc.Makeup)
	return c
}

// applyTo fusionne avec le compresseur de base (nil : aucun) ; enabled: false le supprime.
func (cc *compressorConfig) applyTo(base *audioprofile.CompressorSettings) *audioprofile.CompressorSettings {
	if cc == nil {
		return base
	}
	if cc.Enabled != nil && !*cc.Enabled {
		return nil
	}
	c := compressorStart
	if base != nil {
		c = *base
	}
	c = cc.merge(c)
	return &c
}

func (mc *multibandConfig) applyTo(base *audioprofile.MultibandSettings) (*audioprofile.MultibandSettings, error) {
	if mc.Enabled != nil && !*mc.Enabled {
		return nil, nil
	}
	var m audioprofile.MultibandSettings
	if base != nil {
		m = *base
	}
	if mc.Crossovers != nil {
		m.Crossovers = append([]float64(nil), *mc.Crossovers...)
	}
	if mc.Bands != nil {
		given := *mc.Bands
		// Les bandes qu'on ne nomme pas gardent leur valeur de départ : on complète la liste avec celles du profil de
		// base, de même rang, jusqu'au nombre de bandes que demandent les séparations. Une bande de plus que le
		// profil n'en a n'a rien à garder : si elle manque, la validation signale le nombre de bandes attendu.
		count := len(given)
		if want := len(m.Crossovers) + 1; base != nil && count < want {
			count = min(want, max(count, len(base.Bands)))
		}
		bands := make([]audioprofile.CompressorSettings, count)
		for i := range bands {
			start := compressorStart
			if base != nil && i < len(base.Bands) {
				start = base.Bands[i]
			}
			bands[i] = start
			if i < len(given) {
				if given[i].Enabled != nil {
					return nil, fmt.Errorf("bands[%d] : enabled n'a pas de sens pour une bande (utilisez ratio: 1 et makeup: 0)", i)
				}
				bands[i] = given[i].merge(start)
			}
		}
		m.Bands = bands
	}
	return &m, nil
}

func (ac *ampConfig) resolve(name string) (audioprofile.TubeAmpSettings, error) {
	var s audioprofile.TubeAmpSettings
	base := strings.ToLower(strings.TrimSpace(ac.Base))
	if base == "" {
		base = name
	}
	if p, ok := audioprofile.ParseTubeAmpPreset(base); ok {
		s = p.Settings()
	} else if ac.Base != "" {
		return s, fmt.Errorf("base %q inconnue (préréglages prédéfinis : %s)", ac.Base, strings.Join((*Config)(nil).AmpNames(), ", "))
	}
	set(&s.DriveDB, ac.Drive)
	if ac.Stages != nil {
		s.Stages = *ac.Stages
	}
	set(&s.Bias, ac.Bias)
	set(&s.BassDB, ac.Bass)
	set(&s.MidDB, ac.Mid)
	set(&s.TrebleDB, ac.Treble)
	if ac.Cabinet != nil {
		s.Cabinet = *ac.Cabinet
	}
	set(&s.OutputDB, ac.Output)
	return s, nil
}

func set(dst *float64, v *float64) {
	if v != nil {
		*dst = *v
	}
}

// fxDefaultsFor vérifie les valeurs par défaut d'un effet : l'effet et chaque clé doivent exister,
// une valeur est un nombre ou un booléen (true = 1, false = 0).
func fxDefaultsFor(name string, raw map[string]any) (map[string]float64, error) {
	def := fx.Lookup(name)
	if def == nil {
		return nil, fmt.Errorf("effet inconnu (effets : %s)", strings.Join(fx.Names(), ", "))
	}
	known := map[string]bool{}
	var keys []string
	for _, p := range def.Params {
		known[p.Key] = true
		keys = append(keys, p.Key)
	}
	out := map[string]float64{}
	for _, k := range sortedKeys(raw) {
		key := strings.ToLower(k)
		if !known[key] {
			return nil, fmt.Errorf("paramètre inconnu %q (paramètres : %s)", k, strings.Join(keys, ", "))
		}
		switch v := raw[k].(type) {
		case int:
			out[key] = float64(v)
		case float64:
			out[key] = v
		case bool:
			out[key] = map[bool]float64{false: 0, true: 1}[v]
		default:
			return nil, fmt.Errorf("%s : %v n'est pas un nombre", k, raw[k])
		}
		if f := out[key]; math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%s : %v n'est pas un nombre fini", k, raw[k])
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- validation

func finite(vals ...float64) bool {
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func checkCompressor(c audioprofile.CompressorSettings) error {
	if !finite(c.ThresholdDB, c.Ratio, c.AttackMs, c.ReleaseMs, c.KneeDB, c.MakeupDB) {
		return errors.New("valeur non finie")
	}
	if c.ThresholdDB < -80 || c.ThresholdDB > 0 {
		return fmt.Errorf("threshold doit être dans [-80, 0] dBFS (reçu %g)", c.ThresholdDB)
	}
	if c.Ratio < 0 || c.Ratio > 100 {
		return fmt.Errorf("ratio doit être dans [0, 100] (reçu %g ; <= 1 : pas de compression)", c.Ratio)
	}
	if c.AttackMs < 0 || c.ReleaseMs < 0 || c.KneeDB < 0 {
		return errors.New("attack, release et knee doivent être >= 0")
	}
	return nil
}

// ValidateProfile vérifie des réglages de profil pour une fréquence d'échantillonnage : valeurs finies et
// dans leurs bornes, fréquences sous Nyquist, multibande cohérente.
func ValidateProfile(s audioprofile.ProfileSettings, rate int) error {
	nyq := float64(rate) / 2
	for i, b := range s.Bands {
		if !finite(b.FreqHz, b.GainDB, b.Q) {
			return fmt.Errorf("bands[%d] : valeur non finie", i)
		}
		if math.Abs(b.GainDB) > 24 {
			return fmt.Errorf("bands[%d] : gain doit être dans [-24, 24] dB (reçu %g)", i, b.GainDB)
		}
		if b.Type != audioprofile.Gain && (b.FreqHz <= 0 || b.FreqHz >= nyq) {
			return fmt.Errorf("bands[%d] (%s) : freq doit être dans ]0, %g[ Hz (reçu %g)", i, b.Type.String(), nyq, b.FreqHz)
		}
		if b.Q < 0 || b.Order < 0 || b.Order > 8 {
			return fmt.Errorf("bands[%d] : q doit être >= 0 et order dans [0, 8]", i)
		}
	}
	if s.Compression != nil {
		if err := checkCompressor(*s.Compression); err != nil {
			return fmt.Errorf("compression : %w", err)
		}
	}
	sat := s.Saturation
	if !finite(sat.DriveDB, sat.Bias, sat.OutputDB) {
		return errors.New("saturation : valeur non finie")
	}
	switch {
	case sat.DriveDB < 0:
		return fmt.Errorf("saturation : drive doit être >= 0 (reçu %g)", sat.DriveDB)
	case sat.Bias < 0 || sat.Bias > 1:
		return fmt.Errorf("saturation : bias doit être dans [0, 1] (reçu %g)", sat.Bias)
	case sat.Stages < 0 || sat.Stages > 4:
		return fmt.Errorf("saturation : stages doit être dans [0, 4] (reçu %d)", sat.Stages)
	}
	if m := s.Multiband; m != nil {
		if len(m.Bands) != len(m.Crossovers)+1 {
			return fmt.Errorf("multiband : %d séparations demandent %d bandes (reçu %d)", len(m.Crossovers), len(m.Crossovers)+1, len(m.Bands))
		}
		prev := 0.0
		for _, f := range m.Crossovers {
			if !finite(f) || f <= prev || f >= nyq {
				return fmt.Errorf("multiband : crossovers doivent croître strictement, dans ]0, %g[ Hz", nyq)
			}
			prev = f
		}
		for i, c := range m.Bands {
			if err := checkCompressor(c); err != nil {
				return fmt.Errorf("multiband bands[%d] : %w", i, err)
			}
		}
	}
	if !finite(s.Width) || s.Width < 0 || s.Width > audioprofile.MaxWidth {
		return fmt.Errorf("width doit être dans [0, %g] (reçu %g ; 0 : pas d'élargissement)", audioprofile.MaxWidth, s.Width)
	}
	if l := s.Limiter; l != nil {
		if !finite(l.CeilingDB, l.LookaheadMs, l.ReleaseMs) || l.CeilingDB < -40 || l.CeilingDB > 0 || l.LookaheadMs < 0 || l.LookaheadMs > 50 || l.ReleaseMs < 0 {
			return errors.New("limiter : ceiling dans [-40, 0] dBFS, lookahead dans [0, 50] ms, release >= 0")
		}
	}
	return nil
}

// ValidateAmp vérifie des réglages d'ampli.
func ValidateAmp(s audioprofile.TubeAmpSettings) error {
	if !finite(s.DriveDB, s.Bias, s.BassDB, s.MidDB, s.TrebleDB, s.OutputDB) {
		return errors.New("valeur non finie")
	}
	switch {
	case s.DriveDB < 0:
		return fmt.Errorf("drive doit être >= 0 (reçu %g)", s.DriveDB)
	case s.Bias < 0 || s.Bias > 1:
		return fmt.Errorf("bias doit être dans [0, 1] (reçu %g)", s.Bias)
	case s.Stages < 0 || s.Stages > 4:
		return fmt.Errorf("stages doit être dans [0, 4] (reçu %d)", s.Stages)
	}
	return nil
}

// ---------------------------------------------------------------- export

func ptr[T any](v T) *T { return &v }

func compressorToConfig(c audioprofile.CompressorSettings) *compressorConfig {
	return &compressorConfig{Threshold: ptr(c.ThresholdDB), Ratio: ptr(c.Ratio), Attack: ptr(c.AttackMs), Release: ptr(c.ReleaseMs), Knee: ptr(c.KneeDB), Makeup: ptr(c.MakeupDB)}
}

// profileToConfig écrit un profil en entier. Un étage absent mais présent dans la base prédéfinie du
// même nom est écrit « enabled: false », pour que le fichier se relise à l'identique.
func profileToConfig(name string, s audioprofile.ProfileSettings) *profileConfig {
	var base audioprofile.ProfileSettings
	if p, ok := audioprofile.ParseProfile(name); ok {
		base = p.Settings()
	}
	off := &compressorConfig{Enabled: ptr(false)}
	pc := &profileConfig{}
	if len(s.Bands) > 0 || len(base.Bands) > 0 {
		bands := make([]bandConfig, 0, len(s.Bands))
		for _, b := range s.Bands {
			bands = append(bands, bandConfig{Type: b.Type.String(), Freq: b.FreqHz, Gain: b.GainDB, Q: b.Q, Order: b.Order})
		}
		pc.Bands = &bands
	}
	switch {
	case s.Compression != nil:
		pc.Compression = compressorToConfig(*s.Compression)
	case base.Compression != nil:
		pc.Compression = off
	}
	switch {
	case s.Saturation != audioprofile.SaturationSettings{}:
		pc.Saturation = &saturationConfig{Drive: ptr(s.Saturation.DriveDB), Bias: ptr(s.Saturation.Bias), Output: ptr(s.Saturation.OutputDB), Stages: ptr(s.Saturation.Stages)}
	case base.Saturation != audioprofile.SaturationSettings{}:
		pc.Saturation = &saturationConfig{Enabled: ptr(false)}
	}
	switch {
	case s.Multiband != nil:
		bands := make([]compressorConfig, len(s.Multiband.Bands))
		for i, c := range s.Multiband.Bands {
			bands[i] = *compressorToConfig(c)
		}
		pc.Multiband = &multibandConfig{Crossovers: ptr(append([]float64{}, s.Multiband.Crossovers...)), Bands: &bands}
	case base.Multiband != nil:
		pc.Multiband = &multibandConfig{Enabled: ptr(false)}
	}
	if s.Width != 0 || base.Width != 0 {
		pc.Width = ptr(s.Width)
	}
	switch {
	case s.Limiter != nil:
		pc.Limiter = &limiterConfig{Ceiling: ptr(s.Limiter.CeilingDB), Lookahead: ptr(s.Limiter.LookaheadMs), Release: ptr(s.Limiter.ReleaseMs)}
	case base.Limiter != nil:
		pc.Limiter = &limiterConfig{Enabled: ptr(false)}
	}
	return pc
}

func ampToConfig(s audioprofile.TubeAmpSettings) *ampConfig {
	return &ampConfig{Drive: ptr(s.DriveDB), Stages: ptr(s.Stages), Bias: ptr(s.Bias), Bass: ptr(s.BassDB), Mid: ptr(s.MidDB),
		Treble: ptr(s.TrebleDB), Cabinet: ptr(s.Cabinet), Output: ptr(s.OutputDB)}
}

// Dump écrit, en YAML, les réglages effectifs (valeurs du fichier comprises) de tous les profils
// et préréglages d'ampli, et les valeurs par défaut des effets : un point de départ complet à éditer.
func (c *Config) Dump(w io.Writer) error {
	f := configFile{Profiles: map[string]*profileConfig{}, Amps: map[string]*ampConfig{}, FX: map[string]map[string]any{}}
	for _, n := range c.ProfileNames() {
		s, _ := c.Profile(n)
		f.Profiles[n] = profileToConfig(n, s)
	}
	for _, n := range c.AmpNames() {
		s, _ := c.Amp(n)
		f.Amps[n] = ampToConfig(s)
	}
	for _, d := range fx.Defs() {
		m := map[string]any{}
		for _, p := range d.Params {
			if v, ok := c.FXDefaults()[d.Name][p.Key]; ok {
				m[p.Key] = v
			} else if !p.Required {
				m[p.Key] = p.Default
			}
		}
		if len(m) > 0 {
			f.FX[d.Name] = m
		}
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return err
	}
	return enc.Close()
}

// MarshalProfile écrit un profil au format du fichier de réglages (sous la clé profiles), pour le coller dans le
// fichier. Un profil prédéfini est écrit en entier, étages supprimés compris (« enabled: false »).
func MarshalProfile(name string, s audioprofile.ProfileSettings) ([]byte, error) {
	name = strings.ToLower(name)
	return marshal(configFile{Profiles: map[string]*profileConfig{name: profileToConfig(name, s)}})
}

// MarshalAmp écrit un préréglage d'ampli au format du fichier de réglages (sous la clé amps).
func MarshalAmp(name string, s audioprofile.TubeAmpSettings) ([]byte, error) {
	return marshal(configFile{Amps: map[string]*ampConfig{strings.ToLower(name): ampToConfig(s)}})
}

func marshal(f configFile) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
