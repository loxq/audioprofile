package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func TestProfileNames(t *testing.T) {
	for _, p := range Profiles() {
		got, ok := ParseProfile(p.String())
		if !ok || got != p {
			t.Errorf("ParseProfile(%q) = %v, %v", p.String(), got, ok)
		}
	}
	if p, ok := ParseProfile("  ROCK "); !ok || p != Rock {
		t.Error("ParseProfile doit ignorer casse et espaces")
	}
	if _, ok := ParseProfile("inconnu"); ok {
		t.Error("un nom inconnu ne doit pas être reconnu")
	}
	if Profile(99).String() != "unknown" || Profile(99).Bands() != nil {
		t.Error("profil inconnu mal géré")
	}
}

func TestProfileBandsIsCopy(t *testing.T) {
	b := Rock.Bands()
	b[0].GainDB = 99
	if Rock.Bands()[0].GainDB == 99 {
		t.Error("Bands() doit renvoyer une copie")
	}
}

func TestProfileIdentity(t *testing.T) {
	in := sine(1000, 44100, 2000)
	for name, tc := range map[string]struct {
		p         Profile
		intensity float64
	}{
		"flat":            {Flat, 1},
		"profil inconnu":  {Profile(99), 1},
		"intensité nulle": {Rock, 0},
		"intensité < 0":   {Rock, -1},
		"intensité NaN":   {Rock, math.NaN()},
	} {
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), 44100, 1, tc.p, tc.intensity))
		if !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
}

// Aucun profil ne doit dépasser 0 dB (grâce au gain global automatique),
// à aucune intensité, et chacun doit bien modifier le son.
func TestProfilesPeakAtZeroDB(t *testing.T) {
	for _, fs := range []float64{44100, 48000} {
		for _, p := range Profiles() {
			if p == Flat {
				continue
			}
			for _, intensity := range []float64{0.5, 1} {
				bands := p.Bands()
				for i := range bands {
					bands[i].GainDB *= intensity
				}
				peak := peakResponseDB(fs, designBands(fs, bands))
				bands = append(bands, Band{Type: Gain, GainDB: -peak})
				got := peakResponseDB(fs, designBands(fs, bands))
				if math.Abs(got) > 0.01 {
					t.Errorf("%v @%.0f i=%.1f : pic à %+.3f dB, attendu 0", p, fs, intensity, got)
				}
			}
		}
	}
}

// withoutDynamics désactive compresseurs et saturations le temps d'un test,
// pour n'examiner que la courbe d'égalisation.
func withoutDynamics(t *testing.T) {
	t.Helper()
	comp, sat := profileCompression, profileSaturation
	profileCompression = [numProfiles]*CompressorSettings{}
	profileSaturation = [numProfiles]struct{ driveDB, outputDB float64 }{}
	t.Cleanup(func() { profileCompression, profileSaturation = comp, sat })
}

func TestProfileShape(t *testing.T) {
	withoutDynamics(t)
	const fs = 44100
	ratio := func(p Profile, freq float64) float64 {
		in := sine(freq, fs, fs)
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, 1))
		return rms(out) / rms(in)
	}
	// Rock : sourire (basses et aigus > médiums), avec le pic à 0 dB
	if !(ratio(Rock, 40) > ratio(Rock, 400)*1.5 && ratio(Rock, 15000) > ratio(Rock, 400)*1.5) {
		t.Error("Rock : les extrêmes doivent dominer les médiums")
	}
	// Vocal : graves réduits, présence relevée
	if !(ratio(Vocal, 40) < ratio(Vocal, 3500)*0.6) {
		t.Error("Vocal : les graves doivent être nettement sous la présence")
	}
	// Hip-hop : rapport basses/médiums plus élevé que Classical
	// (les profils sont normalisés à 0 dB au pic : on compare des rapports)
	bm := func(p Profile) float64 { return ratio(p, 40) / ratio(p, 1000) }
	if !(bm(HipHop) > bm(Classical)*1.5) {
		t.Errorf("HipHop doit avoir bien plus de basses relatives que Classical (%.2f vs %.2f)", bm(HipHop), bm(Classical))
	}
	// Sans compresseur ni saturation, un profil ne rend jamais plus fort que
	// l'entrée (pic à 0 dB). Les autres relèvent volontairement le niveau
	// (compensation, saturation) : leur absence d'écrêtage est vérifiée dans
	// TestProfilesNoClipping.
	for _, p := range Profiles() {
		if hasDynamicsEffects(p) {
			continue
		}
		for _, f := range []float64{40, 100, 400, 1000, 3000, 8000, 15000} {
			if r := ratio(p, f); r > 1.02 {
				t.Errorf("%v à %.0f Hz : ratio %.3f > 1", p, f, r)
			}
		}
	}
}

func TestProfileStereoOddChunks(t *testing.T) {
	in := sine(60, 44100, 2*3001)
	out, err := io.ReadAll(ApplyProfile(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), 44100, 2, Rock, 0.7))
	ref, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), 44100, 2, Rock, 0.7))
	if err != nil || !bytes.Equal(out, ref) {
		t.Fatalf("err=%v : la lecture fragmentée doit donner le même résultat", err)
	}
}

// hasDynamicsEffects indique si le profil a un compresseur ou une saturation.
func hasDynamicsEffects(p Profile) bool {
	return profileCompression[p] != nil || profileSaturation[p] != struct{ driveDB, outputDB float64 }{}
}

// Les profils avec effets rendent à peu près le niveau d'entrée à un niveau
// d'écoute normal (-18 dBFS, 1 kHz) : compensation du gain automatique.
func TestProfilesLevelNearUnity(t *testing.T) {
	const fs = 44100
	for _, p := range Profiles() {
		if !hasDynamicsEffects(p) {
			continue
		}
		in := tone(1000, ampOf(-18), fs, fs)
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, 1))
		got := 20 * math.Log10(rms(out)/rms(in))
		t.Logf("%-10v : %+.2f dB", p, got)
		if math.Abs(got) > 0.75 {
			t.Errorf("%v : niveau %+.2f dB, attendu ≈ 0 ± 0.75", p, got)
		}
	}
}

// À -3 dBFS (crête réaliste) aucun profil ne doit écrêter, à aucune fréquence.
func TestProfilesNoClipping(t *testing.T) {
	const fs = 44100
	for _, p := range Profiles() {
		for _, f := range []float64{40, 100, 400, 1000, 3000, 8000, 15000} {
			in := tone(f, ampOf(-3), fs, fs)
			out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, 1))
			for i := 0; i+1 < len(out); i += 2 {
				if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v == 32767 || v == -32768 {
					t.Fatalf("%v @%.0f Hz : écrêtage à l'échantillon %d", p, f, i/2)
				}
			}
		}
	}
}
