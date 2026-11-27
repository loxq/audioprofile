package panel

import (
	"bufio"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// meteredFake est un Backend qui mesure : à chaque appel de Meters, il renvoie l'image suivante.
type meteredFake struct {
	fake
	mu     sync.Mutex
	frames []MeterFrame
	calls  atomic.Int64
	ok     bool
}

func (m *meteredFake) Meters() (MeterFrame, bool) {
	n := int(m.calls.Add(1)) - 1
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ok || len(m.frames) == 0 {
		return MeterFrame{}, false
	}
	return m.frames[n%len(m.frames)], true
}

func frame(peak float64, curve []float64) MeterFrame {
	side := MeterSide{PeakDB: []float64{peak, peak - 1}, RMSDB: []float64{peak - 10, peak - 11}, Clips: 3,
		MomentaryLUFS: -20, ShortTermLUFS: -21, Spectrum: []float64{-50, -40, -60}}
	return MeterFrame{In: side, Out: side, Curve: curve}
}

func withFastMeters(t *testing.T) {
	old := meterInterval
	meterInterval = 5 * time.Millisecond
	t.Cleanup(func() { meterInterval = old })
}

// readEvents lit n événements « data: » d'un flux d'événements.
func readEvents(t *testing.T, resp *http.Response, n int) []MeterFrame {
	t.Helper()
	var out []MeterFrame
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for len(out) < n && sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var f MeterFrame
			if err := json.Unmarshal([]byte(data), &f); err != nil {
				t.Fatalf("événement illisible : %v\n%s", err, data)
			}
			out = append(out, f)
		}
	}
	if len(out) < n {
		t.Fatalf("%d événements reçus, attendu %d (%v)", len(out), n, sc.Err())
	}
	return out
}

func TestMetersStream(t *testing.T) {
	withFastMeters(t)
	f := &meteredFake{ok: true, frames: []MeterFrame{frame(-6, []float64{0, 3, 6}), frame(-12, nil)}}
	srv := httptest.NewServer(Handler(f))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/meters")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	evs := readEvents(t, resp, 4)
	// les images arrivent dans l'ordre, avec tous leurs champs
	if evs[0].In.PeakDB[0] != -6 || evs[1].In.PeakDB[0] != -12 || evs[2].In.PeakDB[0] != -6 {
		t.Errorf("ordre des images : %v %v %v", evs[0].In.PeakDB, evs[1].In.PeakDB, evs[2].In.PeakDB)
	}
	e := evs[0]
	if e.In.Clips != 3 || e.In.MomentaryLUFS != -20 || e.In.ShortTermLUFS != -21 || len(e.In.Spectrum) != 3 || len(e.In.RMSDB) != 2 || len(e.Curve) != 3 || e.Curve[2] != 6 {
		t.Errorf("image : %+v", e)
	}
	if len(evs[1].Curve) != 0 {
		t.Errorf("sans courbe : %v", evs[1].Curve)
	}
	// les noms JSON sont ceux que lit la page
	raw, _ := json.Marshal(e)
	for _, key := range []string{`"in":`, `"out":`, `"curve":`, `"peakDb":`, `"rmsDb":`, `"clips":`, `"momentary":`, `"shortTerm":`, `"spectrum":`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("clé %s absente de %s", key, raw)
		}
	}
}

// Le flux se termine quand le client part, et le serveur peut s'arrêter ensuite sans attendre.
func TestMetersStreamStopsWhenTheClientLeaves(t *testing.T) {
	withFastMeters(t)
	f := &meteredFake{ok: true, frames: []MeterFrame{frame(-6, nil)}}
	srv := httptest.NewServer(Handler(f))
	resp, err := http.Get(srv.URL + "/api/meters")
	if err != nil {
		t.Fatal(err)
	}
	readEvents(t, resp, 2)
	resp.Body.Close()
	time.Sleep(50 * time.Millisecond) // le temps que le serveur voie la déconnexion
	before := f.calls.Load()
	time.Sleep(60 * time.Millisecond)
	if after := f.calls.Load(); after > before+1 {
		t.Errorf("le serveur continue de mesurer après le départ du client (%d appels de plus)", after-before)
	}
	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("le serveur ne s'arrête pas : un flux d'événements est resté ouvert")
	}
}

// Sans mesure disponible, aucun événement ; un backend qui ne mesure pas n'a pas de route.
func TestMetersAbsent(t *testing.T) {
	withFastMeters(t)
	idle := &meteredFake{ok: false}
	srv := httptest.NewServer(Handler(idle))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/meters")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if idle.calls.Load() < 3 {
		t.Errorf("le flux doit interroger le backend à chaque image (%d appels)", idle.calls.Load())
	}
	resp.Body.Close()

	plain := httptest.NewServer(Handler(&fake{}))
	defer plain.Close()
	r2, err := http.Get(plain.URL + "/api/meters")
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != 404 {
		t.Errorf("backend sans mesure : %d, attendu 404", r2.StatusCode)
	}
}

// Une valeur non finie (qui ne s'écrit pas en JSON) fait sauter l'image, pas couper le flux.
func TestMetersSkipsNonFiniteFrames(t *testing.T) {
	withFastMeters(t)
	bad := frame(-6, nil)
	bad.In.PeakDB[0] = math.Inf(-1)
	f := &meteredFake{ok: true, frames: []MeterFrame{bad, frame(-9, nil)}}
	srv := httptest.NewServer(Handler(f))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/meters")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, e := range readEvents(t, resp, 3) {
		if e.In.PeakDB[0] != -9 {
			t.Errorf("seules les images valables doivent passer : %v", e.In.PeakDB)
		}
	}
}

// La même protection d'origine que le reste du panneau.
func TestMetersRefusesForeignOrigin(t *testing.T) {
	h := Handler(&meteredFake{ok: true, frames: []MeterFrame{frame(-6, nil)}})
	rec := do(h, "GET", "/api/meters", "", map[string]string{"Origin": "http://evil.example"})
	if rec.Code != 403 {
		t.Errorf("origine étrangère : %d", rec.Code)
	}
}

func TestSchemaCarriesMeterFreqs(t *testing.T) {
	f := &meteredFake{}
	f.fake.state = State{Kind: KindNone, FX: []FX{}}
	h := Handler(&schemaWithMeters{meteredFake: f, freqs: []float64{50, 500, 5000}})
	var sc Schema
	if err := json.Unmarshal(do(h, "GET", "/api/schema", "", nil).Body.Bytes(), &sc); err != nil || len(sc.MeterFreqs) != 3 || sc.MeterFreqs[1] != 500 {
		t.Errorf("%v %v", err, sc.MeterFreqs)
	}
	// sans mesure : le champ n'existe pas (la page cache alors les vumètres)
	body := do(Handler(&fake{}), "GET", "/api/schema", "", nil).Body.String()
	if strings.Contains(body, "meterFreqs") {
		t.Error("meterFreqs ne doit pas figurer dans le schéma d'un backend qui ne mesure pas")
	}
}

type schemaWithMeters struct {
	*meteredFake
	freqs []float64
}

func (s *schemaWithMeters) Schema() Schema {
	sc := s.fake.Schema()
	sc.MeterFreqs = s.freqs
	return sc
}
