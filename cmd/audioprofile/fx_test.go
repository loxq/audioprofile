package main

import (
	"math"
	"strings"
	"testing"
)

// gainAt : gain (dB) mesuré sur la sortie d'un sinus de freq Hz, canal gauche.
func fxGain(t *testing.T, freq float64, args ...string) float64 {
	t.Helper()
	in := pcm(freq, 44100, 2)
	out, _, err := runCLI(t, in, args...)
	if err != nil {
		t.Fatalf("%v : %v", args, err)
	}
	a, o := samplesOf(string(in)), samplesOf(out)
	var ei, eo float64
	for i := 2 * 11025; i < len(a); i += 2 { // 2e moitié du signal, canal gauche
		ei += float64(a[i]) * float64(a[i])
		eo += float64(o[i]) * float64(o[i])
	}
	return 10 * math.Log10(eo/ei)
}

func TestListFX(t *testing.T) {
	out, _, err := runCLI(t, nil, "-list-fx")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hpf", "lpf", "freq", "order", "obligatoire", "défaut 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q absent de %q", want, out)
		}
	}
}

// -fx seul (sans -profile ni -amp), puis combiné : filtres passe-haut, passe-bas, et les deux.
func TestFXFilters(t *testing.T) {
	// passe-haut à 1 kHz d'ordre 4 : 250 Hz (deux octaves plus bas) tombe à −48 dB, 8 kHz ne bouge pas
	if g := fxGain(t, 250, "-fx", "hpf:freq=1000,order=4"); g > -46 || g < -50 {
		t.Errorf("hpf ordre 4 à 250 Hz : %.1f dB, attendu ≈ −48", g)
	}
	if g := fxGain(t, 8000, "-fx", "hpf:freq=1000,order=4"); math.Abs(g) > 0.2 {
		t.Errorf("hpf à 8 kHz : %.2f dB", g)
	}
	if g := fxGain(t, 4000, "-fx", "lpf:freq=1000,order=2"); g > -22 || g < -26 {
		t.Errorf("lpf ordre 2 à 4 kHz : %.1f dB, attendu ≈ −24", g)
	}
	// deux filtres = passe-bande, appliqués dans l'ordre
	band := []string{"-fx", "hpf:freq=500", "-fx", "lpf:freq=2000"}
	if g := fxGain(t, 1000, band...); math.Abs(g) > 1.5 {
		t.Errorf("bande 500–2000 Hz à 1 kHz : %.2f dB", g)
	}
	if g := fxGain(t, 60, band...); g > -15 {
		t.Errorf("bande 500–2000 Hz à 60 Hz : %.1f dB, attendu < −15", g)
	}
	// forme à clé unique, majuscules et espaces tolérés
	if g := fxGain(t, 100, "-fx", " HPF:Freq=1000 "); g > -10 {
		t.Errorf("casse et espaces : %.1f dB", g)
	}
	// mono
	out, _, err := runCLI(t, pcm(100, 20000, 1), "-channels", "1", "-fx", "hpf:freq=1000")
	if err != nil || len(out) != 2*20000 {
		t.Errorf("mono : err=%v, longueur %d", err, len(out))
	}
}

// Avec un profil, les effets viennent après lui, dans l'ordre : même résultat qu'en enchaînant
// à la main, et le profil n'est pas touché quand il n'y a pas d'effet.
func TestFXAfterProfile(t *testing.T) {
	in := pcm(60, 44100, 2)
	both, _, err := runCLI(t, in, "-profile", "rock", "-fx", "hpf:freq=200,order=3")
	if err != nil {
		t.Fatal(err)
	}
	rock, _, _ := runCLI(t, in, "-profile", "rock")
	if both == rock {
		t.Error("le filtre doit modifier la sortie du profil")
	}
	// 60 Hz sous un passe-haut à 200 Hz d'ordre 3 : −32 dB environ par rapport au profil seul
	r, b := samplesOf(rock), samplesOf(both)
	var er, eb float64
	for i := 2 * 11025; i < len(r); i += 2 {
		er += float64(r[i]) * float64(r[i])
		eb += float64(b[i]) * float64(b[i])
	}
	if g := 10 * math.Log10(eb/er); g > -25 {
		t.Errorf("profil + hpf : %.1f dB par rapport au profil seul, attendu < −25", g)
	}
	// sans -fx : sortie inchangée (le profil seul est ce qu'il était)
	plain, _, _ := runCLI(t, in, "-profile", "rock")
	if plain != rock {
		t.Error("sortie non déterministe")
	}
}

func TestFXErrors(t *testing.T) {
	in := pcm(200, 1000, 2)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-fx", "reverbe"}, `effet inconnu "reverbe"`},
		{[]string{"-fx", ""}, "effet inconnu"},
		{[]string{"-fx", "hpf"}, `paramètre "freq" obligatoire`},
		{[]string{"-fx", "hpf:order=2"}, `paramètre "freq" obligatoire`},
		{[]string{"-fx", "hpf:freq=100,cutoff=3"}, `paramètre inconnu "cutoff"`},
		{[]string{"-fx", "hpf:freq=100,freq=200"}, "donné deux fois"},
		{[]string{"-fx", "hpf:freq"}, "cle=valeur"},
		{[]string{"-fx", "hpf:freq=abc"}, "pas un nombre fini"},
		{[]string{"-fx", "hpf:freq=NaN"}, "pas un nombre fini"},
		{[]string{"-fx", "hpf:freq=+Inf"}, "pas un nombre fini"},
		{[]string{"-fx", "hpf:freq=0"}, "freq doit être dans"},
		{[]string{"-fx", "hpf:freq=-50"}, "freq doit être dans"},
		{[]string{"-fx", "lpf:freq=22050"}, "freq doit être dans"}, // Nyquist à 44,1 kHz
		{[]string{"-fx", "lpf:freq=30000"}, "freq doit être dans"},
		{[]string{"-fx", "hpf:freq=100,order=9"}, "entier de 1 à 8"},
		{[]string{"-fx", "hpf:freq=100,order=0"}, "entier de 1 à 8"},
		{[]string{"-fx", "hpf:freq=100,order=2.5"}, "entier de 1 à 8"},
		{[]string{"-rate", "8000", "-fx", "lpf:freq=4000"}, "freq doit être dans"},
		{[]string{}, "-profile ou -amp est obligatoire, sauf si au moins un -fx est donné"},
	} {
		_, _, err := runCLI(t, in, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	// la liste des effets valides figure dans le message d'un effet inconnu
	_, _, err := runCLI(t, in, "-fx", "zzz")
	if err == nil || !strings.Contains(err.Error(), "hpf") || !strings.Contains(err.Error(), "lpf") {
		t.Errorf("les effets valides doivent être listés : %v", err)
	}
}

// -ab, -loudness et -target-lufs voient toute la chaîne, y compris les effets, et -ab nomme
// l'effet quand il n'y a pas de profil.
func TestFXWithABLoudnessAndTarget(t *testing.T) {
	in := pcm(1000, 44100, 2)
	_, errOut, err := runCLI(t, in, "-ab", "-fx", "hpf:freq=2000,order=4", "-ab-seconds", "0.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "traité (hpf:freq=2000,order=4)") || strings.Contains(errOut, "()") {
		t.Errorf("l'effet doit nommer le côté B : %q", errOut)
	}
	if !strings.Contains(errOut, "le traité") && !strings.Contains(errOut, "l'original est plus fort") {
		t.Errorf("gain A/B attendu dans %q", errOut)
	}
	_, errOut, err = runCLI(t, in, "-profile", "flat", "-ab", "-fx", "lpf:freq=3000", "-ab-seconds", "0.3")
	if err != nil || !strings.Contains(errOut, "traité (flat + lpf:freq=3000)") {
		t.Errorf("profil et effet dans le nom : err=%v %q", err, errOut)
	}
	// -loudness : le « traité » est la sortie du filtre (sinus de 1 kHz sous un passe-haut à 8 kHz : presque rien)
	_, errOut, err = runCLI(t, music(1500, 700), "-fx", "hpf:freq=8000,order=4", "-loudness")
	if err != nil || !strings.Contains(errOut, "Sonie (EBU R128)") {
		t.Fatalf("err=%v %q", err, errOut)
	}
	// -target-lufs : normalisation après l'effet
	out, errOut, err := runCLI(t, music(1500, 700), "-fx", "lpf:freq=1000,order=2", "-target-lufs", "-20")
	if err != nil {
		t.Fatal(err)
	}
	if l := loudnessOf(t, []byte(out)); math.Abs(l.IntegratedLUFS+20) > 0.2 || !strings.Contains(errOut, "Normalisation") {
		t.Errorf("%.2f LUFS, %q", l.IntegratedLUFS, errOut)
	}
}
