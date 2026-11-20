package main

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/loxq/audioprofile"
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

// -loudness n'altère pas la sortie, et affiche la sonie de l'original et du traité : un sinus
// de 1 kHz d'amplitude 8000 (−12,3 dBFS) sur les deux canaux mesure −12,25 LUFS.
func TestLoudnessReport(t *testing.T) {
	in := pcm(1000, 44100, 2)
	plain, _, err := runCLI(t, in, "-profile", "rock")
	if err != nil {
		t.Fatal(err)
	}
	out, errOut, err := runCLI(t, in, "-profile", "rock", "-loudness")
	if err != nil || out != plain {
		t.Fatalf("-loudness ne doit pas changer la sortie (err=%v)", err)
	}
	flat, errFlat, err := runCLI(t, in, "-profile", "flat", "-loudness")
	if err != nil || flat != string(in) {
		t.Fatalf("flat : sortie différente de l'entrée (err=%v)", err)
	}
	for _, want := range []string{"Sonie (EBU R128)", "original", "traité", "intégrée (LUFS)", "plage (LU)", "crête d'échantillon (dBFS)"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("%q absent de %q", want, errOut)
		}
	}
	// flat : l'original et le traité ont exactement la même sonie
	for _, line := range strings.Split(errFlat, "\n") {
		if strings.Contains(line, "intégrée (LUFS)") {
			f := strings.Fields(line)
			v, err := strconv.ParseFloat(f[len(f)-1], 64)
			if len(f) < 4 || f[len(f)-1] != f[len(f)-2] || err != nil || math.Abs(v+12.25) > 0.1 {
				t.Errorf("sonie intégrée de flat : %q, attendu ≈ -12,25 des deux côtés", line)
			}
		}
	}
	// rock change la sonie du traité mais pas celle de l'original
	var origRock, procRock, origFlat string
	for _, line := range strings.Split(errOut, "\n") {
		if strings.Contains(line, "intégrée (LUFS)") {
			f := strings.Fields(line)
			origRock, procRock = f[len(f)-2], f[len(f)-1]
		}
	}
	for _, line := range strings.Split(errFlat, "\n") {
		if strings.Contains(line, "intégrée (LUFS)") {
			f := strings.Fields(line)
			origFlat = f[len(f)-2]
		}
	}
	if origRock != origFlat || procRock == origRock {
		t.Errorf("original %s (flat %s), traité %s : l'original doit être le même et le traité différer", origRock, origFlat, procRock)
	}
}

func TestLoudnessReportNotMeasured(t *testing.T) {
	_, errOut, err := runCLI(t, pcm(1000, 4000, 2), "-profile", "flat", "-loudness") // 90 ms
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "—") || !strings.Contains(errOut, "400 ms") {
		t.Errorf("signal trop court : explication attendue dans %q", errOut)
	}
	if strings.Contains(errOut, "NaN") || strings.Contains(errOut, "Inf") {
		t.Errorf("valeur non finie dans %q", errOut)
	}
	_, errOut, err = runCLI(t, make([]byte, 4*44100*2), "-profile", "flat", "-loudness") // silence
	if err != nil || !strings.Contains(errOut, "—") || strings.Contains(errOut, "Inf") {
		t.Errorf("silence : err=%v, %q", err, errOut)
	}
}

// Avec -ab, -loudness mesure les deux flux entiers (pas l'alternance).
func TestLoudnessWithAB(t *testing.T) {
	_, errOut, err := runCLI(t, pcm(1000, 44100, 2), "-ab", "-profile", "rock", "-loudness", "-ab-seconds", "0.2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "Sonie (EBU R128)") || !strings.Contains(errOut, "A = original") || !strings.Contains(errOut, "sonie") {
		t.Errorf("tableau et convention A/B attendus dans %q", errOut)
	}
}

// Piège rencontré plus tôt : /dev/null est un périphérique caractère mais pas un terminal. Pour ne
// garder que la mesure, la sortie doit pouvoir y aller.
func TestOutputToDevNullIsAllowed(t *testing.T) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	var se bytes.Buffer
	if err := run([]string{"-profile", "flat", "-loudness"}, bytes.NewReader(pcm(1000, 44100, 2)), null, &se); err != nil {
		t.Fatalf("sortie vers %s : %v", os.DevNull, err)
	}
	if !strings.Contains(se.String(), "Sonie (EBU R128)") {
		t.Errorf("mesure attendue sur la sortie d'erreur : %q", se.String())
	}
	if isTerminal(null) {
		t.Error("/dev/null n'est pas un terminal")
	}
}

// music : deux sinus à des niveaux différents, assez longs (6 s) pour avoir une sonie intégrée.
func music(amp1, amp2 float64) []byte {
	const n = 6 * 44100
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		t := float64(i) / 44100
		l := int16(amp1*math.Sin(2*math.Pi*220*t) + amp2*math.Sin(2*math.Pi*2000*t))
		r := int16(amp1*math.Sin(2*math.Pi*330*t) + amp2*math.Sin(2*math.Pi*3000*t))
		binary.Write(buf, binary.LittleEndian, l)
		binary.Write(buf, binary.LittleEndian, r)
	}
	return buf.Bytes()
}

func loudnessOf(t *testing.T, b []byte) audioprofile.Loudness {
	t.Helper()
	l, err := audioprofile.MeasureLoudness(bytes.NewReader(b), 44100, 2)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestTargetLUFS(t *testing.T) {
	in := music(1500, 700)
	for _, target := range []string{"-14", "-23", "-30"} {
		out, errOut, err := runCLI(t, in, "-profile", "flat", "-target-lufs", target)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != len(in) {
			t.Fatalf("longueur %d, attendu %d", len(out), len(in))
		}
		want, _ := strconv.ParseFloat(target, 64)
		l := loudnessOf(t, []byte(out))
		if math.Abs(l.IntegratedLUFS-want) > 0.15 {
			t.Errorf("cible %s : %.2f LUFS obtenus", target, l.IntegratedLUFS)
		}
		if !strings.Contains(errOut, "Normalisation : sonie intégrée") || !strings.Contains(errOut, "cible "+target+".0") {
			t.Errorf("résumé attendu dans %q", errOut)
		}
		for _, v := range samplesOf(out) {
			if math.Abs(float64(v)) > 29205 {
				t.Fatalf("crête %d au-dessus du plafond par défaut de -1 dBFS", v)
			}
		}
	}
}

// Avec un profil : la normalisation vient après le traitement, et le tableau -loudness compare
// l'original à la sortie normalisée.
func TestTargetLUFSAfterProfileAndWithLoudness(t *testing.T) {
	in := music(1500, 700)
	out, errOut, err := runCLI(t, in, "-profile", "radio-hits", "-target-lufs", "-16", "-loudness")
	if err != nil {
		t.Fatal(err)
	}
	if l := loudnessOf(t, []byte(out)); math.Abs(l.IntegratedLUFS+16) > 0.2 {
		t.Errorf("%.2f LUFS, attendu -16", l.IntegratedLUFS)
	}
	if !strings.Contains(errOut, "Normalisation") || !strings.Contains(errOut, "Sonie (EBU R128)") {
		t.Errorf("résumé et tableau attendus dans %q", errOut)
	}
}

func TestTargetLUFSUnreachableAndSilent(t *testing.T) {
	// plafond trop bas : la cible n'est pas atteinte, et on le dit
	out, errOut, err := runCLI(t, music(3000, 0), "-profile", "flat", "-target-lufs", "-5", "-ceiling", "-20")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "non atteinte") || !strings.Contains(errOut, "limiteur") {
		t.Errorf("« non atteinte » et la raison attendus dans %q", errOut)
	}
	for _, v := range samplesOf(out) {
		if math.Abs(float64(v)) > 3277+1 { // -20 dBFS
			t.Fatalf("crête %d au-dessus du plafond de -20 dBFS", v)
		}
	}
	// silence : sortie identique, message clair
	silent := make([]byte, 4*44100*2)
	out, errOut, err = runCLI(t, silent, "-profile", "flat", "-target-lufs", "-14")
	if err != nil || out != string(silent) || !strings.Contains(errOut, "silencieux") || strings.Contains(errOut, "NaN") || strings.Contains(errOut, "Inf") {
		t.Errorf("silence : err=%v, identique=%v, %q", err, out == string(silent), errOut)
	}
}

func TestTargetLUFSFlagErrors(t *testing.T) {
	in := music(1500, 700)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-profile", "rock", "-target-lufs", "NaN"}, "-target-lufs doit être dans [-70, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "-90"}, "-target-lufs doit être dans [-70, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "5"}, "-target-lufs doit être dans [-70, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "+Inf"}, "-target-lufs doit être dans [-70, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "-14", "-ceiling", "3"}, "-ceiling doit être dans [-40, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "-14", "-ceiling", "NaN"}, "-ceiling doit être dans [-40, 0]"},
		{[]string{"-profile", "rock", "-target-lufs", "-14", "-ceiling", "-80"}, "-ceiling doit être dans [-40, 0]"},
		{[]string{"-profile", "rock", "-ceiling", "-2"}, "-ceiling ne s'utilise qu'avec -target-lufs"},
		{[]string{"-profile", "rock", "-ab", "-target-lufs", "-14"}, "-target-lufs et -ab sont exclusifs"},
	} {
		_, _, err := runCLI(t, in, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
}

// Sans -target-lufs, la sortie est inchangée : les nouvelles options n'ont aucun effet par défaut.
func TestNoTargetLeavesOutputUnchanged(t *testing.T) {
	in := music(1500, 700)
	a, _, err := runCLI(t, in, "-profile", "rock")
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := runCLI(t, in, "-profile", "rock", "-loudness")
	if a != b {
		t.Error("-loudness ne doit pas changer la sortie")
	}
	if a == string(in) {
		t.Error("rock doit modifier le signal")
	}
}
