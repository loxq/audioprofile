package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func runAmp(t *testing.T, in []byte, ch int, s TubeAmpSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(TubeAmp(bytes.NewReader(in), 44100, ch, s))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
	return out
}

// gainDB mesure le gain (dB) d'un ampli à une fréquence, sur un signal faible.
func ampGainDB(t *testing.T, s TubeAmpSettings, freq float64) float64 {
	t.Helper()
	in := tone(freq, 300, 44100, 44100)
	out := runAmp(t, in, 1, s)
	return 20 * math.Log10(rms(out)/rms(in))
}

func TestTubeAmpIdentity(t *testing.T) {
	in := tone(1000, 8000, 44100, 3000)
	for name, s := range map[string]TubeAmpSettings{
		"réglages nuls": {},
		"stages seul":   {Stages: 3},
		"drive NaN":     {DriveDB: math.NaN()},
		"biais seul":    {Bias: 0.3}, // sans drive, le biais n'a pas d'effet
	} {
		if !bytes.Equal(in, runAmp(t, in, 1, s)) {
			t.Errorf("%s : signal intact attendu", name)
		}
	}
	out, _ := io.ReadAll(TubeAmp(bytes.NewReader(in), 0, 1, TubeAmpSettings{DriveDB: 12, Cabinet: true}))
	if !bytes.Equal(in, out) {
		t.Error("sampleRate <= 0 : signal intact attendu")
	}
}

// Quel que soit le nombre d'étages, la phase d'origine est rétablie : la sortie
// reste en phase avec l'entrée.
func TestTubeAmpPreservesPolarity(t *testing.T) {
	in := tone(200, ampOf(-30), 44100, 44100)
	for stages := 1; stages <= maxStages; stages++ {
		out := runAmp(t, in, 1, TubeAmpSettings{DriveDB: 8, Stages: stages})
		var corr float64
		for i := 22050; i < 44100; i++ {
			x := float64(int16(uint16(in[2*i]) | uint16(in[2*i+1])<<8))
			y := float64(int16(uint16(out[2*i]) | uint16(out[2*i+1])<<8))
			corr += x * y
		}
		if corr <= 0 {
			t.Errorf("%d étage(s) : sortie en opposition de phase", stages)
		}
	}
}

func TestTubeAmpStagesChangeTheSound(t *testing.T) {
	in := tone(200, ampOf(-12), 44100, 44100)
	var prev []byte
	for stages := 1; stages <= 3; stages++ {
		out := runAmp(t, in, 1, TubeAmpSettings{DriveDB: 12, Stages: stages})
		h3 := goertzel(out, 600, 44100) / goertzel(out, 200, 44100)
		t.Logf("%d étage(s) : 3e harmonique %.1f %%", stages, 100*h3)
		if h3 < 0.01 {
			t.Errorf("%d étage(s) : saturation attendue (3e harmonique %.4f)", stages, h3)
		}
		if prev != nil && bytes.Equal(prev, out) {
			t.Errorf("%d étages : même son qu'avec %d", stages, stages-1)
		}
		prev = out
	}
}

// L'asymétrie fait apparaître des harmoniques paires mais pas de composante
// continue, même après plusieurs étages (le couplage la retire entre eux).
func TestTubeAmpEvenHarmonicsNoDC(t *testing.T) {
	in := tone(200, 20000, 44100, 44100)
	for stages := 1; stages <= maxStages; stages++ {
		out := runAmp(t, in, 1, TubeAmpSettings{DriveDB: 12, Stages: stages, Bias: 0.3})
		even := goertzel(out, 400, 44100) / goertzel(out, 200, 44100)
		var sum float64
		n := len(out) / 2
		for i := n / 2; i < n; i++ {
			sum += float64(int16(uint16(out[2*i]) | uint16(out[2*i+1])<<8))
		}
		mean := sum / float64(n-n/2)
		t.Logf("%d étage(s) : 2e harmonique %.1f %%, continue %.0f", stages, 100*even, mean)
		if even < 0.01 {
			t.Errorf("%d étage(s) : harmoniques paires attendues (%.4f)", stages, even)
		}
		if math.Abs(mean) > 0.02*20000 {
			t.Errorf("%d étage(s) : composante continue %.0f", stages, mean)
		}
	}
}

func TestTubeAmpToneStack(t *testing.T) {
	// sans saturation : le correcteur de timbre seul (gain automatique : on compare des écarts)
	s := TubeAmpSettings{BassDB: 6, TrebleDB: -6}
	g1k := ampGainDB(t, s, 1000)
	bass := ampGainDB(t, s, 40) - g1k
	treble := ampGainDB(t, s, 10000) - g1k
	t.Logf("basses %+.2f dB, aigus %+.2f dB (par rapport à 1 kHz)", bass, treble)
	if bass < 4.5 || bass > 6.5 {
		t.Errorf("basses : %+.2f dB, attendu ≈ +6", bass)
	}
	if treble > -4.5 || treble < -6.5 {
		t.Errorf("aigus : %+.2f dB, attendu ≈ -6", treble)
	}
	mid := TubeAmpSettings{MidDB: 6}
	m := ampGainDB(t, mid, 650) - ampGainDB(t, mid, 100)
	if m < 4.5 || m > 6.5 {
		t.Errorf("médiums : %+.2f dB, attendu ≈ +6", m)
	}
}

func TestTubeAmpCabinet(t *testing.T) {
	s := TubeAmpSettings{Cabinet: true}
	g1k := ampGainDB(t, s, 1000)
	hf := ampGainDB(t, s, 10000) - g1k
	lf := ampGainDB(t, s, 30) - ampGainDB(t, s, 110)
	pres := ampGainDB(t, s, 2800) - g1k
	reson := ampGainDB(t, s, 110) - g1k
	t.Logf("10 kHz %+.1f dB | 30 Hz vs 110 Hz %+.1f dB | présence %+.1f dB | résonance %+.1f dB", hf, lf, pres, reson)
	if hf > -15 {
		t.Errorf("le baffle doit couper les aigus (10 kHz : %+.1f dB)", hf)
	}
	if lf > -10 {
		t.Errorf("le baffle doit couper les très graves (%+.1f dB)", lf)
	}
	if pres < 1 {
		t.Errorf("présence attendue autour de 2,8 kHz (%+.1f dB)", pres)
	}
	if reson < 1 {
		t.Errorf("résonance attendue autour de 110 Hz (%+.1f dB)", reson)
	}
}

// Les filtres finaux ne dépassent jamais 0 dB (gain automatique).
func TestTubeAmpPostPeakAtZero(t *testing.T) {
	for name, s := range map[string]TubeAmpSettings{
		"basses +12":        {BassDB: 12},
		"aigus +10":         {TrebleDB: 10},
		"timbre complet":    {BassDB: 6, MidDB: 4, TrebleDB: 6},
		"baffle":            {Cabinet: true},
		"baffle + basses":   {Cabinet: true, BassDB: 8},
		"atténuation seule": {BassDB: -6, TrebleDB: -3},
	} {
		post := tubeAmpPost(44100, s)
		if peak := peakResponseDB(44100, post); peak > 0.01 {
			t.Errorf("%s : pic à %+.2f dB", name, peak)
		}
	}
	if tubeAmpPost(44100, TubeAmpSettings{}) != nil || tubeAmpPost(0, TubeAmpSettings{Cabinet: true}) != nil {
		t.Error("sans réglage ou fs <= 0 : aucun filtre attendu")
	}
}

func TestTubeAmpCabinetLowSampleRate(t *testing.T) {
	// à 8 kHz, le passe-bas à 5 kHz dépasse Nyquist (4 kHz) : il est ignoré, sans échec
	in := tone(500, 8000, 8000, 8000)
	out, err := io.ReadAll(TubeAmp(bytes.NewReader(in), 8000, 1, TubeAmpSettings{DriveDB: 6, Cabinet: true}))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v len=%d/%d", err, len(out), len(in))
	}
	for _, f := range cabinetFilters(8000) {
		if math.IsNaN(f.b0) || math.IsNaN(f.a1) {
			t.Fatal("coefficients invalides")
		}
	}
}

func TestTubeAmpOutputGain(t *testing.T) {
	in := tone(1000, 3000, 44100, 20000)
	out := runAmp(t, in, 1, TubeAmpSettings{OutputDB: -6})
	if got := 20 * math.Log10(rms(out)/rms(in)); math.Abs(got+6) > 0.05 {
		t.Errorf("gain de sortie seul : %+.2f dB, attendu -6", got)
	}
}

func TestTubeAmpSilenceStaysSilent(t *testing.T) {
	in := make([]byte, 4*3000)
	s := TubeAmpSettings{DriveDB: 18, Stages: 3, Bias: 0.4, BassDB: 6, MidDB: -3, TrebleDB: 4, Cabinet: true, OutputDB: 3}
	if out := runAmp(t, in, 2, s); !bytes.Equal(in, out) {
		t.Error("le silence doit rester un silence exact")
	}
}

// États par canal : un canal silencieux reste silencieux, et une lecture
// fragmentée donne le même résultat qu'une lecture en un bloc.
func TestTubeAmpPerChannelAndChunks(t *testing.T) {
	l := tone(120, 20000, 44100, 44100)
	in := make([]byte, 0, 2*len(l))
	for i := 0; i+1 < len(l); i += 2 {
		in = append(in, l[i], l[i+1], 0, 0)
	}
	s := TubeAmpSettings{DriveDB: 12, Stages: 2, Bias: 0.3, BassDB: 4, Cabinet: true}
	out := runAmp(t, in, 2, s)
	for i := 0; i+3 < len(out); i += 4 {
		if out[i+2] != 0 || out[i+3] != 0 {
			t.Fatalf("diaphonie à la trame %d", i/4)
		}
	}
	chunked, _ := io.ReadAll(TubeAmp(io.MultiReader(bytes.NewReader(in[:7]), bytes.NewReader(in[7:])), 44100, 2, s))
	if !bytes.Equal(out, chunked) {
		t.Error("la lecture fragmentée doit donner le même résultat")
	}
}

// Même poussé à fond, l'ampli n'écrête pas un signal à -3 dBFS.
func TestTubeAmpNoClipping(t *testing.T) {
	const fs = 44100
	for name, s := range map[string]TubeAmpSettings{
		"à fond":         {DriveDB: 24, Stages: 3, Bias: 0.3, BassDB: 12, MidDB: 6, TrebleDB: 12},
		"baffle":         {DriveDB: 18, Cabinet: true, BassDB: 8},
		"un étage":       {DriveDB: 24, Stages: 1, Bias: 0.5},
		"gain de sortie": {DriveDB: 12, OutputDB: 6},
	} {
		for _, f := range []float64{40, 100, 400, 1000, 3000, 8000, 15000} {
			out := runAmp(t, tone(f, ampOf(-3), fs, fs), 1, s)
			for i := 0; i+1 < len(out); i += 2 {
				if v := int16(uint16(out[i]) | uint16(out[i+1])<<8); v == 32767 || v == -32768 {
					t.Fatalf("%s @%.0f Hz : écrêtage à l'échantillon %d", name, f, i/2)
				}
			}
		}
	}
}
