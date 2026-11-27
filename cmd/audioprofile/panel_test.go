package main

import (
	"bufio"
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

// Avec -serve, la page reçoit des mesures avant et après la chaîne : le niveau de l'entrée, celui de la sortie, les bandes
// du spectre, et la courbe de l'égaliseur du profil en vigueur.
func TestCLIServeMeters(t *testing.T) {
	const frames = 4 * 44100
	in := pcm(1000, frames, 2) // sinusoïde d'amplitude 8000
	g := &gatedReader{data: in, cut: 2 * 44100 * 4, paused: make(chan struct{}), release: make(chan struct{})}
	var stdout bytes.Buffer
	var stderr syncBuf
	done := make(chan error, 1)
	go func() { done <- run([]string{"-serve", "127.0.0.1:0", "-profile", "rock"}, g, &stdout, &stderr) }()
	var url string
	waitFor(t, "adresse du panneau", func() bool {
		url = regexp.MustCompile(`http://\S+/`).FindString(stderr.String())
		return url != ""
	})
	<-g.paused // 2 s de son sont passées : les mesures sont figées sur ce qu'elles ont vu

	var sc panel.Schema
	resp, err := http.Get(url + "api/schema")
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&sc)
	resp.Body.Close()
	if len(sc.MeterFreqs) != 64 {
		t.Fatalf("bandes annoncées : %d", len(sc.MeterFreqs))
	}

	resp, err = http.Get(url + "api/meters")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("type : %q", ct)
	}
	var f panel.MeterFrame
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			if err := json.Unmarshal([]byte(data), &f); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	want := 20 * math.Log10(8000.0/32768) // −12,25 dBFS
	if math.Abs(f.In.PeakDB[0]-want) > 0.2 {
		t.Errorf("entrée : crête %.2f dBFS, attendu %.2f", f.In.PeakDB[0], want)
	}
	if f.Out.PeakDB[0] == f.In.PeakDB[0] || math.IsNaN(f.Out.PeakDB[0]) || f.Out.PeakDB[0] < -60 {
		t.Errorf("sortie : crête %.2f dBFS (le rock modifie le niveau, il n'est pas muet)", f.Out.PeakDB[0])
	}
	if len(f.In.Spectrum) != 64 || len(f.Out.Spectrum) != 64 || len(f.Curve) != 64 {
		t.Errorf("bandes : %d, %d et %d points de courbe", len(f.In.Spectrum), len(f.Out.Spectrum), len(f.Curve))
	}
	// le sinus de 1 kHz domine le spectre d'entrée, dans la bonne bande
	best := 0
	for i, v := range f.In.Spectrum {
		if v > f.In.Spectrum[best] {
			best = i
		}
	}
	if fr := sc.MeterFreqs[best]; fr < 900 || fr > 1110 {
		t.Errorf("pic du spectre à %.0f Hz, attendu ~1000", fr)
	}
	if f.In.MomentaryLUFS < -40 || f.In.MomentaryLUFS > -10 {
		t.Errorf("sonie de l'entrée : %.1f LUFS", f.In.MomentaryLUFS)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(stdout.Bytes()) != len(in) {
		t.Errorf("%d octets, attendu %d : la mesure ne doit pas changer la longueur du flux", len(stdout.Bytes()), len(in))
	}
}
