package panel

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake est un Backend qui garde ce qu'on lui envoie.
type fake struct {
	state    State
	applied  []State
	applyErr error
	exportOK bool
}

func (f *fake) Schema() Schema {
	s := NewSchema(44100, 2)
	s.Profiles, s.Amps = []string{"rock"}, []string{"clean"}
	s.FX = []FXSchema{{Name: "delay", Doc: "écho", Params: []Field{NumField("time", "time", "ms", 1, 2000, 350, true, false)}}}
	return s
}
func (f *fake) State() State { return f.state }
func (f *fake) Apply(s State) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied = append(f.applied, s)
	f.state = s
	return nil
}
func (f *fake) Preset(kind, name string) (*Profile, *Amp, error) {
	switch {
	case kind == KindProfile && name == "rock":
		return &Profile{Bands: []Band{{Type: "gain", Gain: -3}}}, nil, nil
	case kind == KindAmp && name == "clean":
		return nil, &Amp{Drive: 4, Stages: 2}, nil
	}
	return nil, nil, errors.New("inconnu")
}
func (f *fake) Export() (string, error) {
	if !f.exportOK {
		return "", errors.New("export impossible")
	}
	return "profiles:\n  rock: {}\n", nil
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
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

func TestHandlerRoutes(t *testing.T) {
	f := &fake{state: State{Kind: KindProfile, Name: "rock", Intensity: 1.5, FX: []FX{}}, exportOK: true}
	h := Handler(f)

	rec := do(h, "GET", "/", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), "<title>") {
		t.Errorf("page : %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("la page ne doit pas être mise en cache : elle change avec le programme")
	}
	var sc Schema
	if err := json.Unmarshal(do(h, "GET", "/api/schema", "", nil).Body.Bytes(), &sc); err != nil || len(sc.FX) != 1 || sc.Profiles[0] != "rock" || sc.Rate != 44100 {
		t.Errorf("schéma : %v %+v", err, sc)
	}
	var st State
	if err := json.Unmarshal(do(h, "GET", "/api/state", "", nil).Body.Bytes(), &st); err != nil || st.Name != "rock" || st.Intensity != 1.5 {
		t.Errorf("état : %v %+v", err, st)
	}
	var pr struct {
		Profile *Profile `json:"profile"`
		Amp     *Amp     `json:"amp"`
	}
	json.Unmarshal(do(h, "GET", "/api/preset?kind=profile&name=rock", "", nil).Body.Bytes(), &pr)
	if pr.Profile == nil || pr.Profile.Bands[0].Gain != -3 || pr.Amp != nil {
		t.Errorf("preset profil : %+v", pr)
	}
	json.Unmarshal(do(h, "GET", "/api/preset?kind=amp&name=clean", "", nil).Body.Bytes(), &pr)
	if pr.Amp == nil || pr.Amp.Drive != 4 {
		t.Errorf("preset ampli : %+v", pr)
	}
	if rec := do(h, "GET", "/api/preset?kind=profile&name=nope", "", nil); rec.Code != 404 || !strings.Contains(rec.Body.String(), "inconnu") {
		t.Errorf("preset inconnu : %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "GET", "/api/yaml", "", nil); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") || !strings.Contains(rec.Body.String(), "rock") {
		t.Errorf("yaml : %d %q", rec.Code, rec.Body)
	}
	f.exportOK = false
	if rec := do(h, "GET", "/api/yaml", "", nil); rec.Code != 500 || !strings.Contains(rec.Body.String(), "export impossible") {
		t.Errorf("yaml en erreur : %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/nulle-part", "/api", "/index.html"} {
		if rec := do(h, "GET", path, "", nil); rec.Code != 404 {
			t.Errorf("%s : %d, attendu 404", path, rec.Code)
		}
	}
	if rec := do(h, "POST", "/api/state", `{}`, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/state : %d", rec.Code)
	}
}

func TestHandlerPutState(t *testing.T) {
	f := &fake{}
	h := Handler(f)
	body := `{"kind":"amp","name":"clean","intensity":2,"bypass":true,"amp":{"drive":9,"stages":3,"bias":0.1,"bass":0,"mid":0,"treble":0,"cabinet":true,"output":-2},"fx":[{"name":"delay","enabled":true,"params":{"time":120}}]}`
	if rec := do(h, "PUT", "/api/state", body, nil); rec.Code != 200 {
		t.Fatalf("PUT : %d %s", rec.Code, rec.Body)
	}
	if len(f.applied) != 1 {
		t.Fatal("l'état n'a pas été transmis")
	}
	got := f.applied[0]
	if got.Kind != KindAmp || got.Intensity != 2 || !got.Bypass || got.Amp.Drive != 9 || !got.Amp.Cabinet || got.FX[0].Params["time"] != 120 {
		t.Errorf("état reçu : %+v", got)
	}

	for name, tc := range map[string]struct {
		body string
		hdr  map[string]string
		code int
		want string
	}{
		"champ inconnu": {`{"kind":"none","intensity":1,"fx":[],"bogus":1}`, nil, 400, "bogus"},
		"json cassé":    {`{`, nil, 400, ""},
		"mauvais type":  {`{"kind":"none","intensity":"fort","fx":[]}`, nil, 400, "intensity"},
		"content-type":  {`{}`, map[string]string{"Content-Type": "text/plain"}, 415, "application/json"},
		"avec charset":  {`{"kind":"none","intensity":1,"fx":[]}`, map[string]string{"Content-Type": "application/json; charset=utf-8"}, 200, ""},
	} {
		rec := do(h, "PUT", "/api/state", tc.body, tc.hdr)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("%s : %d %s", name, rec.Code, rec.Body)
		}
	}
	n := len(f.applied)
	f.applyErr = errors.New("gain hors bornes")
	rec := do(h, "PUT", "/api/state", `{"kind":"none","intensity":1,"fx":[]}`, nil)
	var e map[string]string
	json.Unmarshal(rec.Body.Bytes(), &e)
	if rec.Code != 400 || e["error"] != "gain hors bornes" || len(f.applied) != n {
		t.Errorf("refus de l'hôte : %d %s", rec.Code, rec.Body)
	}
	// un corps énorme est coupé : le JSON ne se termine pas
	huge := `{"kind":"none","name":"` + strings.Repeat("x", 2<<20) + `","intensity":1,"fx":[]}`
	f.applyErr = nil
	if rec := do(h, "PUT", "/api/state", huge, nil); rec.Code != 400 {
		t.Errorf("corps trop gros : %d", rec.Code)
	}
}

func TestHandlerRefusesForeignOrigin(t *testing.T) {
	h := Handler(&fake{})
	body := `{"kind":"none","intensity":1,"fx":[]}`
	if rec := do(h, "PUT", "/api/state", body, map[string]string{"Origin": "http://evil.example"}); rec.Code != 403 {
		t.Errorf("origine étrangère : %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/state", "", map[string]string{"Origin": "null"}); rec.Code != 403 {
		t.Errorf("origine null : %d", rec.Code)
	}
	if rec := do(h, "PUT", "/api/state", body, map[string]string{"Origin": "http://example.com"}); rec.Code != 200 { // httptest : Host = example.com
		t.Errorf("même origine : %d", rec.Code)
	}
}

func TestLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost:8080", "[::1]:9", "127.0.0.1", "127.0.0.1:1234"} {
		if !loopbackHost(host) {
			t.Errorf("%s doit être accepté", host)
		}
	}
	for _, host := range []string{"evil.example:8080", "192.168.1.5:8080", "127.0.0.1.evil.example", "", "localhost.evil.example:80"} {
		if loopbackHost(host) {
			t.Errorf("%s doit être refusé", host)
		}
	}
}

// Serve : vraie écoute sur un port libre, refus d'un Host étranger (rebond DNS), arrêt propre et répétable.
func TestServe(t *testing.T) {
	f := &fake{state: State{Kind: KindNone, Intensity: 1, FX: []FX{}}}
	url, stop, err := Serve("127.0.0.1:0", f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasSuffix(url, "/") {
		t.Errorf("url %q", url)
	}
	resp, err := http.Get(url + "api/state")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("GET : %v %v", err, resp)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", url+"api/state", nil)
	req.Host = "evil.example:" + strings.Split(strings.TrimSuffix(url, "/"), ":")[2]
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("hôte étranger : %d, attendu 403", resp.StatusCode)
	}

	stop()
	stop() // sans effet
	if _, err := http.Get(url + "api/state"); err == nil {
		t.Error("le serveur doit être arrêté")
	}
	if _, _, err := Serve("256.256.256.256:1", f); err == nil {
		t.Error("une adresse invalide doit être une erreur")
	}
	// un port déjà pris
	_, stop2, err := Serve("127.0.0.1:0", f)
	if err != nil {
		t.Fatal(err)
	}
	defer stop2()
}
