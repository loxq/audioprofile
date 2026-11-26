package audioprofile

import (
	"bytes"
	"io"
	"math"
	"testing"
	"testing/iotest"
)

// heavy : un compresseur très marqué, dont la compensation relève fortement les passages faibles.
var heavy = CompressorSettings{ThresholdDB: -40, Ratio: 10, AttackMs: 5, ReleaseMs: 120, KneeDB: 6, MakeupDB: 12}

func parallel(t *testing.T, in []byte, ch int, s ParallelCompressionSettings) []byte {
	t.Helper()
	out, err := io.ReadAll(ParallelCompressor(bytes.NewReader(in), 44100, ch, s))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParallelNoEffectIsBitExact(t *testing.T) {
	const fs = 44100
	in := interleave(pluck(fs, -12, 200, 80), pluck(fs, -14, 300, 60))
	for name, s := range map[string]ParallelCompressionSettings{
		"mix nul":                {Compressor: heavy},
		"mix NaN":                {Compressor: heavy, Mix: math.NaN()},
		"mix négatif":            {Compressor: heavy, Mix: -1},
		"compresseur sans effet": {Compressor: CompressorSettings{ThresholdDB: -20, Ratio: 1}, Mix: 0.5},
		"seuil NaN":              {Compressor: CompressorSettings{ThresholdDB: math.NaN(), Ratio: 4}, Mix: 0.5},
	} {
		if out := parallel(t, in, 2, s); !bytes.Equal(in, out) {
			t.Errorf("%s : le signal doit rester intact", name)
		}
	}
	if out, _ := io.ReadAll(ParallelCompressor(bytes.NewReader(in), 0, 2, ParallelCompressionSettings{Compressor: heavy, Mix: 0.5})); !bytes.Equal(in, out) {
		t.Error("sampleRate 0 : le signal doit rester intact")
	}
}

// Mix = 1 donne le compresseur seul, Mix = 0,5 la moyenne du sec et du comprimé (à 1 LSB près).
func TestParallelMixBlendsDryAndCompressed(t *testing.T) {
	const fs = 44100
	c := CompressorSettings{ThresholdDB: -40, Ratio: 6, AttackMs: 5, ReleaseMs: 120, KneeDB: 6, MakeupDB: 4}
	in := interleave(pluck(fs, -30, 200, 150), pluck(fs, -34, 300, 120)) // assez faible : pas de plafond doux
	comp, _ := io.ReadAll(Compressor(bytes.NewReader(in), fs, 2, c))
	if one := parallel(t, in, 2, ParallelCompressionSettings{Compressor: c, Mix: 1}); !bytes.Equal(one, comp) {
		t.Error("Mix = 1 doit donner exactement le compresseur seul")
	}
	half := samples(parallel(t, in, 2, ParallelCompressionSettings{Compressor: c, Mix: 0.5}))
	d, w := samples(in), samples(comp)
	worst := 0.0
	for i := range half {
		worst = math.Max(worst, math.Abs(float64(half[i])-(0.5*float64(d[i])+0.5*float64(w[i]))))
	}
	if worst > 1.01 {
		t.Errorf("écart maximal %.2f LSB avec la moyenne du sec et du comprimé", worst)
	}
	if bytes.Equal(parallel(t, in, 2, ParallelCompressionSettings{Compressor: c, Mix: 0.5}), in) {
		t.Error("le mélange doit modifier le signal")
	}
}

// L'intérêt de la compression parallèle : un passage faible est relevé bien plus qu'un passage fort,
// et les transitoires survivent mieux qu'avec le compresseur seul.
func TestParallelLiftsQuietPartsAndKeepsTransients(t *testing.T) {
	const fs = 44100
	gain := func(level, mix float64) float64 {
		in := tone(1000, ampOf(level), fs, 2*fs)
		out := parallel(t, in, 1, ParallelCompressionSettings{Compressor: heavy, Mix: mix})
		return 20 * math.Log10(goertzel(out, 1000, fs)/goertzel(in, 1000, fs))
	}
	quiet, loud := gain(-55, 0.5), gain(-10, 0.5)
	t.Logf("gain d'un sinus à −55 dBFS : %+.1f dB ; à −10 dBFS : %+.1f dB (Mix 0,5)", quiet, loud)
	if quiet-loud < 3 {
		t.Errorf("le passage faible (%+.1f dB) doit être relevé d'au moins 3 dB de plus que le fort (%+.1f dB)", quiet, loud)
	}
	// un son qui part d'un coup et s'éteint : la queue (faible) est relevée bien plus que l'attaque (forte)
	fast := heavy
	fast.AttackMs, fast.ReleaseMs = 1, 30
	const onset = fs / 2
	in := pluck(fs, -14, 150, 120)
	out := floats(parallel(t, interleave(in), 1, ParallelCompressionSettings{Compressor: fast, Mix: 0.5}))
	head := windowGainDB(in, out, onset, onset+fs/200)      // 5 premières ms
	tail := windowGainDB(in, out, onset+fs/5, onset+fs*2/5) // de 200 à 400 ms
	t.Logf("son qui s'éteint, Mix 0,5 : attaque %+.1f dB, queue %+.1f dB", head, tail)
	if tail-head < 4 {
		t.Errorf("queue %+.1f dB contre attaque %+.1f dB : la queue doit être relevée d'au moins 4 dB de plus", tail, head)
	}
}

// Détection liée : le même facteur s'applique aux deux canaux.
func TestParallelLinkedStereo(t *testing.T) {
	const fs = 44100
	l := pluck(fs, -20, 200, 150)
	r := pluck(fs, -44, 330, 150)
	in := interleave(l, r)
	out := parallel(t, in, 2, ParallelCompressionSettings{Compressor: heavy, Mix: 0.5})
	lo, ro := channel(out, 0), channel(out, 1)
	var worst float64
	for i := fs / 2; i < fs; i++ {
		if math.Abs(l[i]) < 200 || math.Abs(r[i]) < 40 {
			continue
		}
		worst = math.Max(worst, math.Abs(20*math.Log10(math.Abs(float64(lo[i])/l[i])/math.Abs(float64(ro[i])/r[i]))))
	}
	if worst > 0.5 { // arrondis sur les échantillons faibles
		t.Errorf("rapports entrée/sortie différents entre canaux de %.2f dB", worst)
	}
}

func TestParallelSilenceClampAndStreaming(t *testing.T) {
	const fs = 44100
	silence := make([]byte, 4*fs)
	if o := parallel(t, silence, 2, ParallelCompressionSettings{Compressor: heavy, Mix: 0.5}); !bytes.Equal(o, silence) {
		t.Error("le silence doit rester silence")
	}
	in := interleave(pluck(fs, -30, 200, 150)[:30000], pluck(fs, -34, 300, 120)[:30000])
	if !bytes.Equal(parallel(t, in, 2, ParallelCompressionSettings{Compressor: heavy, Mix: 7}), parallel(t, in, 2, ParallelCompressionSettings{Compressor: heavy, Mix: 1})) {
		t.Error("Mix > 1 doit être ramené à 1")
	}
	s := ParallelCompressionSettings{Compressor: heavy, Mix: 0.4}
	want := parallel(t, in, 2, s)
	if len(want) != len(in) || bytes.Equal(want, in) {
		t.Fatalf("longueur %d/%d, identique=%v", len(want), len(in), bytes.Equal(want, in))
	}
	got, err := io.ReadAll(iotest.OneByteReader(ParallelCompressor(bytes.NewReader(in), fs, 2, s)))
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("la lecture fragmentée doit donner le même résultat (err=%v)", err)
	}
}
