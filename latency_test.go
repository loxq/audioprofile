package audioprofile

import (
	"bytes"
	"io"
	"testing"
	"testing/iotest"
)

// fakeDelaySpec est un effet de test : un simple retard de n trames, qui annonce sa latence.
type fakeDelaySpec struct{ n int }

func (f fakeDelaySpec) stage(fs float64, channels int) frameStage {
	d := &fakeDelay{n: f.n}
	for ch := 0; ch < channels; ch++ {
		d.ring = append(d.ring, make([]float64, f.n))
	}
	return d
}

type fakeDelay struct {
	n    int
	ring [][]float64
	pos  int
}

func (d *fakeDelay) latency() int { return d.n }

func (d *fakeDelay) apply(frame []float64) {
	for ch := range frame {
		out := d.ring[ch][d.pos]
		d.ring[ch][d.pos] = frame[ch]
		frame[ch] = out
	}
	if d.pos++; d.pos == d.n {
		d.pos = 0
	}
}

func runChain(t *testing.T, in []byte, ch int, c chain) []byte {
	t.Helper()
	out, err := io.ReadAll(newChainReader(bytes.NewReader(in), 44100, ch, c))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Un effet qui retarde sa sortie de n trames et le déclare : le flux garde sa longueur et son alignement,
// à la trame près (une impulsion ressort à la trame où elle est entrée), comme pour le limiteur, y compris sur
// une entrée plus courte que la latence.
func TestEffectLatencyIsCompensated(t *testing.T) {
	for _, n := range []int{1, 50, 176, 1000} {
		for _, frames := range []int{10, 49, 176, 177, 3000} {
			v := make([]float64, frames)
			v[frames/2] = 12345
			in := interleave(v, v)
			out := runChain(t, in, 2, chain{effects: []effectSpec{fakeDelaySpec{n}}})
			if !bytes.Equal(in, out) {
				t.Fatalf("latence %d, %d trames : le flux doit ressortir identique (longueur %d/%d)", n, frames, len(out), len(in))
			}
		}
	}
}

// La latence des effets s'ajoute, et se combine avec celle du limiteur : même alignement, même longueur.
func TestEffectLatencyWithLimiterAndStacking(t *testing.T) {
	v := make([]float64, 5000)
	v[2000], v[2001] = 9000, -7000
	in := interleave(v)
	c := chain{effects: []effectSpec{fakeDelaySpec{120}, fakeDelaySpec{77}}, limit: &LimiterSettings{CeilingDB: -1, LookaheadMs: 5}}
	if out := runChain(t, in, 1, c); !bytes.Equal(in, out) {
		t.Errorf("deux effets à latence et un limiteur : le flux doit ressortir identique (longueur %d/%d)", len(out), len(in))
	}
	// un signal qui dépasse le plafond : alignement conservé (le limiteur agit, mais au même endroit)
	hot := make([]float64, 5000)
	hot[3000] = 32000
	out := samples(runChain(t, interleave(hot), 1, c))
	peakAt := 0
	for i, x := range out {
		if x > out[peakAt] {
			peakAt = i
		}
	}
	if len(out) != 5000 || peakAt != 3000 {
		t.Errorf("longueur %d, crête à la trame %d : attendu 5000 et 3000", len(out), peakAt)
	}
}

// Lecture octet par octet ou source qui rend données et EOF ensemble : même résultat.
func TestEffectLatencyStreaming(t *testing.T) {
	v := make([]float64, 4000)
	for i := range v {
		v[i] = float64((i % 97) * 100)
	}
	in := interleave(v, v)
	c := chain{effects: []effectSpec{fakeDelaySpec{200}}}
	for name, r := range map[string]io.Reader{
		"octet par octet": iotest.OneByteReader(newChainReader(bytes.NewReader(in), 44100, 2, c)),
		"données + EOF":   newChainReader(iotest.DataErrReader(bytes.NewReader(in)), 44100, 2, c),
	} {
		if got, err := io.ReadAll(r); err != nil || !bytes.Equal(got, in) {
			t.Errorf("%s : résultat différent (err=%v, %d octets)", name, err, len(got))
		}
	}
	// octets en trop en fin de flux : ignorés
	if got := runChain(t, append(append([]byte{}, in...), 1, 2, 3), 2, c); !bytes.Equal(got, in) {
		t.Error("octets en trop : la sortie doit rester celle des trames complètes")
	}
}
