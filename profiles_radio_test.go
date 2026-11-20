package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func radioProfiles() []Profile {
	var ps []Profile
	for _, p := range Profiles() {
		if isRadio(p) {
			ps = append(ps, p)
		}
	}
	return ps
}

func applyP(t *testing.T, in []byte, fs, ch int, p Profile, intensity float64) []byte {
	t.Helper()
	out, err := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, ch, p, intensity))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRadioProfilesExist(t *testing.T) {
	want := map[Profile]string{
		RadioHits: "radio-hits", RadioRock: "radio-rock", RadioUrban: "radio-urban", RadioClub: "radio-club",
		RadioTalk: "radio-talk", RadioGold: "radio-gold", RadioNatural: "radio-natural",
	}
	if got := len(radioProfiles()); got != len(want) {
		t.Fatalf("%d profils radio, attendu %d", got, len(want))
	}
	for p, name := range want {
		if p.String() != name {
			t.Errorf("%d : nom %q, attendu %q", int(p), p.String(), name)
		}
		if got, ok := ParseProfile(name); !ok || got != p {
			t.Errorf("ParseProfile(%q) = %v, %v", name, got, ok)
		}
		if profileRadio[p].limit == nil || profileRadio[p].multi == nil {
			t.Errorf("%v : multibande et limiteur attendus", p)
		}
	}
	// les valeurs des profils existants ne bougent pas : les nouveaux sont ajoutés à la suite
	if Tube != 9 || RadioHits != 10 {
		t.Errorf("Tube = %d, RadioHits = %d : les constantes existantes ne doivent pas changer", Tube, RadioHits)
	}
}

// Niveau d'un sinus à −18 dBFS : les profils radio sont plus forts que l'entrée
// (compensation de l'AGC et des bandes), dans la fourchette mesurée pour chacun.
func TestRadioProfilesAreLouder(t *testing.T) {
	const fs = 44100
	in := tone(1000, ampOf(-18), fs, 2*fs)
	for p, rng := range map[Profile][2]float64{
		RadioHits: {5, 10}, RadioRock: {7, 12}, RadioUrban: {4, 9}, RadioClub: {8, 13},
		RadioTalk: {5, 10}, RadioGold: {3, 8}, RadioNatural: {1, 6},
	} {
		got := 20 * math.Log10(rms(applyP(t, in, fs, 1, p, 1))/rms(in))
		t.Logf("%-14v : %+.2f dB", p, got)
		if got < rng[0] || got > rng[1] {
			t.Errorf("%v : niveau %+.2f dB, attendu entre %+.0f et %+.0f", p, got, rng[0], rng[1])
		}
	}
}

// Quelle que soit la fréquence, l'intensité ou le nombre de canaux, aucune crête
// ne dépasse le plafond de −1 dBFS, même avec un signal à pleine échelle.
func TestRadioProfilesNeverExceedCeiling(t *testing.T) {
	const fs = 44100
	max := ceilingAmp(radioCeilingDB) + 0.5
	for _, p := range radioProfiles() {
		for _, intensity := range []float64{0.5, 1, 2, MaxIntensity} {
			for _, f := range []float64{40, 100, 400, 1000, 3000, 8000, 15000} {
				mono := tone(f, ampOf(-0.1), fs, fs/2)
				if got := maxAbs(samples(applyP(t, mono, fs, 1, p, intensity))); got > max {
					t.Fatalf("%v mono @%.0f Hz i=%.1f : crête %.0f > %.1f", p, f, intensity, got, max)
				}
				st := stereoPCM(fs/2, sineFn(f, ampOf(-0.1), fs), sineFn(f*1.5, ampOf(-0.1), fs))
				out := applyP(t, st, fs, 2, p, intensity)
				if got := math.Max(maxAbs(channel(out, 0)), maxAbs(channel(out, 1))); got > max {
					t.Fatalf("%v stéréo @%.0f Hz i=%.1f : crête %.0f > %.1f", p, f, intensity, got, max)
				}
			}
		}
	}
}

func TestRadioProfilesKeepLengthAndIdentityAtZero(t *testing.T) {
	const fs = 44100
	in := stereoPCM(5001, sineFn(300, 9000, fs), sineFn(2000, 6000, fs))
	for _, p := range radioProfiles() {
		for _, intensity := range []float64{0, -1, math.NaN()} {
			if out := applyP(t, in, fs, 2, p, intensity); !bytes.Equal(out, in) {
				t.Errorf("%v intensité %v : le signal doit rester intact", p, intensity)
			}
		}
		out := applyP(t, in, fs, 2, p, 1)
		if len(out) != len(in) {
			t.Errorf("%v : longueur %d, attendu %d", p, len(out), len(in))
		}
		if bytes.Equal(out, in) {
			t.Errorf("%v doit modifier le signal", p)
		}
	}
}

// Sans EQ, un profil radio (ici Natural) agit tout de même : le garde-fou de
// ApplyProfile ne doit pas le confondre avec Flat.
func TestRadioNaturalHasNoBandsButActs(t *testing.T) {
	if len(RadioNatural.Bands()) != 0 {
		t.Skip("RadioNatural a désormais une égalisation")
	}
	const fs = 44100
	in := tone(1000, ampOf(-10), fs, fs)
	if bytes.Equal(applyP(t, in, fs, 1, RadioNatural, 1), in) {
		t.Error("RadioNatural doit modifier le signal même sans égalisation")
	}
}

// Élargissement : le rapport côté/milieu monte à 3 kHz pour les profils larges,
// reste quasi identique pour Talk (image centrée) et ne bouge pas dans les basses.
func TestRadioProfilesWidenAboveBassOnly(t *testing.T) {
	const fs = 44100
	sideMid := func(p Profile, freq float64) float64 {
		in := stereoPCM(2*fs, sineFn(freq, 8000, fs), sineFn(freq, 2000, fs)) // M = 5000, S = 3000
		m, s := midSide(t, applyP(t, in, fs, 2, p, 1), freq, fs)
		return s / m
	}
	for _, p := range []Profile{RadioHits, RadioUrban} { // pas de saturation : seul l'élargissement change S/M
		r3k := sideMid(p, 3000) / (3000.0 / 5000.0)
		r60 := sideMid(p, 60) / (3000.0 / 5000.0)
		w := profileRadio[p].width
		t.Logf("%v : S/M ×%.2f à 3 kHz, ×%.2f à 60 Hz (largeur %.2f)", p, r3k, r60, w)
		if math.Abs(r3k/w-1) > 0.1 {
			t.Errorf("%v : S/M ×%.2f à 3 kHz, attendu ≈ ×%.2f", p, r3k, w)
		}
		if math.Abs(20*math.Log10(r60)) > 1.5 {
			t.Errorf("%v : S/M ×%.2f à 60 Hz, les basses doivent rester au centre", p, r60)
		}
	}
	if r := sideMid(RadioTalk, 3000) / (3000.0 / 5000.0); math.Abs(r-1) > 0.05 {
		t.Errorf("RadioTalk : S/M ×%.2f, l'image doit rester inchangée", r)
	}
}

// Les profils radio réduisent la dynamique à court terme moins que le plafond ne
// l'impose : un passage faible reste plus faible qu'un passage fort.
func TestRadioProfilesKeepSomeDynamics(t *testing.T) {
	const fs = 44100
	quiet := tone(1000, ampOf(-30), fs, 2*fs)
	loud := tone(1000, ampOf(-8), fs, 2*fs)
	in := append(append([]byte{}, quiet...), loud...)
	for _, p := range radioProfiles() {
		out := applyP(t, in, fs, 1, p, 1)
		half := len(out) / 2
		inRange := 22.0 // −8 − (−30)
		outRange := 20 * math.Log10(rms(out[half:])/rms(out[:half]))
		t.Logf("%-14v : 22 dB d'écart en entrée → %.1f dB en sortie", p, outRange)
		if outRange < 4 {
			t.Errorf("%v : écart %.1f dB entre faible et fort, la dynamique est écrasée", p, outRange)
		}
		if outRange > inRange {
			t.Errorf("%v : écart %.1f dB, plus grand qu'en entrée", p, outRange)
		}
	}
}

// Les basses fréquences d'échantillonnage ne font pas échouer : les séparations
// au-delà de Nyquist désactivent seulement la multibande ; le limiteur agit toujours.
func TestRadioProfilesLowSampleRate(t *testing.T) {
	const fs = 8000
	in := tone(500, ampOf(-0.1), fs, fs)
	for _, p := range radioProfiles() {
		out := applyP(t, in, fs, 1, p, 1)
		if len(out) != len(in) {
			t.Errorf("%v : longueur %d, attendu %d", p, len(out), len(in))
		}
		if got := maxAbs(samples(out)); got > ceilingAmp(radioCeilingDB)+0.5 {
			t.Errorf("%v : crête %.0f au-delà du plafond", p, got)
		}
	}
}
