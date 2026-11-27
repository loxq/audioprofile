package live

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loxq/audioprofile/config"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("délai dépassé : %s", what)
}

func writeCfg(t *testing.T, yml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "reglages.yaml")
	if err := os.WriteFile(p, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
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

func TestWatchConfigReloads(t *testing.T) {
	path := writeCfg(t, "profiles:\n  x:\n    width: 1\n")
	var mu sync.Mutex
	var applied []*config.Config
	var logs []string
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		watchConfig(path, []byte("profiles:\n  x:\n    width: 1\n"), 5*time.Millisecond, stop, func(c *config.Config) error {
			mu.Lock()
			defer mu.Unlock()
			if _, ok := c.Profile("refuse"); ok {
				return fmt.Errorf("refusé")
			}
			applied = append(applied, c)
			return nil
		}, func(f string, a ...any) { mu.Lock(); logs = append(logs, fmt.Sprintf(f, a...)); mu.Unlock() })
	}()
	defer func() { close(stop); <-done }()
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(applied) }
	logged := func(sub string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, l := range logs {
			if strings.Contains(l, sub) {
				return true
			}
		}
		return false
	}

	time.Sleep(50 * time.Millisecond)
	if count() != 0 {
		t.Fatal("rien n'a changé : pas de rechargement")
	}
	rewrite(t, path, "profiles:\n  x:\n    width: 2\n")
	waitFor(t, "rechargement", func() bool { return count() == 1 })
	if s, _ := applied[0].Profile("x"); s.Width != 2 {
		t.Errorf("la configuration rechargée a width %g", s.Width)
	}
	if !logged("rechargé") {
		t.Error("le rechargement doit être signalé")
	}

	rewrite(t, path, "profiles:\n  x:\n    width: 2\n") // même contenu, autre date : rien à faire
	time.Sleep(80 * time.Millisecond)
	if count() != 1 {
		t.Error("un contenu inchangé ne doit pas être rechargé")
	}

	rewrite(t, path, "profiles: [\n") // invalide : les réglages précédents restent, une erreur est signalée
	waitFor(t, "erreur signalée", func() bool { return logged("conservés") })
	rewrite(t, path, "profiles:\n  refuse: {width: 1}\n") // refusé par apply
	waitFor(t, "refus signalé", func() bool { return logged("refusé") })
	if count() != 1 {
		t.Error("une configuration invalide ou refusée ne doit pas être appliquée")
	}

	rewrite(t, path, "profiles:\n  x:\n    width: 3\n") // la correction est prise en compte
	waitFor(t, "rechargement après correction", func() bool { return count() == 2 })

	// un fichier qui disparaît un instant (enregistrement par renommage) est attendu
	os.Remove(path)
	time.Sleep(30 * time.Millisecond)
	rewrite(t, path, "profiles:\n  x:\n    width: 4\n")
	waitFor(t, "rechargement après disparition", func() bool { return count() == 3 })
}

// syncLog recueille les messages de Watch.
type syncLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *syncLog) logf(format string, args ...any) {
	l.mu.Lock()
	l.msgs = append(l.msgs, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *syncLog) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, m := range l.msgs {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

func (l *syncLog) count() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.msgs) }

// waitForT attend, sans testing.T (appelé hors du goroutine du test), que has(sub) soit vrai.
func waitForT(has func(string) bool, sub string) {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if has(sub) {
			return
		}
	}
}
