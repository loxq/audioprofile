package audioprofile

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

// stereoPCM génère n trames stéréo : fl et fr donnent chaque canal (échelle int16).
func stereoPCM(n int, fl, fr func(i int) float64) []byte {
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		binary.Write(buf, binary.LittleEndian, int16(fl(i)))
		binary.Write(buf, binary.LittleEndian, int16(fr(i)))
	}
	return buf.Bytes()
}

func sineFn(freq, amp float64, fs int) func(int) float64 {
	return func(i int) float64 { return amp * math.Sin(2*math.Pi*freq*float64(i)/float64(fs)) }
}

// channel extrait un canal d'un flux stéréo.
func channel(b []byte, ch int) []int16 {
	out := make([]int16, len(b)/4)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[4*i+2*ch:]))
	}
	return out
}

// midSide renvoie l'amplitude (Goertzel, 2e moitié) du milieu et du côté à freq.
func midSide(t *testing.T, b []byte, freq float64, fs int) (mid, side float64) {
	t.Helper()
	l, r := channel(b, 0), channel(b, 1)
	m, s := make([]byte, 2*len(l)), make([]byte, 2*len(l))
	for i := range l {
		binary.LittleEndian.PutUint16(m[2*i:], uint16(int16((int(l[i])+int(r[i]))/2)))
		binary.LittleEndian.PutUint16(s[2*i:], uint16(int16((int(l[i])-int(r[i]))/2)))
	}
	return goertzel(m, freq, fs), goertzel(s, freq, fs)
}

func widen(t *testing.T, in []byte, ch int, width float64) []byte {
	t.Helper()
	out, err := io.ReadAll(StereoWidth(bytes.NewReader(in), 44100, ch, width))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Un son à moitié à gauche (milieu et côté égaux) : l'élargissement double le
// côté et laisse le milieu intact.
func TestStereoWidthScalesSideNotMid(t *testing.T) {
	const fs = 44100
	in := stereoPCM(fs, sineFn(2000, 8000, fs), sineFn(2000, 4000, fs)) // M = 6000, S = 2000
	m0, s0 := midSide(t, in, 2000, fs)
	for _, width := range []float64{0.5, 1.5, 2} {
		out := widen(t, in, 2, width)
		if len(out) != len(in) {
			t.Fatalf("longueur %d, attendu %d", len(out), len(in))
		}
		m, s := midSide(t, out, 2000, fs)
		if math.Abs(m/m0-1) > 0.01 {
			t.Errorf("largeur %g : le milieu a changé (×%.3f)", width, m/m0)
		}
		if math.Abs(s/s0/width-1) > 0.03 {
			t.Errorf("largeur %g : côté ×%.3f, attendu ×%g", width, s/s0, width)
		}
	}
}

// Les basses restent au centre : le côté à 60 Hz bouge à peine (< 1,5 dB),
// alors que le même côté à 2 kHz est doublé.
func TestStereoWidthKeepsBassCentred(t *testing.T) {
	const fs = 44100
	in := stereoPCM(fs, sineFn(60, 6000, fs), func(i int) float64 { return -sineFn(60, 6000, fs)(i) }) // côté pur
	_, s0 := midSide(t, in, 60, fs)
	_, s := midSide(t, widen(t, in, 2, 2), 60, fs)
	if d := math.Abs(20 * math.Log10(s/s0)); d > 1.5 {
		t.Errorf("côté à 60 Hz modifié de %.2f dB, attendu < 1,5 dB", d)
	}
}

// Compatibilité mono : G + D est conservé, à l'arrondi près (1 LSB), quelle que
// soit la largeur, sur un signal faux-stéréo non trivial.
func TestStereoWidthKeepsMonoSum(t *testing.T) {
	const fs = 44100
	fl := func(i int) float64 { return sineFn(220, 5000, fs)(i) + sineFn(3100, 3000, fs)(i) }
	fr := func(i int) float64 { return sineFn(220, 3000, fs)(i) + sineFn(5300, 4000, fs)(i) }
	in := stereoPCM(fs, fl, fr)
	li, ri := channel(in, 0), channel(in, 1)
	for _, width := range []float64{0, 0.5, 1.7, 3} {
		out := widen(t, in, 2, width)
		lo, ro := channel(out, 0), channel(out, 1)
		worst := 0
		for i := range lo {
			d := (int(lo[i]) + int(ro[i])) - (int(li[i]) + int(ri[i]))
			if d < 0 {
				d = -d
			}
			if d > worst {
				worst = d
			}
		}
		if worst > 1 {
			t.Errorf("largeur %g : G+D modifié de %d LSB", width, worst)
		}
	}
}

// Largeur 0 : au-dessus de 200 Hz, plus de côté.
func TestStereoWidthZeroIsMonoAboveCrossover(t *testing.T) {
	const fs = 44100
	in := stereoPCM(fs, sineFn(2000, 8000, fs), sineFn(2000, -8000, fs)) // côté pur
	_, s := midSide(t, widen(t, in, 2, 0), 2000, fs)
	if s > 20 { // < −64 dB par rapport à 8000
		t.Errorf("côté résiduel %.1f à 2 kHz", s)
	}
}

func TestStereoWidthIdentity(t *testing.T) {
	const fs = 44100
	stereo := stereoPCM(4000, sineFn(300, 8000, fs), sineFn(700, 5000, fs))
	for name, tc := range map[string]struct {
		in    []byte
		ch    int
		width float64
	}{
		"largeur 1":    {stereo, 2, 1},
		"largeur NaN":  {stereo, 2, math.NaN()},
		"mono":         {tone(300, 8000, fs, 4000), 1, 2},
		"trois canaux": {append(append([]byte{}, stereo...), stereo[:len(stereo)/2]...), 3, 2},
	} {
		in := tc.in
		if tc.ch == 3 {
			in = in[:len(in)-len(in)%6]
		}
		out, err := io.ReadAll(StereoWidth(bytes.NewReader(in), fs, tc.ch, tc.width))
		if err != nil || !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact (err=%v)", name, err)
		}
	}
	out, _ := io.ReadAll(StereoWidth(bytes.NewReader(stereo), 0, 2, 2)) // fréquence nulle
	if !bytes.Equal(stereo, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

func TestStereoWidthClampsToMax(t *testing.T) {
	const fs = 44100
	in := stereoPCM(fs/2, sineFn(2000, 3000, fs), sineFn(2000, 1000, fs))
	if !bytes.Equal(widen(t, in, 2, 50), widen(t, in, 2, MaxWidth)) {
		t.Error("une largeur au-dessus de MaxWidth doit être ramenée à MaxWidth")
	}
}

// Un signal fort élargi ne dépasse jamais la pleine échelle et arrondit les pics
// sans les écrêter net.
func TestStereoWidthNoHardClip(t *testing.T) {
	const fs = 44100
	// gauche seule, très forte : le côté élargi ajoute jusqu'à +100 % au pic
	in := stereoPCM(fs, sineFn(1000, 30000, fs), func(int) float64 { return 0 })
	out := widen(t, in, 2, 3)
	for _, v := range channel(out, 0) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
}

// Lecture octet par octet : l'état de l'élargisseur est conservé d'un bloc à l'autre.
func TestStereoWidthStreaming(t *testing.T) {
	const fs = 44100
	in := stereoPCM(5000, sineFn(900, 7000, fs), sineFn(1300, 4000, fs))
	want := widen(t, in, 2, 2)
	got, err := io.ReadAll(iotest.OneByteReader(StereoWidth(bytes.NewReader(in), fs, 2, 2)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
