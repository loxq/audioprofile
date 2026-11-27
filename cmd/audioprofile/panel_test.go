package main

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/loxq/audioprofile/panel"
)

func TestCLIServeEndToEnd(t *testing.T) {
	const frames = 4 * 44100
	in := pcm(100, frames, 2)
	g := &gatedReader{data: in, cut: 2 * 44100 * 4, paused: make(chan struct{}), release: make(chan struct{})}
	var stdout bytes.Buffer
	var stderr syncBuf
	done := make(chan error, 1)
	go func() { done <- run([]string{"-serve", "127.0.0.1:0", "-profile", "flat"}, g, &stdout, &stderr) }()
	var url string
	waitFor(t, "adresse du panneau", func() bool {
		m := regexp.MustCompile(`http://\S+/`).FindString(stderr.String())
		url = m
		return m != ""
	})
	<-g.paused
	resp, err := http.Get(url + "api/state")
	if err != nil {
		t.Fatal(err)
	}
	var st panel.State
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.Kind != "profile" || st.Name != "flat" {
		t.Fatalf("état : %+v", st)
	}
	st.Kind, st.Name = "profile", "doux"
	st.Profile.Bands = []panel.Band{{Type: "gain", Gain: -12}}
	b, _ := json.Marshal(st)
	req, _ := http.NewRequest("PUT", url+"api/state", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PUT : %v %v", err, resp)
	}
	resp.Body.Close()
	close(g.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run ne finit pas")
	}
	out := stdout.Bytes()
	if len(out) != len(in) {
		t.Fatalf("%d octets, attendu %d", len(out), len(in))
	}
	if a := amplitude(out, 44100, 2*44100-4096); math.Abs(a-8000) > 100 {
		t.Errorf("avant : amplitude %g, attendu 8000 (profil flat)", a)
	}
	if a := amplitude(out, 3*44100, frames); math.Abs(a-2000) > 150 {
		t.Errorf("après : amplitude %g, attendu ~2000", a)
	}
	if _, err := http.Get(url + "api/state"); err == nil {
		t.Error("le serveur doit être arrêté à la fin du flux")
	}
}

func TestCLIServeFlagErrors(t *testing.T) {
	in := pcm(100, 1000, 2)
	for name, tc := range map[string][]string{
		"-watch":       {"-serve", "127.0.0.1:0", "-config", writeCfg(t, ""), "-watch"},
		"-ab":          {"-serve", "127.0.0.1:0", "-profile", "rock", "-ab"},
		"-target-lufs": {"-serve", "127.0.0.1:0", "-profile", "rock", "-target-lufs", "-14"},
	} {
		if _, _, err := runCLI(t, in, tc...); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s : %v", name, err)
		}
	}
	if _, _, err := runCLI(t, in, "-serve", "256.256.256.256:1", "-profile", "rock"); err == nil || !strings.Contains(err.Error(), "-serve") {
		t.Errorf("adresse invalide : %v", err)
	}
}

// Les valeurs de départ d'un compresseur activé depuis la page sont celles du fichier de configuration.
