package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, yml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reglages.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

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
