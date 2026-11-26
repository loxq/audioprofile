package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

func excite(t *testing.T, in []byte, ch int, s ExciterSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(Exciter(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExciterNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, fs, [3]float64{5000, -12, 0}))
	for name, s := range map[string]ExciterSettings{
		"quantité nulle":     {Amount: 0, DriveDB: 18},
		"quantité NaN":       {Amount: math.NaN()},
		"quantité négative":  {Amount: -1},
		"séparation Nyquist": {FreqHz: 22050, Amount: 1},
	} {
		if out := excite(t, in, 1, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(Exciter(bytes.NewReader(in), 0, 1, ExciterSettings{Amount: 1})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Les harmoniques apparaissent au-dessus de la séparation : la 3e toujours, la 2e seulement avec
// Warmth ; et une note grave (200 Hz), bien en dessous, n'est pas touchée.
func TestExciterAddsHarmonics(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, 2*fs, [3]float64{5000, -14, 0}))
	rel := func(out []byte, f float64) float64 { return levelAt(out[2*fs:], f, fs) - levelAt(out[2*fs:], 5000, fs) } // dBc, 2e moitié
	plain := excite(t, in, 1, ExciterSettings{Amount: 1})
	warm := excite(t, in, 1, ExciterSettings{Amount: 1, Warmth: 0.6})
	h2p, h3p, h2w := rel(plain, 10000), rel(plain, 15000), rel(warm, 10000)
	t.Logf("5 kHz à −14 dBFS : 2e harmonique %.1f dBc (symétrique), %.1f dBc (Warmth 0,6) ; 3e harmonique %.1f dBc", h2p, h2w, h3p)
	if h3p < -45 {
		t.Errorf("3e harmonique à %.1f dBc, attendue au-dessus de −45", h3p)
	}
	if h2p > -60 {
		t.Errorf("2e harmonique à %.1f dBc sans Warmth, attendue sous −60 (saturation symétrique)", h2p)
	}
	if h2w < -50 || h2w < h2p+15 {
		t.Errorf("2e harmonique à %.1f dBc avec Warmth, attendue au-dessus de −50 et 15 dB plus haut que sans", h2w)
	}
	// l'entrée d'origine est toujours là : la fondamentale ne baisse pas
	if g := levelAt(plain[2*fs:], 5000, fs) - levelAt(in[2*fs:], 5000, fs); g < -0.5 {
		t.Errorf("la fondamentale baisse de %.2f dB", g)
	}
	// une note grave est intacte
	low := interleave(burstTones(fs, fs, [3]float64{200, -10, 0}))
	if g := levelAt(excite(t, low, 1, ExciterSettings{Amount: 1})[fs:], 200, fs) - levelAt(low[fs:], 200, fs); math.Abs(g) > 0.1 {
		t.Errorf("200 Hz : %+.2f dB, attendu inchangé", g)
	}
}

// Pas de filtre en peigne : le chemin ajouté est aligné sur le signal sec (même retard), donc aucune
// fréquence ne s'annule. À petit signal l'exciter est transparent, partout, y compris autour de la
// fréquence de séparation (sans l'alignement des retards, il creusait 13 dB à 10 kHz) ; à −20 dBFS, la
// fondamentale ne bouge que de ±1,8 dB (la saturation la comprime un peu) ; et même sur un aigu très fort
// (−14 dBFS, saturation profonde), aucune annulation destructrice.
func TestExciterHasNoCombDips(t *testing.T) {
	const fs = 44100
	freqs := []float64{100, 300, 1000, 1500, 2000, 2500, 3000, 3500, 4000, 5000, 6000, 8000, 10000, 12000, 16000}
	for _, tc := range []ExciterSettings{{Amount: 1}, {Amount: 1, Warmth: 0.5}, {Amount: 0.5}, {Amount: 1, FreqHz: 1500}, {Amount: 1, FreqHz: 8000}} {
		for _, lim := range []struct{ level, lo, hi float64 }{{-55, -0.3, 0.3}, {-20, -1.8, 1.8}, {-14, -5, 3.5}} {
			worst, best := 0.0, 0.0
			var worstF float64
			for _, f := range freqs {
				in := interleave(burstTones(fs, fs, [3]float64{f, lim.level, 0}))
				g := levelAt(excite(t, in, 1, tc)[fs:], f, fs) - levelAt(in[fs:], f, fs)
				if g < worst {
					worst, worstF = g, f
				}
				best = math.Max(best, g)
			}
			t.Logf("%+v à %g dBFS : de %.2f dB (à %g Hz) à %+.2f dB", tc, lim.level, worst, worstF, best)
			if worst < lim.lo || best > lim.hi {
				t.Errorf("%+v à %g dBFS : de %.2f dB (à %g Hz) à %+.2f dB, attendu entre %.1f et %+.1f", tc, lim.level, worst, worstF, best, lim.lo, lim.hi)
			}
		}
	}
}

// Peu de repliement de spectre : un sinus à 9 kHz dont la 3e harmonique (27 kHz) se replierait vers 17,1 kHz.
func TestExciterAliasingStaysLow(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, 2*fs, [3]float64{9000, -10, 0}))
	out := excite(t, in, 1, ExciterSettings{Amount: 1, DriveDB: 24})
	alias := levelAt(out[2*fs:], 17100, fs) - levelAt(out[2*fs:], 9000, fs)
	t.Logf("repliement à 17,1 kHz : %.1f dBc", alias)
	if alias > -30 {
		t.Errorf("repliement à %.1f dBc, attendu sous −30", alias)
	}
}

// Canaux traités séparément (un canal silencieux reste silencieux), silence exact, aucun écrêtage.
func TestExciterChannelsSilenceAndClipping(t *testing.T) {
	const fs = 44100
	loud := burstTones(fs, fs, [3]float64{6000, -0.5, 0})
	in := interleave(loud, make([]float64, fs))
	out := excite(t, in, 2, ExciterSettings{Amount: 1, DriveDB: 24, Warmth: 1})
	for i, v := range channel(out, 1) {
		if v != 0 {
			t.Fatalf("canal droit silencieux : %d à la trame %d", v, i)
		}
	}
	for _, v := range channel(out, 0) {
		if v >= 32767 || v <= -32768 {
			t.Fatalf("échantillon écrêté (%d)", v)
		}
	}
	silence := make([]byte, 4*fs)
	if o := excite(t, silence, 1, ExciterSettings{Amount: 1, Warmth: 1}); !bytes.Equal(o, silence) {
		t.Error("le silence doit rester silence")
	}
}

func TestExciterClampsAndStreams(t *testing.T) {
	const fs = 44100
	in := interleave(burstTones(fs, 15000, [3]float64{5000, -14, 0}), burstTones(fs, 15000, [3]float64{4200, -16, 0}))
	a := excite(t, in, 2, ExciterSettings{Amount: 9, DriveDB: 999, Warmth: 5})
	b := excite(t, in, 2, ExciterSettings{Amount: 1, DriveDB: maxExciterDrive, Warmth: 1})
	if !bytes.Equal(a, b) {
		t.Error("les valeurs hors bornes doivent être ramenées dedans")
	}
	s := ExciterSettings{Amount: 0.7, DriveDB: 15, Warmth: 0.3}
	want := excite(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(Exciter(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
