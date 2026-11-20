package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func TestAmpPresetNames(t *testing.T) {
	for _, p := range TubeAmpPresets() {
		got, ok := ParseTubeAmpPreset(p.String())
		if !ok || got != p {
			t.Errorf("ParseTubeAmpPreset(%q) = %v, %v", p.String(), got, ok)
		}
	}
	if p, ok := ParseTubeAmpPreset("  CRUNCH "); !ok || p != AmpCrunch {
		t.Error("ParseTubeAmpPreset doit ignorer casse et espaces")
	}
	if _, ok := ParseTubeAmpPreset("fuzz"); ok {
		t.Error("un nom inconnu ne doit pas être reconnu")
	}
	if TubeAmpPreset(99).String() != "unknown" || TubeAmpPreset(99).Settings() != (TubeAmpSettings{}) {
		t.Error("préréglage inconnu mal géré")
	}
	if len(TubeAmpPresets()) != 3 {
		t.Errorf("3 préréglages attendus, %d trouvés", len(TubeAmpPresets()))
	}
}

func TestAmpPresetSettingsIsCopy(t *testing.T) {
	s := AmpCrunch.Settings()
	s.DriveDB = 99
	s.Cabinet = true
	if AmpCrunch.Settings().DriveDB == 99 || AmpCrunch.Settings().Cabinet {
		t.Error("Settings() doit renvoyer une copie")
	}
	for _, p := range TubeAmpPresets() {
		if p.Settings().Cabinet {
			t.Errorf("%v : baffle désactivé par défaut (usage sur un morceau complet)", p)
		}
	}
}

func TestTubeAmpSettingsScaled(t *testing.T) {
	base := TubeAmpSettings{DriveDB: 6, Stages: 3, Bias: 0.1, BassDB: 2, MidDB: -1, TrebleDB: 1.5, Cabinet: true, OutputDB: -2}
	if got := base.Scaled(1); got != base {
		t.Errorf("intensité 1 : %+v", got)
	}
	zero := base.Scaled(0)
	if zero.DriveDB != 0 || zero.Bias != 0 || zero.BassDB != 0 || zero.MidDB != 0 || zero.TrebleDB != 0 || zero.OutputDB != 0 {
		t.Errorf("intensité 0 : %+v", zero)
	}
	if zero.Stages != 3 || !zero.Cabinet {
		t.Error("le nombre d'étages et Cabinet ne doivent pas changer")
	}
	two := base.Scaled(2)
	if two.DriveDB != 12 || two.Bias != 0.2 || two.BassDB != 4 || two.MidDB != -2 || two.TrebleDB != 3 || two.OutputDB != -4 {
		t.Errorf("intensité 2 : %+v", two)
	}
	// gain de sortie positif : plafonné à sa valeur d'intensité 1
	pos := TubeAmpSettings{DriveDB: 6, OutputDB: 3}
	if got := pos.Scaled(2).OutputDB; got != 3 {
		t.Errorf("gain de sortie positif à intensité 2 : %v, attendu 3", got)
	}
	if got := pos.Scaled(0.5).OutputDB; got != 1.5 {
		t.Errorf("gain de sortie positif à intensité 0,5 : %v, attendu 1,5", got)
	}
	// plafonnée à MaxIntensity ; négative et NaN valent 0
	if base.Scaled(10) != base.Scaled(MaxIntensity) {
		t.Error("l'intensité doit être plafonnée à MaxIntensity")
	}
	if base.Scaled(-1) != zero || base.Scaled(math.NaN()) != zero {
		t.Error("intensité négative ou NaN : équivalent à 0")
	}
}

func TestAmpPresetZeroIntensityIsIdentity(t *testing.T) {
	in := tone(1000, 8000, 44100, 4000)
	for _, p := range TubeAmpPresets() {
		if !bytes.Equal(in, runAmp(t, in, 1, p.Settings().Scaled(0))) {
			t.Errorf("%v à intensité 0 : signal intact attendu", p)
		}
	}
}

// À un niveau d'écoute normal, chaque préréglage rend à peu près le niveau d'entrée.
func TestAmpPresetsLevelNearUnity(t *testing.T) {
	in := tone(1000, ampOf(-18), 44100, 44100)
	for _, p := range TubeAmpPresets() {
		out := runAmp(t, in, 1, p.Settings())
		got := 20 * math.Log10(rms(out)/rms(in))
		t.Logf("%-7v : %+.2f dB", p, got)
		if math.Abs(got) > 0.75 {
			t.Errorf("%v : niveau %+.2f dB, attendu ≈ 0 ± 0.75", p, got)
		}
	}
}

// La distorsion croît de Clean à Crunch puis Lead.
func TestAmpPresetsDistortionOrder(t *testing.T) {
	const fs = 44100
	in := tone(200, ampOf(-12), fs, fs)
	thd := func(p TubeAmpPreset) float64 {
		out := runAmp(t, in, 1, p.Settings())
		f := goertzel(out, 200, fs)
		var sum float64
		for h := 2; h <= 6; h++ {
			a := goertzel(out, 200*float64(h), fs)
			sum += a * a
		}
		return math.Sqrt(sum) / f
	}
	c, k, l := thd(AmpClean), thd(AmpCrunch), thd(AmpLead)
	t.Logf("distorsion harmonique : clean %.1f %%, crunch %.1f %%, lead %.1f %%", 100*c, 100*k, 100*l)
	if !(c > 0.005 && c < k && k < l) {
		t.Errorf("la distorsion doit croître (%.4f < %.4f < %.4f)", c, k, l)
	}
}

// Aucun écrêtage à -3 dBFS, avec ou sans baffle, y compris à intensité exagérée.
func TestAmpPresetsNoClipping(t *testing.T) {
	const fs = 44100
	for _, p := range TubeAmpPresets() {
		for _, intensity := range []float64{1, 2, MaxIntensity} {
			for _, cab := range []bool{false, true} {
				s := p.Settings().Scaled(intensity)
				s.Cabinet = cab
				for _, f := range []float64{40, 100, 400, 1000, 3000, 8000, 15000} {
					out := runAmp(t, tone(f, ampOf(-3), fs, fs), 1, s)
					for i := 0; i+1 < len(out); i += 2 {
						if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v == 32767 || v == -32768 {
							t.Fatalf("%v intensité %.0f baffle=%v @%.0f Hz : écrêtage à l'échantillon %d", p, intensity, cab, f, i/2)
						}
					}
				}
			}
		}
	}
}

func TestAmpPresetsSilenceAndChunks(t *testing.T) {
	silence := make([]byte, 4*2000)
	l := tone(150, 15000, 44100, 22050)
	mix := make([]byte, 0, 2*len(l))
	for i := 0; i+1 < len(l); i += 2 {
		mix = append(mix, l[i], l[i+1], l[i+1], l[i]) // canaux différents
	}
	for _, p := range TubeAmpPresets() {
		s := p.Settings()
		if out := runAmp(t, silence, 2, s); !bytes.Equal(silence, out) {
			t.Errorf("%v : le silence doit rester un silence exact", p)
		}
		whole := runAmp(t, mix, 2, s)
		chunked, _ := io.ReadAll(TubeAmp(io.MultiReader(bytes.NewReader(mix[:7]), bytes.NewReader(mix[7:])), 44100, 2, s))
		if !bytes.Equal(whole, chunked) {
			t.Errorf("%v : la lecture fragmentée doit donner le même résultat", p)
		}
	}
}
