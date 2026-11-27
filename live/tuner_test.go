package live

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
	"github.com/loxq/audioprofile/fx"
	"github.com/loxq/audioprofile/panel"
)

// pcm renvoie n trames d'une sinusoïde d'amplitude 8000 à 44,1 kHz.
func pcm(freq float64, n, channels int) []byte {
	buf := new(bytes.Buffer)
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*freq*float64(i)/44100))
		for c := 0; c < channels; c++ {
			binary.Write(buf, binary.LittleEndian, v)
		}
	}
	return buf.Bytes()
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

func parseCfg(t *testing.T, yml string) *config.Config {
	t.Helper()
	c, err := config.Parse(strings.NewReader(yml))
	if err != nil {
		t.Fatalf("config.Parse : %v", err)
	}
	return c
}

func newTestTuner(t *testing.T, cfg *config.Config, args Pipeline, in []byte) (*Tuner, *audioprofile.Switcher) {
	t.Helper()
	if args.Rate == 0 {
		args.Rate, args.Channels, args.Intensity = 44100, 2, 1
	}
	process, err := args.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sw := audioprofile.NewSwitcher(bytes.NewReader(in), args.Rate, args.Channels, process)
	p, err := NewTuner(cfg, args, sw)
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

// Ce que la page reçoit d'un profil ou d'un ampli est accepté tel quel quand elle le renvoie.
func TestTunerAcceptsEveryPresetUnchanged(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestTuner(t, &config.Config{}, Pipeline{}, in)
	for _, n := range (&config.Config{}).ProfileNames() {
		pp, _, err := p.Preset("profile", n)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(panel.State{Kind: "profile", Name: n, Intensity: 1, Profile: pp, FX: []panel.FX{}}); err != nil {
			t.Errorf("profil %s : %v", n, err)
		}
	}
	for _, n := range (&config.Config{}).AmpNames() {
		_, pa, err := p.Preset("amp", n)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Apply(panel.State{Kind: "amp", Name: n, Intensity: 1, Amp: pa, FX: []panel.FX{}}); err != nil {
			t.Errorf("ampli %s : %v", n, err)
		}
	}
	for _, d := range fx.Defs() { // chaque effet, avec les valeurs de départ que la page donne
		params := map[string]float64{}
		for _, f := range fxSchemas() {
			if f.Name == d.Name {
				for _, q := range f.Params {
					params[q.Key] = q.Def
				}
			}
		}
		if err := p.Apply(panel.State{Kind: "none", Intensity: 1, FX: []panel.FX{{Name: d.Name, Enabled: true, Params: params}}}); err != nil {
			t.Errorf("effet %s avec ses valeurs de départ : %v", d.Name, err)
		}
	}
}

func TestTunerServesPageSchemaAndState(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestTuner(t, &config.Config{}, Pipeline{Profile: "rock", FX: []string{"delay:time=100"}, Rate: 44100, Channels: 2, Intensity: 1.5}, in)
	h := panel.Handler(p)
	if rec := do(t, h, "GET", "/", "", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>") || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Errorf("page : %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var sc panel.Schema
	rec := do(t, h, "GET", "/api/schema", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &sc); err != nil || len(sc.FX) != len(fx.Defs()) || len(sc.Profiles) < 17 || sc.Rate != 44100 || sc.Defaults.Compression == nil {
		t.Errorf("schéma : %v, %d effets, %d profils", err, len(sc.FX), len(sc.Profiles))
	}
	var st panel.State
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

func TestTunerPutStateChangesTheSound(t *testing.T) {
	in := pcm(100, 6*44100, 2)
	p, sw := newTestTuner(t, &config.Config{}, Pipeline{}, in)
	h := panel.Handler(p)
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
	var st panel.State
	json.Unmarshal(do(t, h, "GET", "/api/state", "", nil).Body.Bytes(), &st)
	if st.Kind != "profile" || st.Name != "x" || st.Profile.Bands[0].Gain != -12 {
		t.Errorf("l'état doit retenir le changement : %+v", st)
	}
}

const switchBlockBytes = 1024 * 4

func TestTunerRejectsBadRequests(t *testing.T) {
	in := pcm(100, 5000, 2)
	p, _ := newTestTuner(t, &config.Config{}, Pipeline{Profile: "rock", Rate: 44100, Channels: 2, Intensity: 1}, in)
	h := panel.Handler(p)
	before := p.State()
	good := panel.State{Kind: "profile", Name: "rock", Intensity: 1, Profile: before.Profile, FX: []panel.FX{}}
	mut := func(f func(*panel.State)) string {
		s := good
		pp := *good.Profile
		pp.Bands = append([]panel.Band(nil), good.Profile.Bands...)
		s.Profile = &pp
		f(&s)
		b, _ := json.Marshal(s)
		return string(b)
	}
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"gain hors bornes": {mut(func(s *panel.State) { s.Profile.Bands[0].Gain = 99 }), "gain"},
		"fréquence":        {mut(func(s *panel.State) { s.Profile.Bands[0].Freq = 30000 }), "freq"},
		"type de bande":    {mut(func(s *panel.State) { s.Profile.Bands[0].Type = "shelf" }), "shelf"},
		"intensité":        {mut(func(s *panel.State) { s.Intensity = 9 }), "intensity"},
		"kind":             {mut(func(s *panel.State) { s.Kind = "radio" }), "radio"},
		"sans nom":         {mut(func(s *panel.State) { s.Name = " " }), "nom"},
		"effet inconnu":    {mut(func(s *panel.State) { s.FX = []panel.FX{{Name: "wah", Enabled: true}} }), "wah"},
		"param inconnu": {mut(func(s *panel.State) {
			s.FX = []panel.FX{{Name: "delay", Enabled: true, Params: map[string]float64{"temps": 1}}}
		}), "temps"},
		"param hors bornes": {mut(func(s *panel.State) {
			s.FX = []panel.FX{{Name: "delay", Enabled: true, Params: map[string]float64{"feedback": 2}}}
		}), "feedback"},
		"obligatoire":   {mut(func(s *panel.State) { s.FX = []panel.FX{{Name: "hpf", Enabled: true}} }), "freq"},
		"champ inconnu": {`{"kind":"none","intensity":1,"fx":[],"bogus":1}`, "bogus"},
		"json cassé":    {`{`, ""},
	} {
		rec := do(t, h, "PUT", "/api/state", tc.body, nil)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s : %d %s", name, rec.Code, rec.Body)
		}
	}
	if !reflect.DeepEqual(p.State(), before) {
		t.Error("un état refusé ne doit rien changer")
	}
	if rec := do(t, h, "PUT", "/api/state", "{}", map[string]string{"Content-Type": "text/plain"}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type : %d", rec.Code)
	}
}

func TestTunerPresetAndYAML(t *testing.T) {
	in := pcm(100, 5000, 2)
	cfg := parseCfg(t, "profiles:\n  rock:\n    compression: {ratio: 6}\n")
	p, _ := newTestTuner(t, cfg, Pipeline{Profile: "rock", Rate: 44100, Channels: 2, Intensity: 1}, in)
	h := panel.Handler(p)
	var pr struct {
		Profile *panel.Profile `json:"profile"`
		Amp     *panel.Amp     `json:"amp"`
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
	st := p.State()
	st.Profile.Bands[0].Gain = 4.5
	st.FX = []panel.FX{{Name: "lpf", Enabled: true, Params: map[string]float64{"freq": 9000, "order": 2}}}
	if err := p.Apply(st); err != nil {
		t.Fatal(err)
	}
	out := do(t, h, "GET", "/api/yaml", "", nil).Body.String()
	c := parseCfg(t, out)
	got, _ := c.Profile("rock")
	want, _ := st.Profile.Settings()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("le YAML exporté ne redonne pas les réglages :\n%s", out)
	}
	if !strings.Contains(out, "-profile rock") || !strings.Contains(out, "-fx lpf:freq=9000,order=2") {
		t.Errorf("ligne de commande absente :\n%s", out)
	}
}

// ---------------------------------------------------------------- ligne de commande

// Les valeurs de départ d'un étage activé depuis la page sont celles du fichier de réglages.
func TestTunerSchemaUsesConfigStartValues(t *testing.T) {
	p, _ := newTestTuner(t, nil, Pipeline{}, pcm(100, 1000, 2))
	s := p.Schema()
	if got, want := s.Defaults.Compression.Settings(), config.StartCompressor(); got != want {
		t.Errorf("compresseur de départ de la page %+v, celui du fichier %+v", got, want)
	}
	lim := config.StartLimiter()
	if l := s.Defaults.Limiter; l.Ceiling != lim.CeilingDB || l.Lookahead != lim.LookaheadMs || l.Release != lim.ReleaseMs {
		t.Errorf("limiteur de départ de la page %+v, celui du fichier %+v", l, lim)
	}
	if len(s.FX) != len(fx.Defs()) || len(s.Profiles) < 17 || len(s.Amps) < 3 || s.Rate != 44100 || s.Channels != 2 {
		t.Errorf("schéma : %d effets, %d profils, %d amplis", len(s.FX), len(s.Profiles), len(s.Amps))
	}
}

// Un effet ajouté depuis la page avec un paramètre obligatoire part d'une valeur audible et valable.
func TestFXSchemaRequiredParamsHaveAStartValue(t *testing.T) {
	for _, d := range fxSchemas() {
		for _, f := range d.Params {
			if f.Required && (f.Def < f.Min || f.Def > f.Max || f.Def <= 0) {
				t.Errorf("%s.%s : valeur de départ %g", d.Name, f.Key, f.Def)
			}
		}
	}
}
