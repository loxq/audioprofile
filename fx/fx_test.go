package fx

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
)

func TestBoundsCoverEveryParam(t *testing.T) {
	for _, d := range Defs() {
		for _, p := range d.Params {
			b, ok := fxBounds[d.Name][p.Key]
			if !ok {
				t.Errorf("%s.%s : bornes absentes de fxBounds", d.Name, p.Key)
				continue
			}
			if !(p.Min < p.Max) || (p.Log && p.Min <= 0) {
				t.Errorf("%s.%s : plage incohérente [%g, %g]", d.Name, p.Key, p.Min, p.Max)
			}
			if p.Min != b.lo || p.Max != b.hi {
				t.Errorf("%s.%s : plage non appliquée", d.Name, p.Key)
			}
			if !p.Required && (p.Default < p.Min || p.Default > p.Max) {
				t.Errorf("%s.%s : défaut %g hors de la plage [%g, %g]", d.Name, p.Key, p.Default, p.Min, p.Max)
			}
		}
		if len(fxBounds[d.Name]) != len(d.Params) {
			t.Errorf("%s : fxBounds a des paramètres qui n'existent pas", d.Name)
		}
	}
	for name := range fxBounds {
		if Lookup(name) == nil {
			t.Errorf("fxBounds : effet %q inconnu", name)
		}
	}
	if p := Lookup("delay").Params[4]; !p.Bool || p.Key != "pingpong" {
		t.Errorf("pingpong doit être un booléen : %+v", p)
	}
	if p := Lookup("hpf").Params[1]; !p.Int || p.Unit != "" {
		t.Errorf("order doit être un entier : %+v", p)
	}
	if p := Lookup("hpf").Params[0]; !p.Log || p.Unit != "Hz" {
		t.Errorf("freq doit être logarithmique en Hz : %+v", p)
	}
}

// Les extrémités de chaque plage sont acceptées par la validation de l'effet (les autres paramètres au défaut),
// sauf les dépendances entre paramètres qu'un curseur seul ne sait pas respecter.
func TestRangeEndsAreValid(t *testing.T) {
	skip := map[string]bool{"chorus.depth": true, "flanger.depth": true, "chorus.delay": true, "flanger.delay": true,
		"lofi.lowcut": true, "lofi.highcut": true, "lofi.samplerate": true}
	for _, d := range Defs() {
		if d.check == nil {
			continue
		}
		for _, f := range d.Params {
			if skip[d.Name+"."+f.Key] {
				continue
			}
			for _, v := range []float64{f.Min, f.Max, f.Default} {
				vals := map[string]float64{}
				for _, p := range d.Params {
					vals[p.Key] = p.Default
					if p.Required {
						vals[p.Key] = 1000
					}
				}
				vals[f.Key] = v
				if f.Required && v == f.Default {
					continue
				}
				if err := d.check(vals, 44100, 2); err != nil {
					t.Errorf("%s.%s = %g : %v", d.Name, f.Key, v, err)
				}
			}
		}
	}
}

func TestLookupAndNames(t *testing.T) {
	if Lookup("  Reverb ") == nil || Lookup("reverbe") != nil || Lookup("") != nil {
		t.Error("Lookup : casse et espaces ignorés, inconnu = nil")
	}
	names := Names()
	if len(names) != len(Defs()) || names[0] != "hpf" {
		t.Errorf("Names : %v", names)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Errorf("effet %q en double", n)
		}
		seen[n] = true
	}
	l := Listing()
	for _, want := range []string{"hpf", "reverb", "transient", "obligatoire", "défaut 2"} {
		if !strings.Contains(l, want) {
			t.Errorf("Listing : %q absent", want)
		}
	}
}

func TestParse(t *testing.T) {
	e, err := Parse(" Delay : Time=120 , feedback=0.2 ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Def.Name != "delay" || e.Vals["time"] != 120 || e.Vals["feedback"] != 0.2 || e.Vals["mix"] != 0.3 || len(e.Vals) != 5 || e.Spec != "Delay : Time=120 , feedback=0.2" {
		t.Errorf("Parse : %+v", e)
	}
	if e, err := Parse("delay:", nil); err != nil || e.Vals["time"] != 350 {
		t.Errorf("deux-points sans paramètre : %v %+v", err, e)
	}
	for spec, want := range map[string]string{
		"":                      "effet inconnu",
		"wah":                   `effet inconnu "wah"`,
		"hpf":                   `paramètre "freq" obligatoire`,
		"hpf:order=2":           `paramètre "freq" obligatoire`,
		"hpf:freq=100,cutoff=3": `paramètre inconnu "cutoff"`,
		"hpf:freq=100,freq=200": "donné deux fois",
		"hpf:freq":              "cle=valeur",
		"hpf:freq=beaucoup":     "pas un nombre fini",
		"hpf:freq=NaN":          "pas un nombre fini",
		"hpf:freq=+Inf":         "pas un nombre fini",
	} {
		if _, err := Parse(spec, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) : %v, attendu %q", spec, err, want)
		}
	}
}

func TestNewAndDefaults(t *testing.T) {
	defaults := map[string]map[string]float64{"delay": {"time": 500, "pingpong": 1}, "hpf": {"freq": 80}}
	e, err := New("delay", map[string]float64{"feedback": 0.1}, defaults)
	if err != nil || e.Vals["time"] != 500 || e.Vals["pingpong"] != 1 || e.Vals["feedback"] != 0.1 || e.Vals["mix"] != 0.3 {
		t.Errorf("New avec défauts : %v %+v", err, e)
	}
	if e, _ = New("delay", map[string]float64{"time": 100}, defaults); e.Vals["time"] != 100 {
		t.Error("une valeur donnée l'emporte sur les défauts")
	}
	if e, err = New("hpf", nil, defaults); err != nil || e.Vals["freq"] != 80 {
		t.Errorf("un défaut rend un paramètre obligatoire facultatif : %v %+v", err, e)
	}
	if _, err = New("hpf", nil, nil); err == nil {
		t.Error("sans défaut, freq reste obligatoire")
	}
	if _, err = New("hpf", map[string]float64{"freq": math.NaN()}, nil); err == nil {
		t.Error("NaN doit être refusé")
	}
	if _, err = New("hpf", map[string]float64{"freq": 100, "bogus": 1}, nil); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Errorf("paramètre inconnu : %v", err)
	}
	if _, err = New("nope", nil, nil); err == nil {
		t.Error("effet inconnu")
	}
}

func TestValidateAndApply(t *testing.T) {
	e, _ := Parse("reverb:room=1.5", nil)
	if err := e.Validate(44100, 2); err == nil || !strings.Contains(err.Error(), "room doit être dans [0, 1]") || !strings.HasPrefix(err.Error(), "fx reverb : ") {
		t.Errorf("Validate : %v", err)
	}
	e, _ = Parse("hpf:freq=30000", nil)
	if err := e.Validate(44100, 2); err == nil {
		t.Error("freq au-delà de Nyquist")
	}
	if err := e.Validate(96000, 2); err != nil {
		t.Errorf("freq valable à 96 kHz : %v", err)
	}
	// chaque effet, avec ses défauts, se valide et s'applique sans changer la longueur du flux
	in := make([]byte, 4*3000)
	for i := range in {
		in[i] = byte(i * 7)
	}
	for _, d := range Defs() {
		vals := map[string]float64{}
		for _, p := range d.Params {
			if p.Required {
				vals[p.Key] = 1000
			}
		}
		e, err := New(d.Name, vals, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Validate(44100, 2); err != nil {
			t.Errorf("%s avec ses défauts : %v", d.Name, err)
			continue
		}
		out, err := io.ReadAll(e.Apply(bytes.NewReader(in), 44100, 2))
		if err != nil || len(out) != len(in) {
			t.Errorf("%s : %d octets, attendu %d (%v)", d.Name, len(out), len(in), err)
		}
	}
}
