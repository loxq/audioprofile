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

// -fx transient : sans réglage, le flux est identique ; avec attack, le début d'un son est renforcé.
func TestFXTransient(t *testing.T) {
	// un son qui part d'un coup puis s'éteint (7 000 trames de silence, puis un sinus qui décroît)
	var in []byte
	for i := 0; i < 44100; i++ {
		v := 0.0
		if i >= 7000 {
			tt := float64(i-7000) / 44100
			v = 6000 * math.Exp(-tt*1000/90) * math.Sin(2*math.Pi*180*tt)
		}
		in = append(in, byte(uint16(int16(v))), byte(uint16(int16(v))>>8), byte(uint16(int16(v))), byte(uint16(int16(v))>>8))
	}
	same, _, err := runCLI(t, in, "-fx", "transient")
	if err != nil || same != string(in) {
		t.Fatalf("sans réglage, le flux doit rester identique (err=%v)", err)
	}
	out, _, err := runCLI(t, in, "-fx", "transient:attack=1")
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
	}
	a, o := samplesOf(string(in)), samplesOf(out)
	var ei, eo float64
	for i := 2 * 7000; i < 2*(7000+441); i += 2 { // les 10 premières ms de l'attaque
		ei += float64(a[i]) * float64(a[i])
		eo += float64(o[i]) * float64(o[i])
	}
	if g := 10 * math.Log10(eo/ei); g < 3 {
		t.Errorf("attaque : %+.1f dB sur les 10 premières ms, attendu > +3", g)
	}
	for _, args := range [][]string{{"-fx", "transient:attack=2"}, {"-fx", "transient:sustain=-1.5"}} {
		if _, _, err := runCLI(t, in, args...); err == nil || !strings.Contains(err.Error(), "doit être dans [-1, 1]") {
			t.Errorf("%v : erreur attendue, reçu %v", args, err)
		}
	}
	list, _, _ := runCLI(t, nil, "-list-fx")
	if !strings.Contains(list, "transient") || !strings.Contains(list, "attack") || !strings.Contains(list, "sustain") {
		t.Errorf("transient absent de -list-fx : %q", list)
	}
}

// -fx reverb : une salve suivie de silence laisse une queue ; sans mélange, rien ne change ; bornes vérifiées.
func TestFXReverb(t *testing.T) {
	in := make([]byte, 4*44100*2)
	copy(in, pcm(300, 4000, 2)) // 4 000 trames de son, puis du silence
	out, _, err := runCLI(t, in, "-fx", "reverb:room=0.4,mix=0.5")
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
	}
	var tail float64
	for _, v := range samplesOf(out)[2*20000 : 2*40000] { // bien après le son : la queue de la réverbération
		tail += math.Abs(float64(v))
	}
	if tail == 0 {
		t.Error("la réverbération doit laisser une queue après la fin du son")
	}
	if same, _, err := runCLI(t, in, "-fx", "reverb:mix=0"); err != nil || same != string(in) {
		t.Errorf("mix=0 : le flux doit rester identique (err=%v)", err)
	}
	// défaut : un mélange de 0,25 modifie le flux
	if def, _, err := runCLI(t, in, "-fx", "reverb"); err != nil || def == string(in) {
		t.Errorf("réglages par défaut : le flux doit être modifié (err=%v)", err)
	}
	for _, tc := range []struct{ args, want string }{
		{"reverb:room=1.5", "room doit être dans [0, 1]"},
		{"reverb:damping=-0.1", "damping doit être dans [0, 1]"},
		{"reverb:width=2", "width doit être dans [0, 1]"},
		{"reverb:mix=1.01", "mix doit être dans [0, 1]"},
		{"reverb:predelay=300", "predelay doit être dans [0, 200]"},
		{"reverb:size=0.5", `paramètre inconnu "size"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	// plusieurs effets dans l'ordre, et le nom du côté B
	_, errOut, err := runCLI(t, pcm(300, 20000, 2), "-ab", "-fx", "transient:attack=0.5", "-fx", "reverb:mix=0.2", "-ab-seconds", "0.2")
	if err != nil || !strings.Contains(errOut, "traité (transient:attack=0.5 + reverb:mix=0.2)") {
		t.Errorf("err=%v %q", err, errOut)
	}
}

// -fx deess et -fx exciter : effets mesurés sur un aigu, identité à réglage nul, toutes les bornes.
func TestFXDeEsserAndExciter(t *testing.T) {
	// un sinus de 7 kHz à −12 dBFS (amplitude 8 000), stéréo
	in := pcm(7000, 44100, 2)
	level := func(out string) float64 {
		o := samplesOf(out)
		var e float64
		for i := 2 * 22050; i < len(o); i += 2 {
			e += float64(o[i]) * float64(o[i])
		}
		return 10 * math.Log10(e/float64(len(o)/4))
	}
	ref := level(string(in))
	de, _, err := runCLI(t, in, "-fx", "deess:threshold=-30,ratio=8")
	if err != nil {
		t.Fatal(err)
	}
	if g := level(de) - ref; g > -6 {
		t.Errorf("deess : %+.1f dB sur une sifflante forte, attendu < −6", g)
	}
	// sous le seuil : à ±0,3 dB
	quiet := pcm(7000, 44100, 2)
	qs := samplesOf(string(quiet))
	for i := range qs {
		qs[i] /= 60 // −47 dBFS
	}
	var qb []byte
	for _, v := range qs {
		qb = append(qb, byte(uint16(v)), byte(uint16(v)>>8))
	}
	qo, _, _ := runCLI(t, qb, "-fx", "deess:threshold=-30,ratio=8")
	if g := level(qo) - level(string(qb)); math.Abs(g) > 0.3 {
		t.Errorf("deess sous le seuil : %+.2f dB", g)
	}
	// ratio 1 : sans effet, flux identique
	if same, _, err := runCLI(t, in, "-fx", "deess:ratio=1"); err != nil || same != string(in) {
		t.Errorf("ratio 1 : le flux doit rester identique (err=%v)", err)
	}
	// exciter : amount 0 identique ; amount 1 modifie un aigu fort
	if same, _, err := runCLI(t, in, "-fx", "exciter:amount=0"); err != nil || same != string(in) {
		t.Errorf("amount 0 : le flux doit rester identique (err=%v)", err)
	}
	ex, _, err := runCLI(t, in, "-fx", "exciter:amount=1,drive=24,freq=2000")
	if err != nil || ex == string(in) || len(ex) != len(in) {
		t.Errorf("exciter : le flux doit être modifié (err=%v)", err)
	}
	for _, tc := range []struct{ args, want string }{
		{"deess:freq=0", "freq doit être dans"},
		{"deess:freq=30000", "freq doit être dans"},
		{"deess:threshold=5", "threshold doit être dans [-80, 0]"},
		{"deess:threshold=-100", "threshold doit être dans [-80, 0]"},
		{"deess:ratio=0.5", "ratio doit être dans [1, 100]"},
		{"deess:ratio=500", "ratio doit être dans [1, 100]"},
		{"exciter:freq=-5", "freq doit être dans"},
		{"exciter:amount=1.2", "amount doit être dans [0, 1]"},
		{"exciter:warmth=-0.2", "warmth doit être dans [0, 1]"},
		{"exciter:drive=1", "drive doit être dans [3, 24]"},
		{"exciter:drive=40", "drive doit être dans [3, 24]"},
		{"exciter:gain=3", `paramètre inconnu "gain"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	list, _, _ := runCLI(t, nil, "-list-fx")
	for _, want := range []string{"deess", "exciter", "threshold", "ratio", "amount", "drive", "warmth"} {
		if !strings.Contains(list, want) {
			t.Errorf("%q absent de -list-fx", want)
		}
	}
	// chaîne complète : tous les effets ensemble, dans l'ordre, avec un profil
	if _, _, err := runCLI(t, in, "-profile", "radio-talk", "-fx", "hpf:freq=80", "-fx", "deess", "-fx", "exciter", "-fx", "transient:attack=0.3", "-fx", "reverb:mix=0.1"); err != nil {
		t.Errorf("chaîne complète : %v", err)
	}
}

// -fx parcomp : mix nul identique, le passage faible est relevé plus que le fort, bornes vérifiées.
func TestFXParallelCompression(t *testing.T) {
	in := pcm(1000, 44100, 2)
	if same, _, err := runCLI(t, in, "-fx", "parcomp:mix=0"); err != nil || same != string(in) {
		t.Errorf("mix 0 : le flux doit rester identique (err=%v)", err)
	}
	out, _, err := runCLI(t, in, "-fx", "parcomp:threshold=-40,ratio=10,makeup=12,mix=0.5")
	if err != nil || len(out) != len(in) || out == string(in) {
		t.Fatalf("err=%v longueur %d/%d, doit être modifié", err, len(out), len(in))
	}
	for _, tc := range []struct{ args, want string }{
		{"parcomp:threshold=5", "threshold doit être dans [-80, 0]"},
		{"parcomp:ratio=0.5", "ratio doit être dans [1, 100]"},
		{"parcomp:attack=0", "attack doit être dans [0.1, 1000]"},
		{"parcomp:release=9999", "release doit être dans [1, 5000]"},
		{"parcomp:makeup=30", "makeup doit être dans [0, 24]"},
		{"parcomp:mix=1.5", "mix doit être dans [0, 1]"},
		{"parcomp:gain=1", `paramètre inconnu "gain"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "parcomp") {
		t.Error("parcomp absent de -list-fx")
	}
}

// -fx gate : un signal fort passe, un signal faible est réduit, ratio 1 identique, bornes vérifiées.
func TestFXGate(t *testing.T) {
	const loud, quiet = 8000, 8000 / 316 // 8 000 (−12 dBFS) et −62 dBFS
	level := func(amp float64) float64 {
		in := make([]byte, 0, 4*44100)
		for i := 0; i < 44100; i++ {
			v := int16(amp * math.Sin(2*math.Pi*1000*float64(i)/44100))
			in = append(in, byte(uint16(v)), byte(uint16(v)>>8), byte(uint16(v)), byte(uint16(v)>>8))
		}
		out, _, err := runCLI(t, in, "-fx", "gate:threshold=-45,ratio=50,range=30")
		if err != nil {
			t.Fatal(err)
		}
		a, o := samplesOf(string(in)), samplesOf(out)
		var ei, eo float64
		for i := 2 * 22050; i < len(a); i += 2 {
			ei += float64(a[i]) * float64(a[i])
			eo += float64(o[i]) * float64(o[i])
		}
		return 10 * math.Log10(eo/ei)
	}
	if g := level(loud); math.Abs(g) > 0.1 {
		t.Errorf("signal fort : %+.2f dB, attendu intact", g)
	}
	if g := level(quiet); g > -20 {
		t.Errorf("signal faible : %+.1f dB, attendu une forte réduction", g)
	}
	in := pcm(1000, 20000, 2)
	if same, _, err := runCLI(t, in, "-fx", "gate:ratio=1"); err != nil || same != string(in) {
		t.Errorf("ratio 1 : le flux doit rester identique (err=%v)", err)
	}
	for _, tc := range []struct{ args, want string }{
		{"gate:threshold=3", "threshold doit être dans [-90, 0]"},
		{"gate:ratio=0.2", "ratio doit être dans [1, 100]"},
		{"gate:range=0", "range doit être dans [1, 90]"},
		{"gate:attack=0", "attack doit être dans [0.05, 500]"},
		{"gate:release=0", "release doit être dans [1, 5000]"},
		{"gate:hold=5", `paramètre inconnu "hold"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "gate") {
		t.Error("gate absent de -list-fx")
	}
}

// -fx notch : supprime la fréquence visée (ronflement à 50 Hz), laisse le reste, bornes vérifiées.
func TestFXNotch(t *testing.T) {
	if g := fxGain(t, 50, "-fx", "notch:freq=50,q=10"); g > -40 {
		t.Errorf("50 Hz : %.1f dB, attendu < −40", g)
	}
	if g := fxGain(t, 1000, "-fx", "notch:freq=50"); math.Abs(g) > 0.1 {
		t.Errorf("1 kHz : %+.2f dB, attendu intact", g)
	}
	// ronflement et harmonique : deux effets de suite
	if g := fxGain(t, 100, "-fx", "notch:freq=50", "-fx", "notch:freq=100,q=20"); g > -40 {
		t.Errorf("100 Hz après deux notch : %.1f dB, attendu < −40", g)
	}
	in := pcm(200, 1000, 2)
	for _, tc := range []struct{ args, want string }{
		{"notch", `paramètre "freq" obligatoire`},
		{"notch:freq=0", "freq doit être dans"},
		{"notch:freq=22050", "freq doit être dans"},
		{"notch:freq=50,q=0", "q doit être dans [0.1, 1000]"},
		{"notch:freq=50,q=5000", "q doit être dans [0.1, 1000]"},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "notch") {
		t.Error("notch absent de -list-fx")
	}
}

// -fx crossfeed : un son au centre est identique, un son à gauche fuit à droite aux graves, bornes vérifiées.
func TestFXCrossfeed(t *testing.T) {
	centered := pcm(100, 44100, 2) // même signal à gauche et à droite
	same, _, err := runCLI(t, centered, "-fx", "crossfeed")
	if err != nil || same != string(centered) {
		t.Errorf("son au centre : le flux doit rester identique (err=%v)", err)
	}
	// 100 Hz à gauche seulement, silence à droite
	var left []byte
	for i := 0; i < 44100; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*100*float64(i)/44100))
		left = append(left, byte(uint16(v)), byte(uint16(v)>>8), 0, 0)
	}
	out, _, err := runCLI(t, left, "-fx", "crossfeed:strength=0.6")
	if err != nil || len(out) != len(left) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(left))
	}
	var el, er float64
	for i, v := range samplesOf(out) {
		if i < 2*22050 {
			continue
		}
		if i%2 == 0 {
			el += float64(v) * float64(v)
		} else {
			er += float64(v) * float64(v)
		}
	}
	if g := 10 * math.Log10(er/el); g > -6 || g < -9 { // droite : k = 0,3 de la gauche ; gauche : 1 − k = 0,7 : 0,3 / 0,7 = −7,4 dB
		t.Errorf("fuite à droite : %.1f dB par rapport à la gauche, attendu ≈ −7,4", g)
	}
	// mono : sans effet
	if mono, _, err := runCLI(t, pcm(100, 20000, 1), "-channels", "1", "-fx", "crossfeed"); err != nil || len(mono) != 2*20000 {
		t.Errorf("mono : err=%v", err)
	}
	for _, tc := range []struct{ args, want string }{
		{"crossfeed:strength=1.5", "strength doit être dans [0, 1]"},
		{"crossfeed:cutoff=0", "cutoff doit être dans"},
		{"crossfeed:cutoff=22050", "cutoff doit être dans"},
		{"crossfeed:delay=2", "delay doit être dans [0, 1] ms"},
		{"crossfeed:level=3", `paramètre inconnu "level"`},
	} {
		if _, _, err := runCLI(t, centered, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "crossfeed") {
		t.Error("crossfeed absent de -list-fx")
	}
}

// -fx delay : un écho apparaît à l'instant demandé, mix 0 identique, pingpong 0 ou 1 seulement, bornes vérifiées.
func TestFXDelay(t *testing.T) {
	in := make([]byte, 4*44100)         // 1 s de silence stéréo, avec une impulsion à gauche
	in[4*100], in[4*100+1] = 0x20, 0x4E // 20 000 en s16le sur le canal gauche, trame 100
	out, _, err := runCLI(t, in, "-fx", "delay:time=100,feedback=0,mix=0.5")
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
	}
	o := samplesOf(out)
	if o[2*(100+4410)] < 9990 || o[2*(100+4410)] > 10010 { // écho à 100 ms, amplitude 10 000
		t.Errorf("écho à 100 ms : %d, attendu ≈ 10 000", o[2*(100+4410)])
	}
	if same, _, err := runCLI(t, in, "-fx", "delay:mix=0"); err != nil || same != string(in) {
		t.Errorf("mix 0 : le flux doit rester identique (err=%v)", err)
	}
	if _, _, err := runCLI(t, in, "-fx", "delay:pingpong=1,time=50"); err != nil {
		t.Errorf("pingpong=1 : %v", err)
	}
	for _, tc := range []struct{ args, want string }{
		{"delay:time=0", "time doit être dans [1, 2000]"},
		{"delay:time=3000", "time doit être dans [1, 2000]"},
		{"delay:feedback=1", "feedback doit être dans [0, 0.95]"},
		{"delay:mix=2", "mix doit être dans [0, 1]"},
		{"delay:damping=5", "damping doit valoir 0 ou être dans"},
		{"delay:damping=30000", "damping doit valoir 0 ou être dans"},
		{"delay:pingpong=0.5", "pingpong doit valoir 0 ou 1"},
		{"delay:pingpong=2", "pingpong doit valoir 0 ou 1"},
		{"delay:echo=3", `paramètre inconnu "echo"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "pingpong") {
		t.Error("delay absent de -list-fx")
	}
}

// -fx chorus et -fx flanger : flux modifié, mix 0 identique, bornes, depth <= delay.
func TestFXChorusAndFlanger(t *testing.T) {
	in := pcm(400, 44100, 2)
	for _, name := range []string{"chorus", "flanger"} {
		if same, _, err := runCLI(t, in, "-fx", name+":mix=0"); err != nil || same != string(in) {
			t.Errorf("%s mix 0 : le flux doit rester identique (err=%v)", name, err)
		}
		out, _, err := runCLI(t, in, "-fx", name)
		if err != nil || len(out) != len(in) || out == string(in) {
			t.Errorf("%s : err=%v longueur %d/%d, doit être modifié", name, err, len(out), len(in))
		}
	}
	for _, tc := range []struct{ args, want string }{
		{"chorus:delay=0", "delay doit être dans [1, 50]"},
		{"chorus:voices=2.5", "voices doit être un entier"},
		{"chorus:voices=9", "voices doit être dans [1, 6]"},
		{"chorus:rate=20", "rate doit être dans [0.05, 10]"},
		{"chorus:mix=1.2", "mix doit être dans [0, 1]"},
		{"chorus:delay=5,depth=4.9", "ne doit pas dépasser delay - 0,5"},
		{"flanger:feedback=1", "feedback doit être dans [-0.95, 0.95]"},
		{"flanger:delay=20", "delay doit être dans [0.3, 10]"},
		{"flanger:delay=1,depth=0.95", "ne doit pas dépasser delay - 0,2"},
		{"flanger:tone=3", `paramètre inconnu "tone"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	list, _, _ := runCLI(t, nil, "-list-fx")
	for _, want := range []string{"chorus", "flanger", "voices", "feedback"} {
		if !strings.Contains(list, want) {
			t.Errorf("%q absent de -list-fx", want)
		}
	}
}

// -fx lofi : modifie le flux et garde sa longueur, tout à zéro est identique, bornes vérifiées.
func TestFXLoFi(t *testing.T) {
	in := pcm(400, 44100, 2)
	out, _, err := runCLI(t, in, "-fx", "lofi")
	if err != nil || len(out) != len(in) || out == string(in) {
		t.Fatalf("lofi : err=%v longueur %d/%d, doit être modifié", err, len(out), len(in))
	}
	again, _, _ := runCLI(t, in, "-fx", "lofi")
	if again != out {
		t.Error("lofi doit être reproductible (graine fixe)")
	}
	zero := "lofi:lowcut=0,highcut=0,bits=0,wobble=0,drive=0,crackle=0,hiss=0"
	if same, _, err := runCLI(t, in, "-fx", zero); err != nil || same != string(in) {
		t.Errorf("tous les réglages à 0 : le flux doit rester identique (err=%v)", err)
	}
	// quantification seule : chaque échantillon est un multiple du pas
	q, _, err := runCLI(t, in, "-fx", "lofi:lowcut=0,highcut=0,bits=6,wobble=0,drive=0,crackle=0,hiss=0")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range samplesOf(q) {
		if v%1024 != 0 { // 2^(16−6)
			t.Fatalf("6 bits : échantillon %d = %d, attendu un multiple de 1024", i, v)
		}
	}
	for _, tc := range []struct{ args, want string }{
		{"lofi:lowcut=5", "lowcut doit valoir 0 ou être dans"},
		{"lofi:highcut=50", "highcut doit valoir 0 ou être dans"},
		{"lofi:highcut=30000", "highcut doit valoir 0 ou être dans"},
		{"lofi:bits=2.5", "bits doit être un entier de 0 à 16"},
		{"lofi:bits=20", "bits doit être un entier de 0 à 16"},
		{"lofi:samplerate=500", "samplerate doit valoir 0 ou être dans"},
		{"lofi:samplerate=44100", "samplerate doit valoir 0 ou être dans"},
		{"lofi:wobble=2", "wobble doit être dans [0, 1]"},
		{"lofi:crackle=-1", "crackle doit être dans [0, 1]"},
		{"lofi:hiss=1.5", "hiss doit être dans [0, 1]"},
		{"lofi:drive=30", "drive doit être dans [0, 24]"},
		{"lofi:noise=1", `paramètre inconnu "noise"`},
	} {
		if _, _, err := runCLI(t, in, "-fx", tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("-fx %s : erreur %v, attendu %q", tc.args, err, tc.want)
		}
	}
	// -ab : le flux traité reste aligné sur l'original (latence rattrapée) : même longueur
	ab, _, err := runCLI(t, in, "-ab", "-fx", "lofi:wobble=1", "-ab-seconds", "0.3")
	if err != nil || len(ab) != len(in) {
		t.Errorf("-ab avec lofi : err=%v longueur %d/%d", err, len(ab), len(in))
	}
	if list, _, _ := runCLI(t, nil, "-list-fx"); !strings.Contains(list, "lofi") || !strings.Contains(list, "crackle") {
		t.Error("lofi absent de -list-fx")
	}
}
