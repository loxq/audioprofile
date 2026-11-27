package panel

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"testing"

	"github.com/loxq/audioprofile"
)

func testSignal(n int) []byte {
	b := make([]byte, 0, n*4)
	for i := 0; i < n; i++ {
		v := int16(8000*math.Sin(2*math.Pi*200*float64(i)/44100) + 2000*math.Sin(2*math.Pi*3100*float64(i)/44100))
		for ch := 0; ch < 2; ch++ {
			b = append(b, byte(uint16(v)), byte(uint16(v)>>8))
		}
	}
	return b
}

// Les réglages d'un profil passés par le format de la page ne changent pas le son.
func TestProfileRoundTripKeepsSound(t *testing.T) {
	in := testSignal(20000)
	for _, p := range audioprofile.Profiles() {
		orig := p.Settings()
		back, err := ProfileFrom(orig).Settings()
		if err != nil {
			t.Fatal(err)
		}
		a, _ := io.ReadAll(audioprofile.ApplyProfileSettings(bytes.NewReader(in), 44100, 2, orig, 1))
		b, _ := io.ReadAll(audioprofile.ApplyProfileSettings(bytes.NewReader(in), 44100, 2, back, 1))
		if !bytes.Equal(a, b) {
			t.Errorf("profil %s : le son change après un aller-retour par la page", p)
		}
	}
}

func TestAmpRoundTrip(t *testing.T) {
	for _, a := range audioprofile.TubeAmpPresets() {
		if got := AmpFrom(a.Settings()).Settings(); got != a.Settings() {
			t.Errorf("ampli %s : %+v, attendu %+v", a, got, a.Settings())
		}
	}
}

// Le JSON de la page se relit sans perte, et un étage absent reste null.
func TestProfileJSONRoundTrip(t *testing.T) {
	for _, p := range []audioprofile.Profile{audioprofile.Flat, audioprofile.Rock, audioprofile.RadioHits} {
		want := ProfileFrom(p.Settings())
		b, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		var got Profile
		if err := json.Unmarshal(b, &got); err != nil || !reflect.DeepEqual(&got, want) {
			t.Errorf("%s : %v %+v", p, err, got)
		}
	}
	var flat map[string]any
	b, _ := json.Marshal(ProfileFrom(audioprofile.Flat.Settings()))
	json.Unmarshal(b, &flat)
	if v, ok := flat["compression"]; !ok || v != nil {
		t.Error("un étage absent doit s'écrire null (la page teste null)")
	}
	if bands, _ := flat["bands"].([]any); bands == nil {
		t.Error("bands doit être une liste vide, pas null")
	}
}

func TestProfileFromMakesDefaultsExplicit(t *testing.T) {
	pp := ProfileFrom(audioprofile.ProfileSettings{Bands: []audioprofile.Band{
		{Type: audioprofile.Peaking, FreqHz: 1000, GainDB: 1},
		{Type: audioprofile.Notch, FreqHz: 50},
		{Type: audioprofile.HighPass, FreqHz: 80},
	}})
	if pp.Bands[0].Q != math.Sqrt2/2 || pp.Bands[1].Q != 10 || pp.Bands[2].Order != 2 {
		t.Errorf("valeurs par défaut non explicitées : %+v", pp.Bands)
	}
}

func TestProfileSettingsRejectsUnknownBandType(t *testing.T) {
	if _, err := (&Profile{Bands: []Band{{Type: "shelf"}}}).Settings(); err == nil {
		t.Error("un type de bande inconnu doit être refusé")
	}
}

// Les curseurs du schéma ont des bornes saines, et leur valeur de départ est dedans.
func TestSchemaFieldsAreSane(t *testing.T) {
	for _, rate := range []int{22050, 44100, 96000} {
		s := NewSchema(rate, 2)
		groups := map[string][]Field{"band": s.Band, "compressor": s.Compressor, "saturation": s.Saturation, "limiter": s.Limiter, "amp": s.Amp,
			"intensity": {s.Intensity}, "width": {s.Width}}
		for name, fields := range groups {
			for _, f := range fields {
				if !(f.Min < f.Max) || f.Def < f.Min || f.Def > f.Max || (f.Log && f.Min <= 0) || (f.Step < 0) || (!f.Log && f.Step == 0) {
					t.Errorf("%s.%s (%d Hz) : %+v", name, f.Key, rate, f)
				}
			}
		}
		if s.Band[0].Max >= float64(rate)/2 {
			t.Errorf("%d Hz : la fréquence maximale d'une bande (%g) doit rester sous Nyquist", rate, s.Band[0].Max)
		}
		if s.Defaults.Compression == nil || s.Defaults.Saturation == nil || s.Defaults.Limiter == nil || s.Defaults.Multiband == nil ||
			len(s.Defaults.Multiband.Bands) != len(s.Defaults.Multiband.Crossovers)+1 {
			t.Errorf("%d Hz : valeurs de départ des étages incomplètes", rate)
		}
	}
	// chaque type de bande de la page est reconnu par la bibliothèque
	for _, n := range NewSchema(44100, 2).BandTypes {
		if _, ok := audioprofile.ParseBandType(n); !ok {
			t.Errorf("type de bande %q inconnu de la bibliothèque", n)
		}
	}
	if len(NewSchema(44100, 2).BandTypes) != len(audioprofile.BandTypes()) {
		t.Error("le sélecteur doit proposer tous les types de bande")
	}
}

func TestNumField(t *testing.T) {
	for _, tc := range []struct {
		lo, hi, step float64
		log, integer bool
	}{{0, 1, 0.01, false, false}, {-24, 24, 0.5, false, false}, {0, 24, 0.1, false, false}, {0, 2000, 1, false, false}, {1, 8, 1, false, true}, {20, 20000, 0, true, false}} {
		if f := NumField("k", "k", "", tc.lo, tc.hi, tc.lo, tc.log, tc.integer); f.Step != tc.step {
			t.Errorf("[%g, %g] log=%v int=%v : pas %g, attendu %g", tc.lo, tc.hi, tc.log, tc.integer, f.Step, tc.step)
		}
	}
}
