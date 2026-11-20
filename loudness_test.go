package audioprofile

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"math/cmplx"
	"testing"
	"testing/iotest"
)

// levelStepSine : sinus de freq Hz dont le niveau passe par paliers (dBFS) de durée égale.
func steppedSine(freq float64, fs int, levels []float64, secondsPerLevel int) []float64 {
	n := fs * secondsPerLevel * len(levels)
	out := make([]float64, n)
	for i := range out {
		lv := levels[i/(fs*secondsPerLevel)]
		out[i] = math.Round(ampOf(lv) * math.Sin(2*math.Pi*freq*float64(i)/float64(fs)))
	}
	return out
}

func interleave(chans ...[]float64) []byte {
	n := len(chans[0])
	b := make([]byte, 0, n*len(chans)*2)
	for i := 0; i < n; i++ {
		for _, c := range chans {
			b = binary.LittleEndian.AppendUint16(b, uint16(int16(c[i])))
		}
	}
	return b
}

func measure(t *testing.T, pcm []byte, fs, ch int) Loudness {
	t.Helper()
	l, err := MeasureLoudness(bytes.NewReader(pcm), fs, ch)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Les coefficients à 48 kHz retombent sur ceux de la table de l'UIT-R BS.1770-4.
func TestKWeightingMatchesBS1770Table(t *testing.T) {
	shelf, hp := kWeighting(48000)
	near := func(name string, got, want float64) {
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s = %.14f, attendu %.14f", name, got, want)
		}
	}
	near("étage 1 b0", shelf.b0, 1.53512485958697)
	near("étage 1 b1", shelf.b1, -2.69169618940638)
	near("étage 1 b2", shelf.b2, 1.19839281085285)
	near("étage 1 a1", shelf.a1, -1.69065929318241)
	near("étage 1 a2", shelf.a2, 0.73248077421585)
	near("étage 2 b0", hp.b0, 1)
	near("étage 2 b1", hp.b1, -2)
	near("étage 2 b2", hp.b2, 1)
	near("étage 2 a1", hp.a1, -1.99004745483398)
	near("étage 2 a2", hp.a2, 0.99007225036621)
}

// Gain de la pondération K à 1 kHz : environ +0,69 dB, d'où le décalage de −0,691 de la
// recommandation. À toutes les fréquences d'échantillonnage.
func TestKWeightingGainAt1kHz(t *testing.T) {
	for _, fs := range []float64{44100, 48000, 96000} {
		shelf, hp := kWeighting(fs)
		z := cmplx.Rect(1, -2*math.Pi*1000/fs)
		h := complex(1, 0)
		for _, c := range []coeffs{shelf, hp} {
			h *= (complex(c.b0, 0) + complex(c.b1, 0)*z + complex(c.b2, 0)*z*z) / (1 + complex(c.a1, 0)*z + complex(c.a2, 0)*z*z)
		}
		if g := 20 * math.Log10(cmplx.Abs(h)); math.Abs(g-0.691) > 0.03 {
			t.Errorf("%.0f Hz : gain à 1 kHz %.3f dB, attendu ≈ +0,69", fs, g)
		}
	}
}

// Étalonnage : un sinus de 1 kHz à X dBFS sur les deux canaux donne X LUFS ; sur un seul canal
// (mono), 3,01 dB de moins. C'est le cas d'essai de l'EBU Tech 3341 (−23 dBFS → −23,0 LUFS).
func TestLoudnessCalibration(t *testing.T) {
	for _, fs := range []int{44100, 48000, 96000} {
		for _, x := range []float64{-23, -20, -10} {
			s := steppedSine(1000, fs, []float64{x}, 20)
			stereo := measure(t, interleave(s, s), fs, 2)
			if !stereo.Valid || math.Abs(stereo.IntegratedLUFS-x) > 0.1 {
				t.Errorf("%d Hz, stéréo %g dBFS : %.3f LUFS (valide=%v), attendu %g", fs, x, stereo.IntegratedLUFS, stereo.Valid, x)
			}
			mono := measure(t, interleave(s), fs, 1)
			if math.Abs(mono.IntegratedLUFS-(x-3.01)) > 0.1 {
				t.Errorf("%d Hz, mono %g dBFS : %.3f LUFS, attendu %.2f", fs, x, mono.IntegratedLUFS, x-3.01)
			}
			if math.Abs(stereo.PeakDBFS-x) > 0.01 {
				t.Errorf("crête %.3f dBFS, attendu %g", stereo.PeakDBFS, x)
			}
		}
	}
}

// Valeurs de référence : mesurées avec le filtre ebur128 de ffmpeg (metadata=1, lavfi.r128.I et
// lavfi.r128.LRA, dernière valeur) sur exactement ces signaux à 48 kHz. Ces tests n'appellent
// pas ffmpeg ; ils vérifient l'accord à ±0,1 LU (sonie intégrée) et ±1 LU (plage).
func TestLoudnessAgreesWithFFmpegEBUR128(t *testing.T) {
	const fs = 48000
	// S1 : stéréo, 30 s ; gauche 1 kHz par paliers de 10 s (−20, −32, −24 dBFS), droite 3 kHz à −28 dBFS
	left := steppedSine(1000, fs, []float64{-20, -32, -24}, 10)
	right := steppedSine(3000, fs, []float64{-28, -28, -28}, 10)
	s1 := measure(t, interleave(left, right), fs, 2)
	check := func(name string, l Loudness, wantI, wantLRA float64) {
		t.Helper()
		t.Logf("%s : I = %.3f LUFS, LRA = %.3f LU (ffmpeg : %.3f, %.3f)", name, l.IntegratedLUFS, l.RangeLU, wantI, wantLRA)
		if !l.Valid || math.Abs(l.IntegratedLUFS-wantI) > 0.1 {
			t.Errorf("%s : sonie intégrée %.3f LUFS, attendu %.3f ± 0,1", name, l.IntegratedLUFS, wantI)
		}
		if !l.HasRange || math.Abs(l.RangeLU-wantLRA) > 1 {
			t.Errorf("%s : plage %.3f LU, attendu %.3f ± 1", name, l.RangeLU, wantLRA)
		}
	}
	check("S1", s1, -23.929, 5.340)
	// S2 : mono, 24 s ; 1 kHz à −20 dBFS pendant 12 s puis −32 dBFS
	check("S2", measure(t, interleave(steppedSine(1000, fs, []float64{-20, -32}, 12)), fs, 1), -25.755, 12.000)
}

// Porte absolue (−70 LUFS) et porte relative (−10 LU) : le calme n'abaisse pas la sonie intégrée.
func TestLoudnessGating(t *testing.T) {
	const fs = 48000
	loud := steppedSine(1000, fs, []float64{-20}, 10)
	quiet := steppedSine(1000, fs, []float64{-50}, 10) // 30 LU plus bas : coupé par la porte relative
	silence := make([]float64, 10*fs)
	both := func(a, b []float64) []byte {
		return interleave(append(append([]float64{}, a...), b...), append(append([]float64{}, a...), b...))
	}

	alone := measure(t, both(loud, nil), fs, 2).IntegratedLUFS
	for name, pcm := range map[string][]byte{
		"avec un passage calme (−50 dBFS)": both(loud, quiet),
		"avec du silence numérique":        both(loud, silence),
		"calme au début":                   both(quiet, loud),
	} {
		if got := measure(t, pcm, fs, 2).IntegratedLUFS; math.Abs(got-alone) > 0.1 {
			t.Errorf("%s : %.3f LUFS, attendu %.3f (les portes doivent écarter le calme)", name, got, alone)
		}
	}
	// témoin : sans porte, la moyenne simple serait bien plus basse (−23 LU de moins que −20 → ≈ −23)
	if ungated := lufs((math.Pow(10, (alone-lufsOffset)/10) + math.Pow(10, (alone-30-lufsOffset)/10)) / 2); ungated > alone-2.5 {
		t.Fatalf("témoin sans porte à %.2f LUFS : le test ne distingue rien", ungated)
	}
	// un niveau juste au-dessus de la porte relative est retenu (−8 LU), un niveau en dessous
	// (−12 LU) ne l'est pas
	near := steppedSine(1000, fs, []float64{-28}, 10) // −8 dB sous −20 : conservé
	got := measure(t, both(loud, near), fs, 2).IntegratedLUFS
	wantKept := lufs((math.Pow(10, (alone-lufsOffset)/10) + math.Pow(10, (alone-8-lufsOffset)/10)) / 2)
	if math.Abs(got-wantKept) > 0.15 {
		t.Errorf("passage à −8 LU : %.3f LUFS, attendu %.3f (retenu par la porte)", got, wantKept)
	}
}

// Plage de sonie : nulle sur un signal stable, ≈ l'écart sur deux niveaux.
func TestLoudnessRange(t *testing.T) {
	const fs = 48000
	flat := measure(t, interleave(steppedSine(1000, fs, []float64{-20}, 30)), fs, 1)
	if !flat.HasRange || flat.RangeLU > 0.3 {
		t.Errorf("signal stable : plage %.2f LU (valide=%v), attendu ≈ 0", flat.RangeLU, flat.HasRange)
	}
	two := measure(t, interleave(steppedSine(1000, fs, []float64{-20, -30}, 15)), fs, 1)
	if math.Abs(two.RangeLU-10) > 1 {
		t.Errorf("deux niveaux à 10 dB d'écart : plage %.2f LU, attendu ≈ 10", two.RangeLU)
	}
}

// Silence, flux vide ou trop court : un résultat explicite « non valable », jamais −Inf ni NaN.
func TestLoudnessInvalidCases(t *testing.T) {
	const fs = 48000
	short := interleave(steppedSine(1000, fs, []float64{-20}, 1))[:2*fs/5] // 200 ms
	for name, l := range map[string]Loudness{
		"silence":         measure(t, make([]byte, 4*fs*2), fs, 2),
		"flux vide":       measure(t, nil, fs, 2),
		"200 ms":          measure(t, short, fs, 1),
		"fréquence nulle": measure(t, short, 0, 1),
		"canaux nuls":     measure(t, short, fs, 0),
	} {
		if l.Valid {
			t.Errorf("%s : la sonie intégrée ne doit pas être valable", name)
		}
		for _, v := range []float64{l.IntegratedLUFS, l.RangeLU, l.PeakDBFS} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Errorf("%s : valeur non finie (%+v)", name, l)
			}
		}
	}
	if l := measure(t, make([]byte, 4*fs), fs, 1); l.PeakDBFS != silenceLevelDB {
		t.Errorf("crête du silence : %v, attendu %v", l.PeakDBFS, silenceLevelDB)
	}
	// 3 s : pas de plage (niveaux à court terme de 3 s : un seul à peine), mais 400 ms suffisent pour la sonie
	if l := measure(t, interleave(steppedSine(1000, fs, []float64{-20}, 1))[:2*fs/2], fs, 1); !l.Valid {
		t.Error("500 ms : la sonie intégrée doit être valable")
	}
}

// Quelle que soit la taille des morceaux reçus, le résultat est identique, à l'octet près.
func TestLoudnessMeterChunking(t *testing.T) {
	const fs = 44100
	left := steppedSine(440, fs, []float64{-18, -30}, 4)
	right := steppedSine(1500, fs, []float64{-24, -20}, 4)
	pcm := interleave(left, right)
	want := measure(t, pcm, fs, 2)
	for name, r := range map[string]io.Reader{
		"octet par octet": iotest.OneByteReader(bytes.NewReader(pcm)),
		"3 octets":        &chunkReader{b: pcm, n: 3},
		"4097 octets":     &chunkReader{b: pcm, n: 4097},
	} {
		got, err := MeasureLoudness(r, fs, 2)
		if err != nil || got != want {
			t.Errorf("%s : %+v (err=%v), attendu %+v", name, got, err, want)
		}
	}
	// un octet isolé en fin de flux est ignoré
	if got := measure(t, append(append([]byte{}, pcm...), 5), fs, 2); got != want {
		t.Errorf("octet isolé : %+v, attendu %+v", got, want)
	}
}

type chunkReader struct {
	b []byte
	n int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.b) == 0 {
		return 0, io.EOF
	}
	k := c.n
	if k > len(c.b) {
		k = len(c.b)
	}
	if k > len(p) {
		k = len(p)
	}
	copy(p, c.b[:k])
	c.b = c.b[k:]
	return k, nil
}

// Plusieurs canaux : pas de panique, et les canaux ajoutent leur énergie.
func TestLoudnessChannels(t *testing.T) {
	const fs = 48000
	s := steppedSine(1000, fs, []float64{-20}, 10)
	one := measure(t, interleave(s), fs, 1).IntegratedLUFS
	for ch := 2; ch <= 4; ch++ {
		chans := make([][]float64, ch)
		for i := range chans {
			chans[i] = s
		}
		got := measure(t, interleave(chans...), fs, ch).IntegratedLUFS
		if want := one + 10*math.Log10(float64(ch)); math.Abs(got-want) > 0.05 {
			t.Errorf("%d canaux : %.3f LUFS, attendu %.3f", ch, got, want)
		}
	}
}
