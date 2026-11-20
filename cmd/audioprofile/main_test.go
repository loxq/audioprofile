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
	for _, want := range []string{"flat", "rock", "pop", "classical", "jazz", "hiphop", "electronic", "acoustic", "vocal", "tube",
		"radio-hits", "radio-rock", "radio-urban", "radio-club", "radio-talk", "radio-gold", "radio-natural"} {
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

// Un profil radio traverse le CLI : même longueur (le retard du limiteur est rattrapé),
// signal modifié, crêtes sous le plafond de -1 dBFS, et intensité 0 sans effet.
func TestRadioProfileThroughCLI(t *testing.T) {
	in := pcm(1000, 20000, 2) // 8000 d'amplitude : bien en dessous du plafond
	out, _, err := runCLI(t, in, "-profile", "radio-hits")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(out), len(in))
	}
	if out == string(in) {
		t.Error("radio-hits doit modifier le signal")
	}
	hot := pcm(1000, 20000, 2)
	for i := 0; i+1 < len(hot); i += 2 { // même signal, mais à pleine échelle
		v := int16(uint16(hot[i]) | uint16(hot[i+1])<<8)
		v = int16(int(v) * 4)
		hot[i], hot[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	hout, _, err := runCLI(t, hot, "-profile", "radio-club", "-intensity", "3")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(hout); i += 2 {
		v := int(int16(uint16(hout[i]) | uint16(hout[i+1])<<8))
		if v > 29205 || v < -29205 { // plafond -1 dBFS = 29204,5 : 29205 après arrondi
			t.Fatalf("échantillon %d à la trame %d : au-dessus du plafond de -1 dBFS", v, i/4)
		}
	}
	same, _, err := runCLI(t, in, "-profile", "radio-hits", "-intensity", "0")
	if err != nil || same != string(in) {
		t.Errorf("intensité 0 : le signal doit rester intact (err=%v)", err)
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

func samplesOf(s string) []int16 {
	out := make([]int16, len(s)/2)
	for i := range out {
		out[i] = int16(uint16(s[2*i]) | uint16(s[2*i+1])<<8)
	}
	return out
}

// Avec un profil sans effet (flat, ou intensité 0), -ab rend le signal bit à bit : le gain
// est de 0 dB et le fondu enchaîné de deux flux identiques ne change rien.
func TestABWithNoEffectIsBitExact(t *testing.T) {
	in := pcm(300, 30000, 2)
	for _, args := range [][]string{
		{"-ab", "-profile", "flat", "-ab-seconds", "0.1"},
		{"-ab", "-profile", "rock", "-intensity", "0", "-ab-seconds", "0.1"},
	} {
		out, errOut, err := runCLI(t, in, args...)
		if err != nil {
			t.Fatalf("%v : %v", args, err)
		}
		if out != string(in) {
			t.Errorf("%v : le signal doit rester intact", args)
		}
		if !strings.Contains(errOut, "A = original") || !strings.Contains(errOut, "aucun gain") {
			t.Errorf("%v : convention A/B ou gain absents de %q", args, errOut)
		}
	}
}

// Avec un vrai profil : même longueur, premier segment = original au gain près, deuxième
// segment différent, et la convention (A = original, quel côté est atténué) est annoncée.
func TestABAlternatesAndReports(t *testing.T) {
	in := pcm(60, 44100, 2) // 1 s
	out, errOut, err := runCLI(t, in, "-ab", "-profile", "rock", "-ab-seconds", "0.25")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(out), len(in))
	}
	for _, want := range []string{"A = original", "B = traité (rock)", "segments de 0.25 s", "gain de"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("%q absent de %q", want, errOut)
		}
	}
	a, o := samplesOf(string(in)), samplesOf(out)
	// rapport sortie/entrée dans le premier segment (hors fondu) : constant
	ratio := func(from, to int) (lo, hi float64) {
		lo, hi = math.Inf(1), math.Inf(-1)
		for i := from; i < to; i += 2 { // canal gauche
			if a[i] > 4000 || a[i] < -4000 {
				r := float64(o[i]) / float64(a[i])
				lo, hi = math.Min(lo, r), math.Max(hi, r)
			}
		}
		return
	}
	l1, h1 := ratio(0, 2*(11025-400))
	if h1-l1 > 0.02 {
		t.Errorf("premier segment : le rapport sortie/entrée varie de %.3f à %.3f, attendu constant (original au gain près)", l1, h1)
	}
	// le deuxième segment est le signal traité : il s'écarte de l'original au même gain
	c1 := (l1 + h1) / 2
	worst := 0.0
	for i := 2 * (11025 + 400); i < 2*(22050-400); i += 2 {
		worst = math.Max(worst, math.Abs(float64(o[i])-c1*float64(a[i])))
	}
	if worst < 400 { // 5 % de l'amplitude (8000)
		t.Errorf("le deuxième segment doit être le signal traité (écart maximal %.0f)", worst)
	}
}

func TestABFlagErrors(t *testing.T) {
	in := pcm(200, 1000, 2)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-profile", "rock", "-ab-seconds", "2"}, "-ab-seconds ne s'utilise qu'avec -ab"},
		{[]string{"-profile", "rock", "-ab", "-ab-seconds", "0"}, "-ab-seconds doit être > 0"},
		{[]string{"-profile", "rock", "-ab", "-ab-seconds", "-3"}, "-ab-seconds doit être > 0"},
		{[]string{"-profile", "rock", "-ab", "-ab-seconds", "NaN"}, "-ab-seconds doit être > 0"},
		{[]string{"-profile", "rock", "-ab", "-ab-seconds", "+Inf"}, "-ab-seconds doit être > 0"},
	} {
		_, _, err := runCLI(t, in, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
}

// -ab fonctionne aussi avec un ampli, et sur de la mono.
func TestABWithAmpAndMono(t *testing.T) {
	in := pcm(200, 20000, 1)
	out, errOut, err := runCLI(t, in, "-ab", "-amp", "lead", "-channels", "1", "-ab-seconds", "0.1")
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
	}
	if !strings.Contains(errOut, "ampli lead") {
		t.Errorf("l'ampli doit être nommé dans %q", errOut)
	}
}
