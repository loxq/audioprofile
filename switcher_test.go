package audioprofile

import (
	"bytes"
	"io"
	"math"
	"math/rand"
	"sync"
	"testing"
	"testing/iotest"
	"time"
)

const swRate = 44100

// swSignal renvoie n trames stéréo d'un mélange d'harmoniques et de bruit (déterministe).
func swSignal(n int) []byte {
	rng := rand.New(rand.NewSource(1))
	b := make([]byte, 0, n*4)
	for i := 0; i < n; i++ {
		t := float64(i) / swRate
		base := 3000*math.Sin(2*math.Pi*110*t) + 2000*math.Sin(2*math.Pi*880*t) + 1500*math.Sin(2*math.Pi*4400*t)
		for ch := 0; ch < 2; ch++ {
			v := int16(base*(1-0.2*float64(ch)) + 1500*(rng.Float64()-0.5))
			b = append(b, byte(uint16(v)), byte(uint16(v)>>8))
		}
	}
	return b
}

func profileBuild(p Profile, intensity float64) func(io.Reader) io.Reader {
	return func(r io.Reader) io.Reader { return ApplyProfile(r, swRate, 2, p, intensity) }
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sample(b []byte, i int) float64 { return float64(int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)) }

func maxDiff(a, b []byte, from, to int) float64 {
	m := 0.0
	for i := from; i < to; i++ {
		m = math.Max(m, math.Abs(sample(a, i)-sample(b, i)))
	}
	return m
}

// readSwap lit atBytes octets, demande build, puis lit le reste.
func readSwap(t *testing.T, sw *Switcher, atBytes int, build func(io.Reader) io.Reader) []byte {
	t.Helper()
	head := make([]byte, atBytes)
	if _, err := io.ReadFull(sw, head); err != nil {
		t.Fatal(err)
	}
	sw.Swap(build)
	return append(head, readAll(t, sw)...)
}

// Sans Swap, un Switcher est exactement la chaîne, même avec une source qui rend des lectures minuscules.
func TestSwitcherWithoutSwapIsTheChain(t *testing.T) {
	in := swSignal(20000)
	for _, p := range []Profile{Rock, RadioHits} {
		want := readAll(t, profileBuild(p, 1)(bytes.NewReader(in)))
		for name, src := range map[string]io.Reader{
			"entière": bytes.NewReader(in),
			"1 octet": iotest.OneByteReader(bytes.NewReader(in)),
			"moitié":  iotest.HalfReader(bytes.NewReader(in)),
		} {
			if got := readAll(t, NewSwitcher(src, swRate, 2, profileBuild(p, 1))); !bytes.Equal(got, want) {
				t.Errorf("%s, %s : le flux diffère de la chaîne", p, name)
			}
		}
	}
}

// Passer à une chaîne identique ne s'entend pas : la nouvelle, mise en régime sur l'historique, redonne
// le même signal (même un profil radio avec AGC lent et limiteur à anticipation).
func TestSwitcherSwapToSameChainIsTransparent(t *testing.T) {
	in := swSignal(6 * swRate)
	for _, p := range []Profile{Rock, RadioHits, Tube} {
		want := readAll(t, profileBuild(p, 1)(bytes.NewReader(in)))
		got := readSwap(t, NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(p, 1)), 4*swRate*4, profileBuild(p, 1))
		if len(got) != len(want) {
			t.Fatalf("%s : %d octets, attendu %d", p, len(got), len(want))
		}
		if d := maxDiff(got, want, 0, len(got)/2); d > 400 { // 1,2 % de la pleine échelle, pour des crêtes de ~10 000
			t.Errorf("%s : écart maximal %g après un changement vers la même chaîne", p, d)
		}
		if !bytes.Equal(got[:4*swRate*4], want[:4*swRate*4]) {
			t.Errorf("%s : avant le changement, le flux doit être celui de l'ancienne chaîne", p)
		}
	}
}

// Vers une autre chaîne : l'ancienne jusqu'au changement, un fondu, puis la nouvelle ; même longueur
// que la source, sans trou ni saut.
func TestSwitcherSwapToOtherChain(t *testing.T) {
	in := swSignal(6 * swRate)
	oldB := readAll(t, profileBuild(Rock, 1)(bytes.NewReader(in)))
	newB := readAll(t, profileBuild(Electronic, 2)(bytes.NewReader(in)))
	at := 3 * swRate // en trames, multiple d'un bloc : 3*44100 = 132300 ; on se cale sur le bloc suivant
	at -= at % switchBlock
	got := readSwap(t, NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(Rock, 1)), at*4, profileBuild(Electronic, 2))
	if len(got) != len(in) {
		t.Fatalf("%d octets, attendu %d", len(got), len(in))
	}
	if !bytes.Equal(got[:at*4], oldB[:at*4]) {
		t.Error("avant le changement : ce doit être l'ancienne chaîne, à l'identique")
	}
	fade := int(switchFadeMs / 1000 * swRate)
	if d := maxDiff(got, newB, 2*(at+fade+swRate/10), len(got)/2); d > 600 {
		t.Errorf("après le fondu : écart %g avec la nouvelle chaîne", d)
	}
	// pas de saut : l'écart entre deux échantillons successifs reste de l'ordre de ceux des flux eux-mêmes
	maxStep := func(b []byte, from, to int) float64 {
		m := 0.0
		for i := from + 2; i < to; i += 2 { // un canal
			m = math.Max(m, math.Abs(sample(b, i)-sample(b, i-2)))
		}
		return m
	}
	from, to := 2*(at-swRate/2), 2*(at+swRate/2) // indices d'échantillons (deux canaux par trame)
	ref := math.Max(maxStep(oldB, from, to), maxStep(newB, from, to))
	if s := maxStep(got, from, to); s > 1.5*ref {
		t.Errorf("saut au changement : pas maximal %g, contre %g pour les flux seuls", s, ref)
	}
}

// Les longueurs restent celles de la source, quel que soit le moment du changement : au tout début (historique
// plus court que la mise en régime), au milieu d'un fondu, et juste avant la fin.
func TestSwitcherKeepsLength(t *testing.T) {
	in := swSignal(3 * swRate)
	for name, tc := range map[string]struct{ at []int }{
		"avant la première lecture": {[]int{0}},
		"dès le premier bloc":       {[]int{switchBlock * 4}},
		"pendant un fondu":          {[]int{switchBlock * 8, switchBlock*8 + switchBlock*4}},
		"près de la fin":            {[]int{len(in) - switchBlock*4*2}},
		"deux changements":          {[]int{swRate * 4, swRate * 4 * 2}},
	} {
		sw := NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(RadioHits, 1))
		var got []byte
		prev := 0
		for i, at := range tc.at {
			buf := make([]byte, at-prev)
			if _, err := io.ReadFull(sw, buf); err != nil {
				t.Fatalf("%s : %v", name, err)
			}
			got = append(got, buf...)
			prev = at
			sw.Swap(profileBuild([]Profile{RadioClub, RadioTalk}[i%2], 1))
		}
		got = append(got, readAll(t, sw)...)
		if len(got) != len(in) {
			t.Errorf("%s : %d octets, attendu %d", name, len(got), len(in))
		}
	}
}

// Un changement vers une chaîne sans effet laisse passer le signal tel quel une fois le fondu fini.
func TestSwitcherSwapToPassthrough(t *testing.T) {
	in := swSignal(2 * swRate)
	at := 20 * switchBlock
	got := readSwap(t, NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(Rock, 1)), at*4, func(r io.Reader) io.Reader { return r })
	fade := int(switchFadeMs / 1000 * swRate)
	if !bytes.Equal(got[(at+fade+switchBlock)*4:], in[(at+fade+switchBlock)*4:]) {
		t.Error("après le fondu, une chaîne vide doit rendre la source")
	}
}

// Une erreur de la source remonte, une fois les données déjà lues traitées.
func TestSwitcherSourceError(t *testing.T) {
	in := swSignal(5000)
	sw := NewSwitcher(io.MultiReader(bytes.NewReader(in), iotest.ErrReader(io.ErrClosedPipe)), swRate, 2, profileBuild(Rock, 1))
	got, err := io.ReadAll(sw)
	if err != io.ErrClosedPipe {
		t.Errorf("erreur %v, attendu %v", err, io.ErrClosedPipe)
	}
	if len(got) == 0 {
		t.Error("les données avant l'erreur doivent sortir")
	}
}

// Swap depuis un autre goroutine pendant la lecture (à lancer avec -race) ; la mémoire de l'historique reste bornée.
func TestSwitcherConcurrentSwap(t *testing.T) {
	in := swSignal(4 * swRate)
	sw := NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(Rock, 1))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
				sw.Swap(profileBuild([]Profile{Pop, Jazz, RadioHits}[i%3], 1))
				time.Sleep(40 * time.Millisecond)
			}
		}
	}()
	var got []byte
	buf := make([]byte, switchBlock*4)
	for { // lecture par blocs, avec une pause : le goroutine de Swap a le temps d'en glisser plusieurs
		n, err := sw.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if len(got) != len(in) {
		t.Errorf("%d octets, attendu %d", len(got), len(in))
	}
	if limit := (int(switchWarmupMs/1000*swRate) + 4*switchBlock) * 4 * 3; len(sw.hub.data) > limit {
		t.Errorf("l'historique garde %d octets, plus que ~%d", len(sw.hub.data), limit)
	}
}

// Coût d'un changement : la mise en régime rejoue deux secondes de signal.
func BenchmarkSwitcherSwap(b *testing.B) {
	in := swSignal(4 * swRate)
	for _, p := range []Profile{Rock, RadioHits} {
		b.Run(p.String(), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				sw := NewSwitcher(bytes.NewReader(in), swRate, 2, profileBuild(p, 1))
				io.CopyN(io.Discard, sw, 3*swRate*4)
				sw.Swap(profileBuild(p, 1))
				b.StartTimer()
				io.CopyN(io.Discard, sw, switchBlock*4) // le bloc qui prend en compte le changement
			}
		})
	}
}
