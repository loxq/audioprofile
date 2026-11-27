package main

import (
	"bytes"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf est un tampon utilisable par plusieurs goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }

func (s *syncBuf) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("délai dépassé : %s", what)
}

// rewrite remplace le contenu du fichier en changeant sûrement sa date de modification.
func rewrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour * time.Duration(1+rewriteCount()))
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

var (
	rewriteMu sync.Mutex
	rewrites  int
)

func rewriteCount() int { rewriteMu.Lock(); defer rewriteMu.Unlock(); rewrites++; return rewrites }

// gatedReader rend data, mais s'arrête à cut jusqu'à ce que release soit fermé ; paused se ferme à l'arrêt.
type gatedReader struct {
	data    []byte
	pos     int
	cut     int
	paused  chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if g.pos >= len(g.data) {
		return 0, io.EOF
	}
	end := len(g.data)
	if g.pos < g.cut {
		end = g.cut
	} else if g.pos == g.cut {
		g.once.Do(func() { close(g.paused) })
		<-g.release
	}
	n := copy(p, g.data[g.pos:end])
	g.pos += n
	return n, nil
}

// amplitude renvoie l'amplitude maximale du premier canal sur les trames [from, to[ d'un flux stéréo.
func amplitude(b []byte, from, to int) float64 {
	m := 0.0
	for i := from; i < to; i++ {
		v := float64(int16(uint16(b[4*i]) | uint16(b[4*i+1])<<8))
		m = math.Max(m, math.Abs(v))
	}
	return m
}

func gainCfg(db string) string {
	return "profiles:\n  reglage:\n    bands:\n      - {type: gain, gain: " + db + "}\n"
}

// Le réglage change en cours de flux : l'amplitude de la sortie suit le nouveau fichier, sans interruption.
func TestWatchEndToEnd(t *testing.T) {
	const frames = 4 * 44100
	in := pcm(100, frames, 2)
	cfg := writeCfg(t, gainCfg("-12"))
	g := &gatedReader{data: in, cut: 2 * 44100 * 4, paused: make(chan struct{}), release: make(chan struct{})}
	var stdout bytes.Buffer
	var stderr syncBuf
	done := make(chan error, 1)
	old := watchInterval
	watchInterval = 5 * time.Millisecond
	defer func() { watchInterval = old }()
	go func() {
		done <- run([]string{"-config", cfg, "-watch", "-profile", "reglage"}, g, &stdout, &stderr)
	}()

	<-g.paused
	rewrite(t, cfg, gainCfg("-3"))
	waitFor(t, "rechargement", func() bool { return strings.Contains(stderr.String(), "rechargé") })
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	out := stdout.Bytes()
	if len(out) != len(in) {
		t.Fatalf("%d octets, attendu %d", len(out), len(in))
	}
	// avant : −12 dB (× 0,25) ; après : −3 dB (× 0,708) sur une sinusoïde d'amplitude 8000
	if a := amplitude(out, 44100, 2*44100-4096); math.Abs(a-2000) > 150 {
		t.Errorf("amplitude avant le changement : %g, attendu ~2000", a)
	}
	if a := amplitude(out, 3*44100, frames); math.Abs(a-5660) > 300 {
		t.Errorf("amplitude après le changement : %g, attendu ~5660", a)
	}
}

// Un fichier invalide en cours de route n'interrompt pas le flux : les réglages précédents restent.
func TestWatchInvalidConfigKeepsPrevious(t *testing.T) {
	in := pcm(100, 3*44100, 2)
	cfg := writeCfg(t, gainCfg("-12"))
	g := &gatedReader{data: in, cut: 44100 * 4, paused: make(chan struct{}), release: make(chan struct{})}
	var stdout bytes.Buffer
	var stderr syncBuf
	done := make(chan error, 1)
	old := watchInterval
	watchInterval = 5 * time.Millisecond
	defer func() { watchInterval = old }()
	go func() { done <- run([]string{"-config", cfg, "-watch", "-profile", "reglage"}, g, &stdout, &stderr) }()
	<-g.paused
	rewrite(t, cfg, "profiles:\n  reglage:\n    bands:\n      - {type: peaking, freq: 100, gain: 99}\n") // gain hors bornes
	waitFor(t, "erreur signalée", func() bool { return strings.Contains(stderr.String(), "conservés") })
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if a := amplitude(stdout.Bytes(), 2*44100, 3*44100); math.Abs(a-2000) > 150 {
		t.Errorf("amplitude %g : les réglages précédents (×0,25) doivent rester", a)
	}
}

func TestWatchFlagErrors(t *testing.T) {
	in := pcm(100, 1000, 2)
	cfg := writeCfg(t, "")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"sans -config":   {[]string{"-watch", "-profile", "rock"}, "-config"},
		"avec -ab":       {[]string{"-config", cfg, "-watch", "-ab", "-profile", "rock"}, "-ab"},
		"avec -target":   {[]string{"-config", cfg, "-watch", "-target-lufs", "-14", "-profile", "rock"}, "-target-lufs"},
		"fichier absent": {[]string{"-config", "/nulle/part.yaml", "-watch", "-profile", "rock"}, "part.yaml"},
	} {
		if _, _, err := runCLI(t, in, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : %v, attendu une erreur sur %q", name, err, tc.want)
		}
	}
}

// Sans changement du fichier, -watch donne exactement le même flux que sans -watch.
func TestWatchWithoutChangeMatchesPlainRun(t *testing.T) {
	in := pcm(100, 20000, 2)
	cfg := writeCfg(t, "")
	plain, _, err := runCLI(t, in, "-config", cfg, "-profile", "radio-hits")
	if err != nil {
		t.Fatal(err)
	}
	watched, _, err := runCLI(t, in, "-config", cfg, "-watch", "-profile", "radio-hits")
	if err != nil || watched != plain {
		t.Errorf("-watch sans changement doit rendre le même flux (err %v)", err)
	}
}
