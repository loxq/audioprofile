package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loxq/audioprofile"
)

func parseCfg(t *testing.T, yml string) *config {
	t.Helper()
	c, err := parseConfig(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("parseConfig : %v", err)
	}
	return c
}

func writeCfg(t *testing.T, yml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reglages.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Un fichier vide ou absent ne change aucun réglage.
func TestConfigEmptyKeepsDefaults(t *testing.T) {
	for name, c := range map[string]*config{"vide": parseCfg(t, ""), "commentaires": parseCfg(t, "# rien\n"), "nil": nil} {
		for _, p := range audioprofile.Profiles() {
			got, ok := c.profile(p.String())
			if !ok || !reflect.DeepEqual(got, p.Settings()) {
				t.Errorf("%s : profil %s modifié", name, p)
			}
		}
		for _, a := range audioprofile.TubeAmpPresets() {
			if got, ok := c.amp(a.String()); !ok || got != a.Settings() {
				t.Errorf("%s : ampli %s modifié", name, a)
			}
		}
	}
}

// -dump-config relu donne exactement les mêmes réglages : le schéma YAML ne perd rien.
func TestConfigDumpRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := (&config{}).dumpConfig(&buf); err != nil {
		t.Fatal(err)
	}
	c := parseCfg(t, buf.String())
	for _, p := range audioprofile.Profiles() {
		got, _ := c.profile(p.String())
		if !reflect.DeepEqual(got, p.Settings()) {
			t.Errorf("profil %s : %+v, attendu %+v", p, got, p.Settings())
		}
	}
	for _, a := range audioprofile.TubeAmpPresets() {
		if got, _ := c.amp(a.String()); got != a.Settings() {
			t.Errorf("ampli %s : %+v, attendu %+v", a, got, a.Settings())
		}
	}
	if err := c.validate(44100); err != nil {
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
	if err := c.dumpConfig(&buf); err != nil {
		t.Fatal(err)
	}
	c2 := parseCfg(t, buf.String())
	for _, n := range c.profileNames() {
		a, _ := c.profile(n)
		b, _ := c2.profile(n)
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
	got, _ := c.profile("rock")
	if got.Compression.Ratio != 6 || got.Compression.ThresholdDB != def.Compression.ThresholdDB || got.Compression.ReleaseMs != def.Compression.ReleaseMs {
		t.Errorf("compression : %+v (le reste doit garder %+v)", got.Compression, def.Compression)
	}
	if got.Saturation.DriveDB != 9 || got.Saturation.OutputDB != def.Saturation.OutputDB {
		t.Errorf("saturation : %+v", got.Saturation)
	}
	if !reflect.DeepEqual(got.Bands, def.Bands) {
		t.Error("les bandes non précisées doivent rester celles du profil")
	}
	hits, _ := c.profile("radio-hits")
	hdef := audioprofile.RadioHits.Settings()
	if hits.Width != 1.5 || hits.Limiter.CeilingDB != -2 || hits.Limiter.ReleaseMs != hdef.Limiter.ReleaseMs {
		t.Errorf("radio-hits : width %g, limiteur %+v", hits.Width, hits.Limiter)
	}
	if len(hits.Multiband.Bands) != 1 || hits.Multiband.Bands[0].Ratio != 4 || hits.Multiband.Bands[0].ThresholdDB != hdef.Multiband.Bands[0].ThresholdDB {
		t.Errorf("multiband fusionné bande par bande : %+v", hits.Multiband.Bands)
	}
	if err := c.validate(44100); err == nil {
		t.Error("une seule bande pour trois séparations doit être refusée")
	}
	amp, _ := c.amp("crunch")
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
	doux, ok := c.profile("doux")
	if !ok || len(doux.Bands) != 1 || doux.Bands[0].Type != audioprofile.HighShelf || doux.Compression != nil || doux.Limiter != nil {
		t.Errorf("profil neuf : %+v", doux)
	}
	chaud, _ := c.profile("chaud")
	if !reflect.DeepEqual(chaud.Bands, audioprofile.Jazz.Settings().Bands) || chaud.Saturation.DriveDB != 5 {
		t.Errorf("profil sur base : %+v", chaud)
	}
	names := strings.Join(c.profileNames(), " ")
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
		_, err := parseConfig(strings.NewReader(tc.yml))
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
		if err := c.validate(44100); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : erreur %v, attendu qu'elle contienne %q", name, err, tc.want)
		}
	}
	if _, err := loadConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("un fichier absent doit être une erreur (le chemin est explicite)")
	}
}

func TestConfigFXDefaults(t *testing.T) {
	c := parseCfg(t, "fx:\n  delay: {time: 350, pingpong: true}\n  hpf: {freq: 80}\n")
	e, err := parseFX("delay:feedback=0.2", c.fxDefaults)
	if err != nil {
		t.Fatal(err)
	}
	if e.vals["time"] != 350 || e.vals["pingpong"] != 1 || e.vals["feedback"] != 0.2 {
		t.Errorf("delay : %v (défaut du fichier, booléen, ligne de commande)", e.vals)
	}
	if e, _ = parseFX("delay:time=100", c.fxDefaults); e.vals["time"] != 100 {
		t.Error("la ligne de commande doit l'emporter sur le fichier")
	}
	if e, err = parseFX("hpf", c.fxDefaults); err != nil || e.vals["freq"] != 80 {
		t.Errorf("un défaut du fichier rend le paramètre facultatif : %v, %v", e.vals, err)
	}
	if _, err = parseFX("hpf", nil); err == nil {
		t.Error("sans fichier, freq reste obligatoire")
	}
}

// ---------------------------------------------------------------- ligne de commande

func TestCLIConfigChangesOutput(t *testing.T) {
	in := pcm(60, 20000, 2)
	base, _, err := runCLI(t, in, "-profile", "rock")
	if err != nil {
		t.Fatal(err)
	}
	same, _, err := runCLI(t, in, "-config", writeCfg(t, "# vide\n"), "-profile", "rock")
	if err != nil || same != base {
		t.Errorf("un fichier vide ne doit rien changer (err %v)", err)
	}
	changed, _, err := runCLI(t, in, "-config", writeCfg(t, "profiles:\n  rock:\n    bands:\n      - {type: lowshelf, freq: 100, gain: -6}\n"), "-profile", "rock")
	if err != nil || changed == base {
		t.Errorf("la configuration doit changer le son (err %v)", err)
	}
	// le profil neuf est utilisable, listé, et se règle comme les autres
	cfg := writeCfg(t, "profiles:\n  mon-rock:\n    base: rock\n    width: 0\n    bands:\n      - {type: lowshelf, freq: 100, gain: -6}\n")
	mine, _, err := runCLI(t, in, "-config", cfg, "-profile", "mon-rock")
	if err != nil || mine != changed {
		t.Errorf("mon-rock doit valoir le rock modifié (err %v)", err)
	}
	if list, _, _ := runCLI(t, nil, "-config", cfg, "-list"); !strings.Contains(list, "mon-rock\n") || !strings.Contains(list, "rock\n") {
		t.Errorf("-list : %q", list)
	}
	if _, _, err = runCLI(t, in, "-profile", "mon-rock"); err == nil || !strings.Contains(err.Error(), "inconnu") {
		t.Errorf("sans le fichier, mon-rock est inconnu (err %v)", err)
	}
}

func TestCLIConfigAmpAndFX(t *testing.T) {
	in := pcm(200, 20000, 2)
	cfg := writeCfg(t, "amps:\n  clean: {drive: 14}\nfx:\n  lpf: {freq: 500}\n")
	base, _, _ := runCLI(t, in, "-amp", "clean")
	loud, _, err := runCLI(t, in, "-config", cfg, "-amp", "clean")
	if err != nil || loud == base {
		t.Errorf("le drive du fichier doit changer l'ampli (err %v)", err)
	}
	plain, _, _ := runCLI(t, in, "-fx", "lpf:freq=500")
	viaCfg, _, err := runCLI(t, in, "-config", cfg, "-fx", "lpf")
	if err != nil || viaCfg != plain {
		t.Errorf("-fx lpf avec freq par défaut du fichier = -fx lpf:freq=500 (err %v)", err)
	}
}

func TestCLIConfigErrors(t *testing.T) {
	in := pcm(60, 1000, 2)
	if _, _, err := runCLI(t, in, "-config", writeCfg(t, "profiles:\n  rock: {bogus: 1}\n"), "-profile", "rock"); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("clé inconnue : %v", err)
	}
	if _, _, err := runCLI(t, in, "-config", writeCfg(t, "profiles:\n  rock:\n    compression: {ratio: 500}\n"), "-profile", "pop"); err == nil || !strings.Contains(err.Error(), "ratio") {
		t.Errorf("un profil invalide du fichier est refusé même s'il n'est pas utilisé : %v", err)
	}
	if _, _, err := runCLI(t, in, "-config", "/nulle/part.yaml", "-profile", "rock"); err == nil {
		t.Error("fichier absent")
	}
}

func TestCLIDumpConfig(t *testing.T) {
	out, _, err := runCLI(t, nil, "-dump-config")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"profiles:", "radio-hits:", "amps:", "crunch:", "fx:", "delay:"} {
		if !strings.Contains(out, want) {
			t.Errorf("-dump-config : %q absent", want)
		}
	}
	// avec un fichier, le dump montre les valeurs effectives
	out, _, err = runCLI(t, nil, "-config", writeCfg(t, "profiles:\n  rock:\n    saturation: {drive: 11}\n"), "-dump-config")
	if err != nil || !strings.Contains(out, "drive: 11") {
		t.Errorf("-dump-config doit refléter le fichier (err %v)", err)
	}
}
