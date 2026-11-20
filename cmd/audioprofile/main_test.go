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
		"profil manquant":   {[]string{}, "-profile est obligatoire"},
		"profil inconnu":    {[]string{"-profile", "disco"}, "profil inconnu"},
		"rate invalide":     {[]string{"-profile", "rock", "-rate", "0"}, "-rate"},
		"channels invalide": {[]string{"-profile", "rock", "-channels", "0"}, "-channels"},
		"intensité > 1":     {[]string{"-profile", "rock", "-intensity", "2"}, "-intensity"},
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
