package audioprofile

import (
	"bytes"
	"math"
	"testing"
)

// constPCM renvoie n trames mono d'une valeur constante.
func constPCM(v int16, n int) []byte {
	b := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		b = append(b, byte(uint16(v)), byte(uint16(v)>>8))
	}
	return b
}

func TestRMSLevel(t *testing.T) {
	const fs = 44100
	sin := tone(1000, 10000, fs, fs)
	want := 20 * math.Log10(10000/math.Sqrt2/32768)
	if got := rmsLevelDB(sin, fs, 1); math.Abs(got-want) > 0.05 {
		t.Errorf("niveau %.3f dB, attendu %.3f", got, want)
	}
	// un long silence numérique ne tire pas la mesure vers le bas
	padded := append(append([]byte{}, sin...), make([]byte, 4*fs)...)
	if got := rmsLevelDB(padded, fs, 1); math.Abs(got-want) > 0.05 {
		t.Errorf("avec silence : %.3f dB, attendu %.3f", got, want)
	}
	if got := rmsLevelDB(make([]byte, 2*fs), fs, 1); got != silenceLevelDB {
		t.Errorf("silence : %.1f, attendu %.1f", got, silenceLevelDB)
	}
	if rmsLevelDB(sin, 0, 1) != silenceLevelDB || rmsLevelDB(sin, fs, 0) != silenceLevelDB || rmsLevelDB(nil, fs, 1) != silenceLevelDB {
		t.Error("paramètres invalides ou flux vide : silenceLevelDB attendu")
	}
}

func TestMatchGains(t *testing.T) {
	const fs = 44100
	quiet := tone(1000, ampOf(-20), fs, fs)
	loud := tone(1000, ampOf(-14), fs, fs)
	for name, tc := range map[string]struct {
		orig, proc   []byte
		wantO, wantP float64
	}{
		"traité plus fort":   {quiet, loud, 0, -6},
		"original plus fort": {loud, quiet, -6, 0},
		"niveaux égaux":      {quiet, quiet, 0, 0},
		"silence":            {quiet, make([]byte, len(quiet)), 0, 0},
	} {
		gO, gP := MatchGains(tc.orig, tc.proc, fs, 1)
		if math.Abs(gO-tc.wantO) > 0.05 || math.Abs(gP-tc.wantP) > 0.05 {
			t.Errorf("%s : gains (%.2f, %.2f), attendu (%.0f, %.0f)", name, gO, gP, tc.wantO, tc.wantP)
		}
		if gO > 0 || gP > 0 {
			t.Errorf("%s : un gain positif risquerait d'écrêter", name)
		}
	}
	// une fois appliqués, les niveaux sont égaux
	gO, gP := MatchGains(quiet, loud, fs, 1)
	lo := rmsLevelDB(ApplyGain(quiet, gO), fs, 1)
	lp := rmsLevelDB(ApplyGain(loud, gP), fs, 1)
	if math.Abs(lo-lp) > 0.05 {
		t.Errorf("niveaux après gain : %.3f et %.3f dB", lo, lp)
	}
}

func TestApplyGain(t *testing.T) {
	in := constPCM(1000, 10)
	if out := ApplyGain(in, 0); !bytes.Equal(out, in) {
		t.Error("0 dB doit laisser le signal identique")
	}
	if out := ApplyGain(in, math.NaN()); !bytes.Equal(out, in) {
		t.Error("NaN doit laisser le signal identique")
	}
	if got := samples(ApplyGain(in, -20*math.Log10(2)))[0]; got != 500 {
		t.Errorf("-6,02 dB : %d, attendu 500", got)
	}
	if got := samples(ApplyGain(constPCM(30000, 4), 20))[0]; got != 32767 {
		t.Errorf("+20 dB : %d, attendu l'écrêtage à 32767", got)
	}
	if got := samples(ApplyGain(constPCM(-30000, 4), 20))[0]; got != -32768 {
		t.Errorf("+20 dB : %d, attendu l'écrêtage à -32768", got)
	}
	if out := ApplyGain(append(append([]byte{}, in...), 7), -3); len(out) != len(in) {
		t.Errorf("l'octet isolé en fin de flux doit être ignoré (%d octets)", len(out))
	}
	if &ApplyGain(in, 0)[0] == &in[0] {
		t.Error("ApplyGain doit renvoyer une copie")
	}
}

// 1 000 trames par seconde et un fondu de 15 ms (15 trames, de -7 à +8 autour de la frontière).
const altFS, altSeg, altFade, altHalf = 1000, 1000, 15, 7

func TestAlternateSegmentsAndFades(t *testing.T) {
	const n = 3500 // 3 segments et demi : le dernier est partiel
	a, b := constPCM(1000, n), constPCM(5000, n)
	out := samples(Alternate(a, b, altFS, 1, 1))
	if len(out) != n {
		t.Fatalf("longueur %d, attendu %d", len(out), n)
	}
	for i, v := range out {
		k := i / altSeg
		inWindow := false
		for m := 1; m <= 3; m++ {
			if i >= m*altSeg-altHalf && i < m*altSeg+altFade-altHalf {
				inWindow = true
			}
		}
		if inWindow {
			continue
		}
		want := int16(1000)
		if k%2 == 1 {
			want = 5000
		}
		if v != want {
			t.Fatalf("trame %d (segment %d) : %d, attendu %d (hors fondu : la source doit être pure)", i, k, v, want)
		}
	}
	// le premier segment commence par A, le premier fondu va de A vers B, linéairement
	if out[0] != 1000 {
		t.Errorf("la première trame doit être A (%d)", out[0])
	}
	prev := int16(1000)
	for i := altSeg - altHalf; i < altSeg+altFade-altHalf; i++ {
		if out[i] <= prev {
			t.Fatalf("fondu A→B : pas strictement croissant à la trame %d (%d ≤ %d)", i, out[i], prev)
		}
		if step := out[i] - prev; i > altSeg-altHalf && math.Abs(float64(step)-4000.0/altFade) > 2 {
			t.Fatalf("fondu non linéaire à la trame %d : pas de %d, attendu ≈ %.0f", i, step, 4000.0/altFade)
		}
		prev = out[i]
	}
	// fondu B→A : strictement décroissant
	prev = 5000
	for i := 2*altSeg - altHalf; i < 2*altSeg+altFade-altHalf; i++ {
		if out[i] >= prev {
			t.Fatalf("fondu B→A : pas strictement décroissant à la trame %d", i)
		}
		prev = out[i]
	}
}

// Deux flux identiques : le fondu à gain constant ne change rien, bit à bit.
func TestAlternateIdenticalSourcesAreBitExact(t *testing.T) {
	const fs = 44100
	a := tone(440, 12000, fs, 3*fs)
	for _, seg := range []float64{0.01, 0.5, 1} {
		if out := Alternate(a, a, fs, 1, seg); !bytes.Equal(out, a) {
			t.Errorf("segments de %g s : deux flux identiques doivent ressortir identiques", seg)
		}
	}
	st := stereoPCM(5000, sineFn(300, 9000, fs), sineFn(700, 4000, fs))
	if out := Alternate(st, st, fs, 2, 0.05); !bytes.Equal(out, st) {
		t.Error("stéréo : deux flux identiques doivent ressortir identiques")
	}
}

// En stéréo, les deux canaux restent entrelacés dans le fondu et dans les segments.
func TestAlternateStereoInterleaving(t *testing.T) {
	const n = 3000
	a := stereoPCM(n, func(int) float64 { return 1000 }, func(int) float64 { return -1000 })
	b := stereoPCM(n, func(int) float64 { return 5000 }, func(int) float64 { return -5000 })
	out := Alternate(a, b, altFS, 2, 1)
	if len(out) != len(a) {
		t.Fatalf("longueur %d, attendu %d", len(out), len(a))
	}
	l, r := channel(out, 0), channel(out, 1)
	for i := range l {
		if l[i] != -r[i] {
			t.Fatalf("trame %d : gauche %d, droite %d, ils doivent rester opposés (entrelacement)", i, l[i], r[i])
		}
	}
	if l[500] != 1000 || l[1500] != 5000 || l[2500] != 1000 {
		t.Errorf("segments : %d, %d, %d", l[500], l[1500], l[2500])
	}
}

func TestAlternateLengthAndInvalid(t *testing.T) {
	a, b := constPCM(1000, 3000), constPCM(5000, 2000)
	if got := len(Alternate(a, b, altFS, 1, 1)); got != 2*2000 {
		t.Errorf("longueur %d, attendu celle du plus court (%d octets)", got, 2*2000)
	}
	odd := append(constPCM(1, 10), 9)
	if got := len(Alternate(odd, constPCM(2, 10), altFS, 1, 1)); got != 20 {
		t.Errorf("octet isolé : longueur %d, attendu 20", got)
	}
	for name, out := range map[string][]byte{
		"fréquence nulle": Alternate(a, b, 0, 1, 1),
		"canaux nuls":     Alternate(a, b, altFS, 0, 1),
		"durée nulle":     Alternate(a, b, altFS, 1, 0),
		"durée négative":  Alternate(a, b, altFS, 1, -1),
		"durée NaN":       Alternate(a, b, altFS, 1, math.NaN()),
		"durée infinie":   Alternate(a, b, altFS, 1, math.Inf(1)),
	} {
		if !bytes.Equal(out, a) {
			t.Errorf("%s : a doit être renvoyé tel quel", name)
		}
	}
	// segment plus court que le fondu : pas de panique, longueur conservée
	short := Alternate(constPCM(1000, 500), constPCM(5000, 500), altFS, 1, 0.004)
	if len(short) != 1000 {
		t.Errorf("segments très courts : longueur %d", len(short))
	}
}
