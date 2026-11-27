package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/loxq/audioprofile"
)

func newTestPanel(t *testing.T, cfg *config, args pipeline, in []byte) (*panel, *audioprofile.Switcher) {
	t.Helper()
	if args.rate == 0 {
		args.rate, args.channels, args.intensity = 44100, 2, 1
	}
	process, err := args.build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sw := audioprofile.NewSwitcher(bytes.NewReader(in), args.rate, args.channels, process)
	p, err := newPanel(cfg, args, sw)
	if err != nil {
		t.Fatal(err)
	}
	return p, sw
}

func do(t *testing.T, h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------- schéma

func TestSchemaCoversEveryFXParam(t *testing.T) {
	for _, d := range fxDefs {
		for _, p := range d.params {
			b, ok := fxBounds[d.name][p.key]
			if !ok {
				t.Errorf("%s.%s : bornes absentes de fxBounds", d.name, p.key)
				continue
			}
			if !(b.lo < b.hi) || (b.log && b.lo <= 0) {
				t.Errorf("%s.%s : bornes incohérentes %+v", d.name, p.key, b)
			}
			if !p.required && (p.def < b.lo || p.def > b.hi) {
				t.Errorf("%s.%s : défaut %g hors du curseur [%g, %g]", d.name, p.key, p.def, b.lo, b.hi)
			}
		}
		if len(fxBounds[d.name]) != len(d.params) {
			t.Errorf("%s : fxBounds a des paramètres qui n'existent pas", d.name)
		}
	}
	for name := range fxBounds {
		if fxByName(name) == nil {
			t.Errorf("fxBounds : effet %q inconnu", name)
		}
	}
}

// Les extrémités de chaque curseur sont acceptées par la validation de l'effet (les autres paramètres au défaut),
// sauf les dépendances entre paramètres qu'un curseur seul ne sait pas respecter.
func TestSchemaSliderEndsAreValid(t *testing.T) {
	sc := newPanelSchema(44100, 2, nil)
	skip := map[string]bool{"chorus.depth": true, "flanger.depth": true, "chorus.delay": true, "flanger.delay": true, "lofi.lowcut": true, "lofi.highcut": true, "lofi.samplerate": true}
	for _, fx := range sc.FX {
		def := fxByName(fx.Name)
		for _, f := range fx.Params {
			if skip[fx.Name+"."+f.Key] {
				continue
			}
			for _, v := range []float64{f.Min, f.Max, f.Def} {
				vals := map[string]float64{}
				for _, p := range fx.Params {
					vals[p.Key] = p.Def
				}
				vals[f.Key] = v
				if def.check == nil {
					continue
				}
				if err := def.check(vals, 44100, 2); err != nil {
					t.Errorf("%s.%s = %g : %v", fx.Name, f.Key, v, err)
				}
			}
		}
	}
}

// Ce que la page reçoit d'un profil ou d'un ampli est accepté tel quel quand elle le renvoie.
func TestPanelAcceptsEveryPresetUnchanged(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestPanel(t, &config{}, pipeline{}, in)
	for _, n := range (&config{}).profileNames() {
		pp, _, err := p.preset("profile", n)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.apply(panelState{Kind: "profile", Name: n, Intensity: 1, Profile: pp, FX: []panelFX{}}); err != nil {
			t.Errorf("profil %s : %v", n, err)
		}
	}
	for _, n := range (&config{}).ampNames() {
		_, pa, err := p.preset("amp", n)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.apply(panelState{Kind: "amp", Name: n, Intensity: 1, Amp: pa, FX: []panelFX{}}); err != nil {
			t.Errorf("ampli %s : %v", n, err)
		}
	}
	for _, d := range fxDefs { // chaque effet, avec les valeurs de départ que la page donne
		params := map[string]float64{}
		for _, f := range newPanelSchema(44100, 2, nil).FX {
			if f.Name == d.name {
				for _, q := range f.Params {
					params[q.Key] = q.Def
				}
			}
		}
		if err := p.apply(panelState{Kind: "none", Intensity: 1, FX: []panelFX{{Name: d.name, Enabled: true, Params: params}}}); err != nil {
			t.Errorf("effet %s avec ses valeurs de départ : %v", d.name, err)
		}
	}
}

// Les réglages d'un profil passés par le format de la page ne changent pas le son.
func TestPanelProfileRoundTripKeepsSound(t *testing.T) {
	in := pcm(200, 20000, 2)
	for _, p := range audioprofile.Profiles() {
		orig := p.Settings()
		back, err := profileToPanel(orig).settings()
		if err != nil {
			t.Fatal(err)
		}
		a, _ := io.ReadAll(audioprofile.ApplyProfileSettings(bytes.NewReader(in), 44100, 2, orig, 1))
		b, _ := io.ReadAll(audioprofile.ApplyProfileSettings(bytes.NewReader(in), 44100, 2, back, 1))
		if !bytes.Equal(a, b) {
			t.Errorf("profil %s : le son change après un aller-retour par la page", p)
		}
	}
}

// ---------------------------------------------------------------- HTTP

func TestPanelServesPageSchemaAndState(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestPanel(t, &config{}, pipeline{profile: "rock", fxSpecs: []string{"delay:time=100"}, rate: 44100, channels: 2, intensity: 1.5}, in)
	h := p.handler()
	if rec := do(t, h, "GET", "/", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>") || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("page : %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var sc panelSchema
	rec := do(t, h, "GET", "/api/schema", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil || len(sc.FX) != len(fxDefs) || len(sc.Profiles) < 17 || sc.Rate != 44100 {
		t.Errorf("schéma : %v, %d effets, %d profils", err, len(sc.FX), len(sc.Profiles))
	}
	var st panelState
	rec = do(t, h, "GET", "/api/state", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Kind != "profile" || st.Name != "rock" || st.Intensity != 1.5 || st.Profile == nil || len(st.Profile.Bands) != 4 ||
		len(st.FX) != 1 || st.FX[0].Name != "delay" || st.FX[0].Params["time"] != 100 || st.FX[0].Params["feedback"] != 0.4 {
		t.Errorf("état de départ : %+v", st)
	}
	if rec := do(t, h, "GET", "/nulle-part", "", nil); rec.Code != 404 {
		t.Errorf("chemin inconnu : %d", rec.Code)
	}
}

func TestPanelPutStateChangesTheSound(t *testing.T) {
	in := pcm(100, 6*44100, 2)
	p, sw := newTestPanel(t, &config{}, pipeline{}, in)
	h := p.handler()
	head := make([]byte, 20*switchBlockBytes)
	if _, err := io.ReadFull(sw, head); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(head, in[:len(head)]) {
		t.Fatal("sans traitement, le flux doit passer tel quel")
	}
	body := `{"kind":"profile","name":"x","intensity":1,"bypass":false,"profile":{"bands":[{"type":"gain","freq":0,"gain":-12,"q":0,"order":0}],"compression":null,"saturation":null,"multiband":null,"width":0,"limiter":null},"fx":[]}`
	if rec := do(t, h, "PUT", "/api/state", body, nil); rec.Code != 200 {
		t.Fatalf("PUT : %d %s", rec.Code, rec.Body)
	}
	rest, _ := io.ReadAll(sw)
	if len(head)+len(rest) != len(in) {
		t.Fatalf("longueur %d, attendu %d", len(head)+len(rest), len(in))
	}
	if a := amplitude(rest, 44100, 5*44100-len(head)/4); math.Abs(a-2000) > 150 { // −12 dB sur 8000
		t.Errorf("amplitude %g après le réglage, attendu ~2000", a)
	}
	var st panelState
	json.Unmarshal(do(t, h, "GET", "/api/state", "", nil).Body.Bytes(), &st)
	if st.Kind != "profile" || st.Name != "x" || st.Profile.Bands[0].Gain != -12 {
		t.Errorf("l'état doit retenir le changement : %+v", st)
	}
}

const switchBlockBytes = 1024 * 4

func TestPanelRejectsBadRequests(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestPanel(t, &config{}, pipeline{profile: "rock", rate: 44100, channels: 2, intensity: 1}, in)
	h := p.handler()
	before := p.current()
	good := panelState{Kind: "profile", Name: "rock", Intensity: 1, Profile: before.Profile, FX: []panelFX{}}
	mut := func(f func(*panelState)) string {
		s := good
		pp := *good.Profile
		pp.Bands = append([]panelBand(nil), good.Profile.Bands...)
		s.Profile = &pp
		f(&s)
		b, _ := json.Marshal(s)
		return string(b)
	}
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"gain hors bornes": {mut(func(s *panelState) { s.Profile.Bands[0].Gain = 99 }), "gain"},
		"fréquence":        {mut(func(s *panelState) { s.Profile.Bands[0].Freq = 30000 }), "freq"},
		"type de bande":    {mut(func(s *panelState) { s.Profile.Bands[0].Type = "shelf" }), "shelf"},
		"intensité":        {mut(func(s *panelState) { s.Intensity = 9 }), "intensity"},
		"kind":             {mut(func(s *panelState) { s.Kind = "radio" }), "radio"},
		"sans nom":         {mut(func(s *panelState) { s.Name = " " }), "nom"},
		"effet inconnu":    {mut(func(s *panelState) { s.FX = []panelFX{{Name: "wah", Enabled: true}} }), "wah"},
		"param inconnu": {mut(func(s *panelState) {
			s.FX = []panelFX{{Name: "delay", Enabled: true, Params: map[string]float64{"temps": 1}}}
		}), "temps"},
		"param hors bornes": {mut(func(s *panelState) {
			s.FX = []panelFX{{Name: "delay", Enabled: true, Params: map[string]float64{"feedback": 2}}}
		}), "feedback"},
		"obligatoire":   {mut(func(s *panelState) { s.FX = []panelFX{{Name: "hpf", Enabled: true}} }), "freq"},
		"champ inconnu": {`{"kind":"none","intensity":1,"fx":[],"bogus":1}`, "bogus"},
		"json cassé":    {`{`, ""},
	} {
		rec := do(t, h, "PUT", "/api/state", tc.body, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s : %d %s", name, rec.Code, rec.Body)
		}
	}
	if !reflect.DeepEqual(p.current(), before) {
		t.Error("un état refusé ne doit rien changer")
	}
	if rec := do(t, h, "PUT", "/api/state", "{}", map[string]string{"Content-Type": "text/plain"}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type : %d", rec.Code)
	}
}

func TestPanelRefusesForeignOriginAndHost(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestPanel(t, &config{}, pipeline{}, in)
	h := p.handler()
	body := `{"kind":"none","intensity":1,"fx":[]}`
	if rec := do(t, h, "PUT", "/api/state", body, map[string]string{"Origin": "http://evil.example"}); rec.Code != 403 {
		t.Errorf("origine étrangère : %d", rec.Code)
	}
	if rec := do(t, h, "PUT", "/api/state", body, map[string]string{"Origin": "http://example.com"}); rec.Code != 200 { // httptest : Host = example.com
		t.Errorf("même origine : %d", rec.Code)
	}
	p.allow = loopbackHost // rebond DNS : un nom étranger qui pointe vers 127.0.0.1
	if rec := do(t, h, "GET", "/api/state", "", nil); rec.Code != 403 {
		t.Errorf("hôte étranger : %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/state", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("bouclage : %d", rec.Code)
	}
	for _, host := range []string{"localhost:8080", "[::1]:9", "127.0.0.1"} {
		if !loopbackHost(host) {
			t.Errorf("%s doit être accepté", host)
		}
	}
	for _, host := range []string{"evil.example:8080", "192.168.1.5:8080", ""} {
		if loopbackHost(host) {
			t.Errorf("%s doit être refusé", host)
		}
	}
}

func TestPanelPresetAndYAML(t *testing.T) {
	in := pcm(100, 5000, 2)
	cfg := parseCfg(t, "profiles:\n  rock:\n    compression: {ratio: 6}\n")
	p, _ := newTestPanel(t, cfg, pipeline{profile: "rock", rate: 44100, channels: 2, intensity: 1}, in)
	h := p.handler()
	var pr struct {
		Profile *panelProfile `json:"profile"`
		Amp     *panelAmp     `json:"amp"`
	}
	json.Unmarshal(do(t, h, "GET", "/api/preset?kind=profile&name=rock", "", nil).Body.Bytes(), &pr)
	if pr.Profile == nil || pr.Profile.Compression.Ratio != 6 {
		t.Errorf("le preset doit refléter le fichier de configuration : %+v", pr.Profile)
	}
	json.Unmarshal(do(t, h, "GET", "/api/preset?kind=amp&name=lead", "", nil).Body.Bytes(), &pr)
	if pr.Amp == nil || pr.Amp.Stages != 3 {
		t.Errorf("ampli : %+v", pr.Amp)
	}
	if rec := do(t, h, "GET", "/api/preset?kind=profile&name=nope", "", nil); rec.Code != 404 {
		t.Errorf("preset inconnu : %d", rec.Code)
	}

	// exporter : le YAML se relit en les mêmes réglages, et la ligne de commande est donnée
	st := p.current()
	st.Profile.Bands[0].Gain = 4.5
	st.FX = []panelFX{{Name: "lpf", Enabled: true, Params: map[string]float64{"freq": 9000, "order": 2}}}
	if err := p.apply(st); err != nil {
		t.Fatal(err)
	}
	out := do(t, h, "GET", "/api/yaml", "", nil).Body.String()
	c := parseCfg(t, out)
	got, _ := c.profile("rock")
	want, _ := st.Profile.settings()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("le YAML exporté ne redonne pas les réglages :\n%s", out)
	}
	if !strings.Contains(out, "-profile rock") || !strings.Contains(out, "-fx lpf:freq=9000,order=2") {
		t.Errorf("ligne de commande absente :\n%s", out)
	}
}

// ---------------------------------------------------------------- ligne de commande

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
	var st panelState
	json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if st.Kind != "profile" || st.Name != "flat" {
		t.Fatalf("état : %+v", st)
	}
	st.Kind, st.Name = "profile", "doux"
	st.Profile.Bands = []panelBand{{Type: "gain", Gain: -12}}
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
