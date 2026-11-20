package bassboost

import (
	"bytes"
	"io"
	"math"
	"testing"
)

// goertzel renvoie l'amplitude de la composante à freq dans la 2e moitié du signal.
func goertzel(b []byte, freq float64, fs int) float64 {
	n := len(b) / 2
	var re, im float64
	start := n / 2
	for i := start; i < n; i++ {
		v := float64(int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8))
		a := 2 * math.Pi * freq * float64(i) / float64(fs)
		re += v * math.Cos(a)
		im += v * math.Sin(a)
	}
	return 2 * math.Hypot(re, im) / float64(n-start)
}

func TestSaturatorShape(t *testing.T) {
	s := newSaturator(6, 0)
	g := math.Pow(10, 6.0/20)
	if got := s.apply(32767) / 32767; math.Abs(got-1) > 0.001 {
		t.Errorf("pleine échelle : %.4f, attendu 1", got)
	}
	// signaux faibles : gain ≈ g/tanh(g)
	want := g / math.Tanh(g)
	if got := s.apply(10) / 10; math.Abs(got-want) > 0.01*want {
		t.Errorf("petit signal : gain %.3f, attendu %.3f", got, want)
	}
	prev := math.Inf(-1)
	for y := -32768.0; y <= 32767; y += 256 { // monotone et impaire
		v := s.apply(y)
		if v < prev {
			t.Fatalf("non monotone en %v", y)
		}
		if math.Abs(s.apply(-y)+v) > 1e-6 {
			t.Fatalf("non impaire en %v", y)
		}
		prev = v
	}
	if newSaturator(0, 0) != nil || newSaturator(math.NaN(), 0) != nil {
		t.Error("étage sans effet : nil attendu")
	}
	// gain de sortie seul
	if got := newSaturator(0, -6).apply(10000) / 10000; math.Abs(got-math.Pow(10, -6.0/20)) > 1e-9 {
		t.Errorf("gain de sortie seul : %.4f", got)
	}
}

func TestSaturationAddsOddHarmonics(t *testing.T) {
	const fs = 44100
	in := sine(200, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // amplitude ≈ 20000
		v := int16(int(int16(uint16(in[i])|uint16(in[i+1])<<8)) * 5 / 2)
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	clean, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, nil))
	sat, _ := io.ReadAll(newEqualizerReader(bytes.NewReader(in), fs, 1, nil, newSaturator(9, 0)))
	if !bytes.Equal(in, clean) {
		t.Fatal("sans saturation : signal intact attendu")
	}
	h3 := goertzel(sat, 600, fs) / goertzel(sat, 200, fs)
	even := goertzel(sat, 400, fs) / goertzel(sat, 200, fs)
	t.Logf("3e harmonique : %.1f %%, 2e : %.2f %%", 100*h3, 100*even)
	if h3 < 0.03 {
		t.Errorf("3e harmonique trop faible : %.3f", h3)
	}
	if even > 0.005 {
		t.Errorf("2e harmonique inattendue : %.3f (saturation symétrique)", even)
	}
}

func TestRockSaturationScalesWithIntensity(t *testing.T) {
	const fs = 44100
	// Dans les basses, là où Rock est au maximum de son égalisation, le niveau
	// qui entre dans la saturation suit le drive : plus d'intensité, plus de grain.
	// (À 200 Hz, le gain automatique compense exactement le drive : pas de différence.)
	in := sine(50, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // amplitude ≈ 20000
		v := int16(int(int16(uint16(in[i])|uint16(in[i+1])<<8)) * 5 / 2)
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	thd := func(p Profile, intensity float64) float64 {
		out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, p, intensity))
		return goertzel(out, 150, fs) / goertzel(out, 50, fs)
	}
	lo, hi := thd(Rock, 0.4), thd(Rock, 1)
	t.Logf("Rock 3e harmonique @50 Hz : %.2f %% @0.4, %.2f %% @1", 100*lo, 100*hi)
	if !(hi > lo*1.3 && lo > 0) {
		t.Errorf("la saturation doit croître avec l'intensité (%.4f → %.4f)", lo, hi)
	}
	// les autres profils n'ajoutent pas de saturation
	if h := thd(Pop, 1); h > 0.005 {
		t.Errorf("Pop ne doit pas saturer (3e harmonique %.4f)", h)
	}
}

// À niveau d'écoute normal (-18 dBFS, 1 kHz), Rock doit rendre à peu près le
// niveau d'entrée : la saturation compense la baisse du gain automatique.
func TestRockLevelNearUnity(t *testing.T) {
	const fs = 44100
	in := sine(1000, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // 8000 → ≈ -12 dBFS ; /2 → ≈ -18 dBFS
		v := int16(uint16(in[i])|uint16(in[i+1])<<8) / 2
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, Rock, 1))
	got := 20 * math.Log10(rms(out)/rms(in))
	t.Logf("Rock @1 kHz, -18 dBFS : %+.2f dB", got)
	if math.Abs(got) > 1.5 {
		t.Errorf("niveau %+.2f dB, attendu ≈ 0 ± 1.5", got)
	}
}

func TestSaturatedOutputNeverClips(t *testing.T) {
	const fs = 44100
	in := sine(100, fs, fs)
	for i := 0; i+1 < len(in); i += 2 { // amplitude ≈ 32000
		v := int16(uint16(in[i])|uint16(in[i+1])<<8) * 4
		in[i], in[i+1] = byte(uint16(v)), byte(uint16(v)>>8)
	}
	out, _ := io.ReadAll(ApplyProfile(bytes.NewReader(in), fs, 1, Rock, 1))
	for i := 0; i+1 < len(out); i += 2 {
		if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v == 32767 || v == -32768 {
			t.Fatalf("écrêtage à l'échantillon %d", i/2)
		}
	}
}
