package config

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/fx"
)

func parseCfg(t *testing.T, yml string) *Config {
	t.Helper()
	c, err := Parse(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("parseConfig : %v", err)
	}
	return c
}

// Un fichier vide ou absent ne change aucun réglage.
func TestConfigEmptyKeepsDefaults(t *testing.T) {
	for name, c := range map[string]*Config{"vide": parseCfg(t, ""), "commentaires": parseCfg(t, "# rien\n"), "nil": nil} {
		for _, p := range audioprofile.Profiles() {
			got, ok := c.Profile(p.String())
			if !ok || !reflect.DeepEqual(got, p.Settings()) {
				t.Errorf("%s : profil %s modifié", name, p)
			}
		}
		for _, a := range audioprofile.TubeAmpPresets() {
			if got, ok := c.Amp(a.String()); !ok || got != a.Settings() {
				t.Errorf("%s : ampli %s modifié", name, a)
			}
		}
	}
}

// -dump-config relu donne exactement les mêmes réglages : le schéma YAML ne perd rien.
func TestConfigDumpRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := (&Config{}).Dump(&buf); err != nil {
		t.Fatal(err)
	}
	c := parseCfg(t, buf.String())
	for _, p := range audioprofile.Profiles() {
		got, _ := c.Profile(p.String())
		if !reflect.DeepEqual(got, p.Settings()) {
			t.Errorf("profil %s : %+v, attendu %+v", p, got, p.Settings())
		}
	}
	for _, a := range audioprofile.TubeAmpPresets() {
		if got, _ := c.Amp(a.String()); got != a.Settings() {
			t.Errorf("ampli %s : %+v, attendu %+v", a, got, a.Settings())
		}
	}
	if err := c.Validate(44100); err != nil {
		t.Errorf("les valeurs par défaut doivent être valides : %v", err)
	}
}

// Le dump d'une configuration modifiée se relit à l'identique, y compris les étages supprimés.
func TestConfigDumpRoundTripWithOverrides(t *testing.T) {
	c := parseCfg(t, `
profiles:
  radio-hits:
    limiter: {enabled: false}
    multiband: {enabled: false}
    width: 0
    compression: {enabled: false}
  rock:
    bands: []
    saturation: {enabled: false}
  mon-profil:
    base: jazz
    width: 1.3
`)
	var buf bytes.Buffer
	if err := c.Dump(&buf); err != nil {
		t.Fatal(err)
	}
	c2 := parseCfg(t, buf.String())
	for _, n := range c.ProfileNames() {
		a, _ := c.Profile(n)
		b, _ := c2.Profile(n)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("profil %s : %+v, attendu %+v", n, b, a)
		}
	}
}

func TestConfigPartialOverride(t *testing.T) {
	c := parseCfg(t, `
profiles:
  Rock:
    compression: {ratio: 6}
    saturation: {drive: 9}
  radio-hits:
    width: 1.5
    limiter: {ceiling: -2}
    multiband:
      bands:
        - {ratio: 4}
amps:
  crunch: {drive: 12, cabinet: true}
`)
	def := audioprofile.Rock.Settings()
	got, _ := c.Profile("rock")
	if got.Compression.Ratio != 6 || got.Compression.ThresholdDB != def.Compression.ThresholdDB || got.Compression.ReleaseMs != def.Compression.ReleaseMs {
		t.Errorf("compression : %+v (le reste doit garder %+v)", got.Compression, def.Compression)
	}
	if got.Saturation.DriveDB != 9 || got.Saturation.OutputDB != def.Saturation.OutputDB {
		t.Errorf("saturation : %+v", got.Saturation)
	}
	if !reflect.DeepEqual(got.Bands, def.Bands) {
		t.Error("les bandes non précisées doivent rester celles du profil")
	}
	hits, _ := c.Profile("radio-hits")
	hdef := audioprofile.RadioHits.Settings()
	if hits.Width != 1.5 || hits.Limiter.CeilingDB != -2 || hits.Limiter.ReleaseMs != hdef.Limiter.ReleaseMs {
		t.Errorf("radio-hits : width %g, limiteur %+v", hits.Width, hits.Limiter)
	}
	if len(hits.Multiband.Bands) != 1 || hits.Multiband.Bands[0].Ratio != 4 || hits.Multiband.Bands[0].ThresholdDB != hdef.Multiband.Bands[0].ThresholdDB {
		t.Errorf("multiband fusionné bande par bande : %+v", hits.Multiband.Bands)
	}
	if err := c.Validate(44100); err == nil {
		t.Error("une seule bande pour trois séparations doit être refusée")
	}
	amp, _ := c.Amp("crunch")
	adef := audioprofile.AmpCrunch.Settings()
	if amp.DriveDB != 12 || !amp.Cabinet || amp.BassDB != adef.BassDB || amp.Stages != adef.Stages {
		t.Errorf("ampli : %+v", amp)
	}
	// le paquet n'est pas modifié
	if !reflect.DeepEqual(audioprofile.Rock.Settings(), def) {
		t.Error("la configuration a modifié les valeurs du paquet")
	}
}

func TestConfigNewProfile(t *testing.T) {
	c := parseCfg(t, `
profiles:
  doux:
    bands:
      - {type: highshelf, freq: 8000, gain: -3}
  chaud:
    base: Jazz
    saturation: {drive: 5}
`)
	doux, ok := c.Profile("doux")
	if !ok || len(doux.Bands) != 1 || doux.Bands[0].Type != audioprofile.HighShelf || doux.Compression != nil || doux.Limiter != nil {
		t.Errorf("profil neuf : %+v", doux)
	}
	chaud, _ := c.Profile("chaud")
	if !reflect.DeepEqual(chaud.Bands, audioprofile.Jazz.Settings().Bands) || chaud.Saturation.DriveDB != 5 {
		t.Errorf("profil sur base : %+v", chaud)
	}
	names := strings.Join(c.ProfileNames(), " ")
	if !strings.Contains(names, "rock") || !strings.HasSuffix(names, "chaud doux") {
		t.Errorf("profileNames : %s", names)
	}
}

func TestConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct{ yml, want string }{
		"clé inconnue":      {"profiles:\n  rock:\n    compresion: {ratio: 2}\n", "compresion"},
		"clé de section":    {"profiles:\n  rock:\n    compression: {ratioo: 2}\n", "ratioo"},
		"section inconnue":  {"profils: {}\n", "profils"},
		"base inconnue":     {"profiles:\n  x:\n    base: nope\n", "base"},
		"type de bande":     {"profiles:\n  x:\n    bands:\n      - {type: shelf, freq: 100}\n", "shelf"},
		"effet inconnu":     {"fx:\n  reverbe: {mix: 1}\n", "reverbe"},
		"paramètre inconnu": {"fx:\n  delay: {temps: 1}\n", "temps"},
		"pas un nombre":     {"fx:\n  delay: {time: beaucoup}\n", "pas un nombre"},
		"yaml invalide":     {"profiles: [\n", "yaml"},
		"enabled de bande":  {"profiles:\n  radio-hits:\n    multiband:\n      bands:\n        - {enabled: false}\n", "enabled"},
	} {
		_, err := Parse(strings.NewReader(tc.yml))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : erreur %v, attendu qu'elle contienne %q", name, err, tc.want)
		}
	}
	for name, tc := range map[string]struct{ yml, want string }{
		"gain":       {"profiles:\n  x:\n    bands:\n      - {type: peaking, freq: 100, gain: 40}\n", "gain"},
		"fréquence":  {"profiles:\n  x:\n    bands:\n      - {type: peaking, freq: 30000, gain: 1}\n", "freq"},
		"ratio":      {"profiles:\n  rock:\n    compression: {ratio: 500}\n", "ratio"},
		"largeur":    {"profiles:\n  x:\n    width: 9\n    bands:\n      - {type: gain, gain: 1}\n", "width"},
		"limiteur":   {"profiles:\n  x:\n    limiter: {ceiling: 3}\n", "limiter"},
		"saturation": {"profiles:\n  x:\n    saturation: {drive: 3, bias: 2}\n", "saturation"},
		"ampli":      {"amps:\n  clean: {bias: 5}\n", "ampli"},
		"nan":        {"profiles:\n  x:\n    bands:\n      - {type: peaking, freq: .nan}\n", "x"},
	} {
		c := parseCfg(t, tc.yml)
		if err := c.Validate(44100); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : erreur %v, attendu qu'elle contienne %q", name, err, tc.want)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("un fichier absent doit être une erreur (le chemin est explicite)")
	}
}

func TestConfigFXDefaults(t *testing.T) {
	c := parseCfg(t, "fx:\n  delay: {time: 350, pingpong: true}\n  hpf: {freq: 80}\n")
	d := c.FXDefaults()
	if d["delay"]["time"] != 350 || d["delay"]["pingpong"] != 1 || d["hpf"]["freq"] != 80 {
		t.Errorf("valeurs par défaut lues : %v", d)
	}
	// ce que fx.Parse en fait : la ligne de commande l'emporte, et un défaut rend freq facultatif
	e, err := fx.Parse("delay:feedback=0.2", d)
	if err != nil || e.Vals["time"] != 350 || e.Vals["pingpong"] != 1 || e.Vals["feedback"] != 0.2 {
		t.Errorf("delay : %v %v", err, e.Vals)
	}
	if e, _ = fx.Parse("delay:time=100", d); e.Vals["time"] != 100 {
		t.Error("la valeur donnée doit l'emporter sur le fichier")
	}
	if e, err = fx.Parse("hpf", d); err != nil || e.Vals["freq"] != 80 {
		t.Errorf("un défaut du fichier rend le paramètre facultatif : %v, %v", e.Vals, err)
	}
	if (*Config)(nil).FXDefaults() != nil || parseCfg(t, "").FXDefaults() != nil {
		t.Error("sans fichier, pas de valeurs par défaut")
	}
}

// Les valeurs de départ d'un étage activé sont publiques, pour que le panneau en montre les mêmes.
func TestStartValues(t *testing.T) {
	if c := StartCompressor(); c.Ratio != 2 || c.ThresholdDB != -20 || c.KneeDB != 6 {
		t.Errorf("StartCompressor : %+v", c)
	}
	c := parseCfg(t, "profiles:\n  x:\n    limiter: {ceiling: -3}\n    compression: {ratio: 5}\n")
	x, _ := c.Profile("x")
	if x.Limiter.LookaheadMs != StartLimiter().LookaheadMs || x.Limiter.ReleaseMs != StartLimiter().ReleaseMs || x.Compression.ThresholdDB != StartCompressor().ThresholdDB {
		t.Errorf("un étage activé part des valeurs de départ : %+v %+v", x.Limiter, x.Compression)
	}
}

// MarshalProfile et MarshalAmp écrivent ce que Parse relit.
func TestMarshalRoundTrip(t *testing.T) {
	ps := audioprofile.RadioHits.Settings()
	ps.Width = 1.5
	b, err := MarshalProfile("Mon-Profil", ps)
	if err != nil || !strings.HasPrefix(string(b), "profiles:\n  mon-profil:") {
		t.Fatalf("MarshalProfile : %v\n%s", err, b)
	}
	c := parseCfg(t, string(b))
	if got, ok := c.Profile("mon-profil"); !ok || !reflect.DeepEqual(got, ps) {
		t.Errorf("profil relu : %+v", got)
	}
	as := audioprofile.AmpCrunch.Settings()
	as.Cabinet = true
	b, err = MarshalAmp("crunch", as)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := parseCfg(t, string(b)).Amp("crunch"); got != as {
		t.Errorf("ampli relu : %+v", got)
	}
}
