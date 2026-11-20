package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func pcm(freq float64, n, channels int) []byte {
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*freq*float64(i)/44100))
		for c := 0; c < channels; c++ {
			binary.Write(buf, binary.LittleEndian, v)
		}
	}
	return buf.Bytes()
}

func runCLI(t *testing.T, in []byte, args ...string) (out, errOut string, err error) {
	t.Helper()
	var so, se bytes.Buffer
	err = run(args, bytes.NewReader(in), &so, &se)
	return so.String(), se.String(), err
}

func TestList(t *testing.T) {
	out, _, err := runCLI(t, nil, "-list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"flat", "rock", "pop", "classical", "jazz", "hiphop", "electronic", "acoustic", "vocal"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("-list : %q absent de %q", want, out)
		}
	}
}

func TestProcessesStream(t *testing.T) {
	in := pcm(60, 5000, 2)
	out, _, err := runCLI(t, in, "-profile", "rock")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(out), len(in))
	}
	if out == string(in) {
		t.Error("rock doit modifier le signal")
	}
}

func TestFlatAndZeroIntensityAreIdentity(t *testing.T) {
	in := pcm(200, 3000, 2)
	for _, args := range [][]string{
		{"-profile", "flat"},
		{"-profile", "rock", "-intensity", "0"},
		{"-profile", "ROCK", "-intensity", "0"}, // casse ignorée
	} {
		out, _, err := runCLI(t, in, args...)
		if err != nil || out != string(in) {
			t.Errorf("%v : signal intact attendu (err=%v)", args, err)
		}
	}
}

func TestMonoRateAndChannels(t *testing.T) {
	in := pcm(100, 4000, 1)
	out, _, err := runCLI(t, in, "-profile", "pop", "-rate", "48000", "-channels", "1")
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
}

func TestErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"profil manquant":   {[]string{}, "-profile ou -amp est obligatoire"},
		"profil inconnu":    {[]string{"-profile", "disco"}, "profil inconnu"},
		"rate invalide":     {[]string{"-profile", "rock", "-rate", "0"}, "-rate"},
		"channels invalide": {[]string{"-profile", "rock", "-channels", "0"}, "-channels"},
		"intensité > max":   {[]string{"-profile", "rock", "-intensity", "3.5"}, "-intensity"},
		"intensité < 0":     {[]string{"-profile", "rock", "-intensity", "-0.5"}, "-intensity"},
		"argument en trop":  {[]string{"-profile", "rock", "fichier.raw"}, "argument inattendu"},
		"option inconnue":   {[]string{"-profile", "rock", "-nope"}, "nope"},
	} {
		_, errOut, err := runCLI(t, pcm(100, 100, 2), tc.args...)
		if err == nil {
			t.Errorf("%s : erreur attendue", name)
			continue
		}
		if !strings.Contains(err.Error()+errOut, tc.want) {
			t.Errorf("%s : %q / %q ne contient pas %q", name, err, errOut, tc.want)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	out, _, err := runCLI(t, nil, "-profile", "rock")
	if err != nil || out != "" {
		t.Errorf("entrée vide : sortie vide attendue (err=%v, %d octets)", err, len(out))
	}
}

func TestIntensityAboveOne(t *testing.T) {
	in := pcm(60, 5000, 2)
	one, _, err1 := runCLI(t, in, "-profile", "rock", "-intensity", "1")
	two, _, err2 := runCLI(t, in, "-profile", "rock", "-intensity", "2")
	max, _, err3 := runCLI(t, in, "-profile", "rock", "-intensity", "3")
	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("erreurs : %v %v %v", err1, err2, err3)
	}
	if one == two || two == max {
		t.Error("une intensité plus forte doit donner un résultat différent")
	}
	if len(two) != len(in) {
		t.Errorf("longueur %d, attendu %d", len(two), len(in))
	}
}

func TestListAmps(t *testing.T) {
	out, _, err := runCLI(t, nil, "-list-amps")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"clean", "crunch", "lead"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("-list-amps : %q absent de %q", want, out)
		}
	}
}

func TestTubeProfileListed(t *testing.T) {
	out, _, _ := runCLI(t, nil, "-list")
	if !strings.Contains(out, "tube\n") {
		t.Errorf("-list doit contenir le profil tube : %q", out)
	}
	if _, _, err := runCLI(t, pcm(100, 100, 2), "-profile", "tube"); err != nil {
		t.Errorf("-profile tube : %v", err)
	}
}

func TestAmpPresets(t *testing.T) {
	in := pcm(100, 5000, 2)
	outs := map[string]string{}
	for _, name := range []string{"clean", "crunch", "lead"} {
		out, _, err := runCLI(t, in, "-amp", name)
		if err != nil || len(out) != len(in) {
			t.Fatalf("-amp %s : err=%v len=%d/%d", name, err, len(out), len(in))
		}
		if out == string(in) {
			t.Errorf("-amp %s doit modifier le signal", name)
		}
		outs[name] = out
	}
	if outs["clean"] == outs["crunch"] || outs["crunch"] == outs["lead"] {
		t.Error("les préréglages doivent donner des sons différents")
	}
	// casse ignorée, intensité nulle = signal intact
	if out, _, err := runCLI(t, in, "-amp", "CRUNCH", "-intensity", "0"); err != nil || out != string(in) {
		t.Errorf("-amp avec intensité 0 : signal intact attendu (err=%v)", err)
	}
	// intensité exagérée acceptée, différente de 1
	one, _, _ := runCLI(t, in, "-amp", "crunch", "-intensity", "1")
	two, _, err := runCLI(t, in, "-amp", "crunch", "-intensity", "2")
	if err != nil || one == two {
		t.Errorf("-intensity 2 doit donner un autre résultat (err=%v)", err)
	}
}

func TestAmpCabinet(t *testing.T) {
	in := pcm(3000, 5000, 2) // 3 kHz : le baffle agit
	plain, _, _ := runCLI(t, in, "-amp", "clean")
	cab, _, err := runCLI(t, in, "-amp", "clean", "-cabinet")
	if err != nil || len(cab) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(cab), len(in))
	}
	if plain == cab {
		t.Error("-cabinet doit changer le son")
	}
}

func TestAmpErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"ampli inconnu":    {[]string{"-amp", "fuzz"}, "ampli inconnu"},
		"profil et ampli":  {[]string{"-profile", "rock", "-amp", "lead"}, "exclusifs"},
		"cabinet sans amp": {[]string{"-profile", "rock", "-cabinet"}, "-cabinet"},
		"ampli intensité":  {[]string{"-amp", "lead", "-intensity", "9"}, "-intensity"},
	} {
		_, errOut, err := runCLI(t, pcm(100, 100, 2), tc.args...)
		if err == nil || !strings.Contains(err.Error()+errOut, tc.want) {
			t.Errorf("%s : %v / %q ne contient pas %q", name, err, errOut, tc.want)
		}
	}
}
