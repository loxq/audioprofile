package live

import (
	"bytes"
	"io"
	"math"
	"testing"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/panel"
)

// meteredTuner monte une chaîne mesurée avant et après, comme le fait un programme qui branche le panneau : un Meter sur
// la source, un Meter sur le flux traité, et le Tuner qui les connaît.
func meteredTuner(t *testing.T, pl Pipeline, in []byte) (*Tuner, io.Reader) {
	t.Helper()
	pl.Rate, pl.Channels, pl.Intensity = 44100, 2, 1
	build, err := pl.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	meterIn, meterOut := audioprofile.NewMeter(44100, 2), audioprofile.NewMeter(44100, 2)
	sw := audioprofile.NewSwitcher(meterIn.Wrap(bytes.NewReader(in)), 44100, 2, build)
	tuner, err := NewTuner(nil, pl, sw)
	if err != nil {
		t.Fatal(err)
	}
	tuner.SetMeters(meterIn, meterOut)
	return tuner, meterOut.Wrap(sw)
}

func TestTunerWithoutMetersDoesNotMeasure(t *testing.T) {
	p, _ := newTestTuner(t, nil, Pipeline{}, pcm(100, 1000, 2))
	if _, ok := p.Meters(); ok {
		t.Error("sans SetMeters, Meters doit renvoyer ok = faux")
	}
	if f := p.Schema().MeterFreqs; len(f) != 0 {
		t.Errorf("sans mesure, le schéma ne doit pas annoncer de bandes : %v", f)
	}
}

// Le Tuner mesure l'entrée et la sortie ; un réglage fait par le panneau se lit dans l'écart entre les deux.
func TestTunerMetersShowWhatTheChainDoes(t *testing.T) {
	in := pcm(1000, 3*44100, 2) // sinus d'amplitude 8000
	tuner, out := meteredTuner(t, Pipeline{}, in)

	if f := tuner.Schema().MeterFreqs; len(f) != 64 {
		t.Fatalf("bandes annoncées : %d", len(f))
	}
	head := make([]byte, 4*44100)
	if _, err := io.ReadFull(out, head); err != nil {
		t.Fatal(err)
	}
	f, ok := tuner.Meters()
	if !ok {
		t.Fatal("pas de mesure")
	}
	want := 20 * math.Log10(8000.0/32768)
	for side, s := range map[string]panel.MeterSide{"entrée": f.In, "sortie": f.Out} {
		if math.Abs(s.PeakDB[0]-want) > 0.1 || len(s.PeakDB) != 2 || len(s.Spectrum) != 64 {
			t.Errorf("%s : crête %v, attendu %.2f ; %d bandes", side, s.PeakDB, want, len(s.Spectrum))
		}
	}
	if len(f.Curve) != 0 {
		t.Errorf("sans traitement, pas de courbe : %v", f.Curve)
	}

	// un gain de −12 dB réglé depuis le panneau : la sortie baisse de 12 dB, l'entrée ne bouge pas
	st := tuner.State()
	st.Kind, st.Name, st.Profile = panel.KindProfile, "reglage", &panel.Profile{Bands: []panel.Band{{Type: "gain", Gain: -12}}}
	if err := tuner.Apply(st); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(out, head[:2*44100]); err != nil {
		t.Fatal(err)
	}
	f, _ = tuner.Meters()
	if d := f.Out.PeakDB[0] - f.In.PeakDB[0]; math.Abs(d+12) > 0.5 {
		t.Errorf("écart sortie − entrée : %.2f dB, attendu −12", d)
	}
	if len(f.Curve) != 64 {
		t.Fatalf("courbe : %d points", len(f.Curve))
	}
	for i, g := range f.Curve {
		if math.Abs(g+12) > 0.01 { // un gain global : plat à −12 dB
			t.Errorf("courbe[%d] = %.3f dB, attendu −12", i, g)
			break
		}
	}
}

// La courbe est celle de l'égaliseur du profil en vigueur, à l'intensité en vigueur ; rien pour un ampli, l'original ou
// une chaîne sans profil.
func TestTunerMeterCurve(t *testing.T) {
	tuner, _ := meteredTuner(t, Pipeline{Profile: "rock"}, pcm(200, 4410, 2))
	freqs := tuner.Schema().MeterFreqs
	f, _ := tuner.Meters()
	ps, _ := tuner.cfg.Profile("rock")
	want := ps.EQResponse(44100, 1, freqs)
	if len(f.Curve) != len(want) {
		t.Fatalf("courbe : %d points, attendu %d", len(f.Curve), len(want))
	}
	maxG := math.Inf(-1)
	for i := range want {
		if math.Abs(f.Curve[i]-want[i]) > 1e-9 {
			t.Fatalf("courbe[%d] = %g, attendu %g", i, f.Curve[i], want[i])
		}
		maxG = math.Max(maxG, f.Curve[i])
	}
	if maxG > 0.05 || maxG < -0.5 { // le gain automatique ramène le pic à 0 dB
		t.Errorf("pic de la courbe du rock : %.2f dB", maxG)
	}
	// l'intensité change la courbe
	st := tuner.State()
	st.Intensity = 0.5
	if err := tuner.Apply(st); err != nil {
		t.Fatal(err)
	}
	half, _ := tuner.Meters()
	if want := ps.EQResponse(44100, 0.5, freqs); math.Abs(half.Curve[10]-want[10]) > 1e-9 || half.Curve[10] == f.Curve[10] {
		t.Errorf("intensité 0,5 : %g, attendu %g (et différent de %g)", half.Curve[10], want[10], f.Curve[10])
	}
	// l'original (A/B) et l'ampli : pas de courbe
	st = tuner.State()
	st.Bypass = true
	tuner.Apply(st)
	if f, _ = tuner.Meters(); len(f.Curve) != 0 {
		t.Errorf("original : %v", f.Curve)
	}
	_, pa, _ := tuner.Preset(panel.KindAmp, "clean")
	st = tuner.State()
	st.Bypass, st.Kind, st.Name, st.Profile, st.Amp = false, panel.KindAmp, "clean", nil, pa
	if err := tuner.Apply(st); err != nil {
		t.Fatal(err)
	}
	if f, _ = tuner.Meters(); len(f.Curve) != 0 {
		t.Errorf("ampli : %v", f.Curve)
	}
}
