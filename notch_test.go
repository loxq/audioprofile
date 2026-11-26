package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
)

func notchChain(fs, f0, q float64) []coeffs {
	return designBands(fs, []Band{{Type: Notch, FreqHz: f0, Q: q}})
}

// Zéro exact au centre, passage à peu près intact loin du centre, et largeur à −3 dB égale à f0 / Q.
func TestNotchResponse(t *testing.T) {
	for _, fs := range []float64{44100, 48000, 96000} {
		for _, f0 := range []float64{50, 1000, 8000} {
			for _, q := range []float64{2, 10, 30} {
				c := notchChain(fs, f0, q)
				if g := responseDB(c, fs, f0); g > -100 {
					t.Errorf("%.0f Hz, f0 %g, Q %g : %.1f dB au centre, attendu un zéro (< −100 dB)", fs, f0, q, g)
				}
				// bords de la bande à −3 dB : f0 · (√(1 + 1/4Q²) ± 1/2Q) (formule analogique, exacte à la déformation près)
				lo := f0 * (math.Sqrt(1+1/(4*q*q)) - 1/(2*q))
				hi := f0 * (math.Sqrt(1+1/(4*q*q)) + 1/(2*q))
				tol := 0.4
				if f0 > 2000 {
					tol = 1.3 // la déformation de la transformation bilinéaire élargit un peu la bande vers les aigus
				}
				for _, e := range []float64{lo, hi} {
					if g := responseDB(c, fs, e); math.Abs(g+3.01) > tol {
						t.Errorf("%.0f Hz, f0 %g, Q %g : %.2f dB à %.1f Hz (bord de bande), attendu −3", fs, f0, q, g, e)
					}
				}
				// une octave de part et d'autre : presque rien (Q = 2 est large : 0,5 dB au plus)
				for _, e := range []float64{f0 / 2, f0 * 2} {
					if e >= fs/2*0.8 {
						continue
					}
					limit := 0.5
					if q >= 10 {
						limit = 0.1
					}
					if g := responseDB(c, fs, e); math.Abs(g) > limit {
						t.Errorf("%.0f Hz, f0 %g, Q %g : %.3f dB à %.0f Hz, attendu intact (±%g)", fs, f0, q, g, e, limit)
					}
				}
			}
		}
	}
}

// Plus Q est grand, plus la bande est étroite ; Q <= 0 vaut 10 ; le gain est ignoré (même à 0).
func TestNotchQAndGain(t *testing.T) {
	const fs = 44100
	atEdge := func(q float64) float64 { return responseDB(notchChain(fs, 1000, q), fs, 1050) }
	if !(atEdge(3) < atEdge(10) && atEdge(10) < atEdge(40)) {
		t.Errorf("gain à 1 050 Hz : %.2f / %.2f / %.2f dB pour Q = 3 / 10 / 40, doit croître avec Q", atEdge(3), atEdge(10), atEdge(40))
	}
	for _, q := range []float64{0, -4} {
		if responseDB(notchChain(fs, 1000, q), fs, 1050) != responseDB(notchChain(fs, 1000, 10), fs, 1050) {
			t.Errorf("Q = %g doit valoir 10", q)
		}
	}
	for _, g := range []float64{0, 12, -12, math.NaN()} {
		got := designBands(fs, []Band{{Type: Notch, FreqHz: 1000, Q: 10, GainDB: g}})
		if len(got) != 1 || responseDB(got, fs, 1020) != responseDB(notchChain(fs, 1000, 10), fs, 1020) {
			t.Errorf("gain %v : doit être ignoré", g)
		}
	}
}

// Retirer un ronflement du secteur : le 50 Hz tombe de plus de 40 dB, la musique ne bouge pas.
func TestNotchRemovesHum(t *testing.T) {
	const fs = 44100
	n := 4 * fs
	mix := make([]float64, n)
	for i := range mix {
		mix[i] = math.Round(ampOf(-12)*math.Sin(2*math.Pi*1000*float64(i)/fs) + ampOf(-14)*math.Sin(2*math.Pi*330*float64(i)/fs) + ampOf(-30)*math.Sin(2*math.Pi*50*float64(i)/fs))
	}
	in := interleave(mix)
	out, err := io.ReadAll(NotchFilter(bytes.NewReader(in), fs, 1, 50, 10))
	if err != nil || len(out) != len(in) {
		t.Fatalf("err=%v longueur %d/%d", err, len(out), len(in))
	}
	for f, want := range map[float64]float64{50: -40, 330: 0, 1000: 0} {
		g := levelAt(out, f, fs) - levelAt(in, f, fs)
		if (want == 0 && math.Abs(g) > 0.1) || (want < 0 && g > want) {
			t.Errorf("%g Hz : %+.2f dB (attendu %s)", f, g, map[bool]string{true: "intact", false: "< −40 dB"}[want == 0])
		}
	}
}

// Fréquence invalide : ignorée ; combinaison avec d'autres bandes ; lecture fragmentée identique.
func TestNotchInvalidAndCombined(t *testing.T) {
	const fs = 44100
	in := sine(1000, fs, 4000)
	for _, f := range []float64{0, -50, math.NaN(), fs / 2, 30000} {
		if out, _ := io.ReadAll(Equalizer(bytes.NewReader(in), fs, 1, []Band{{Type: Notch, FreqHz: f}})); !bytes.Equal(in, out) {
			t.Errorf("fréquence %v : le signal doit rester intact", f)
		}
	}
	if out, _ := io.ReadAll(NotchFilter(bytes.NewReader(in), 0, 1, 1000, 10)); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
	bands := []Band{{Type: HighPass, FreqHz: 30, Order: 2}, {Type: Notch, FreqHz: 50, Q: 12}, {Type: Notch, FreqHz: 100, Q: 12}, {Type: Peaking, FreqHz: 2000, GainDB: 3, Q: 1}}
	if n := len(designBands(fs, bands)); n != 4 {
		t.Errorf("%d sections, attendu 4", n)
	}
	one, _ := io.ReadAll(Equalizer(bytes.NewReader(sine(60, fs, 9000)), fs, 1, bands))
	frag, _ := io.ReadAll(&chunkReader{b: nil, n: 1})
	_ = frag
	got, _ := io.ReadAll(&chunkedEQ{bands: bands, fs: fs, in: sine(60, fs, 9000)})
	if !bytes.Equal(one, got) {
		t.Error("la lecture fragmentée doit donner le même résultat")
	}
}

// chunkedEQ lit un Equalizer par morceaux de 7 octets (taille volontairement impaire).
type chunkedEQ struct {
	bands []Band
	fs    int
	in    []byte
	r     io.Reader
}

func (c *chunkedEQ) Read(p []byte) (int, error) {
	if c.r == nil {
		c.r = Equalizer(&chunkReader{b: c.in, n: 7}, c.fs, 1, c.bands)
	}
	return c.r.Read(p)
}
