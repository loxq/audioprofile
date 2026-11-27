package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
	"github.com/loxq/audioprofile/live"
	"github.com/loxq/audioprofile/panel"
)

// ---------------------------------------------------------------- outils

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

func (l *syncLog) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.msgs, "\n")
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("délai dépassé : %s", what)
}

// needFFmpeg saute le test si ffmpeg (avec l'encodeur mp3) n'est pas installé : les tests de bout en bout s'en servent.
func needFFmpeg(t *testing.T) {
	t.Helper()
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg absent")
	}
	out, _ := exec.Command(path, "-hide_banner", "-encoders").Output()
	if !bytes.Contains(out, []byte("libmp3lame")) {
		t.Skip("ffmpeg sans libmp3lame")
	}
}

// makeTrack écrit un mp3 de synthèse (sinusoïde de 440 Hz, stéréo, 44,1 kHz, 128 kb/s) et renvoie son chemin.
func makeTrack(t *testing.T, dir, name string, seconds int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi",
		"-i", fmt.Sprintf("sine=frequency=440:duration=%d:sample_rate=44100", seconds),
		"-ac", "2", "-c:a", "libmp3lame", "-b:a", "128k", p).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg : %v\n%s", err, out)
	}
	return p
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const profilesYAML = `profiles:
  quiet:
    bands:
      - {type: gain, gain: -12}
  loud:
    bands:
      - {type: gain, gain: -3}
`

// radioOptions : tout au point pour les tests (vite, sans panneau, sans attente).
func radioOptions(t *testing.T, playlist, output string, logf func(string, ...any)) options {
	return options{
		playlist: playlist, output: output, bitrate: "128k", rate: 44100, channels: 2, intensity: 1, loop: false,
		ffmpeg: "ffmpeg", iceName: "Station test", adminUser: "admin", adminPassword: "adminpw",
		retry: 50 * time.Millisecond, announceEvery: 20 * time.Millisecond, speed: 10, logf: logf,
	}
}

// decodeLevels décode le mp3 reçu par Icecast et renvoie le niveau efficace (dBFS) de chaque tranche d'une demi-seconde.
func decodeLevels(t *testing.T, mp3 []byte) []float64 {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-f", "s16le", "-ac", "1", "-ar", "44100", "pipe:1")
	cmd.Stdin = bytes.NewReader(mp3)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("décodage du flux reçu : %v", err)
	}
	const window = 44100 / 2
	var levels []float64
	for i := 0; i+window <= len(out)/2; i += window {
		var sum float64
		for j := i; j < i+window; j++ {
			v := float64(int16(binary.LittleEndian.Uint16(out[2*j:])))
			sum += v * v
		}
		levels = append(levels, 10*math.Log10(sum/window/32768/32768+1e-20))
	}
	return levels
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// run lance la radio et la nettoie à la fin du test.
func run(t *testing.T, o options) *radio {
	t.Helper()
	r, err := start(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)
	return r
}

// ---------------------------------------------------------------- bout en bout (ffmpeg)

// Le trajet complet : playlist → décodage → profil par morceau → mp3 → Icecast, avec l'annonce des titres.
func TestRadioEndToEnd(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 3)
	cfg := writeFile(t, dir, "reglages.yaml", profilesYAML)
	pl := writeFile(t, dir, "playlist.txt", "#profile: quiet\nt.mp3 | Artiste - Titre A\n#profile: loud\nt.mp3 | Titre B & co\n")
	ice := newFakeIcecast(t)
	var logs syncLog
	o := radioOptions(t, pl, ice.URL("/live"), logs.logf)
	o.config = cfg
	r := run(t, o)
	r.Wait()

	// l'encodeur s'est présenté comme une source Icecast
	srcs := ice.Sources()
	if len(srcs) != 1 {
		t.Fatalf("%d connexions source\n%s", len(srcs), logs.all())
	}
	s := srcs[0]
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("source:hackme"))
	if s.Method != "PUT" || s.Path != "/live" || s.Header["authorization"] != wantAuth || s.Header["content-type"] != "audio/mpeg" || !strings.Contains(s.Header["ice-name"], "Station test") {
		t.Errorf("requête de la source : %s %s %v", s.Method, s.Path, s.Header)
	}

	// deux morceaux de 3 s : 6 s de son, et le deuxième est plus fort de 9 dB (−12 dB puis −3 dB)
	levels := decodeLevels(t, s.Body())
	if n := len(levels); n < 11 || n > 13 {
		t.Fatalf("%d tranches de 0,5 s, attendu ~12 (6 s)\n%s", n, logs.all())
	}
	first, second := mean(levels[1:5]), mean(levels[7:11]) // loin des raccords
	if d := second - first; math.Abs(d-9) > 1.5 {
		t.Errorf("le 2e morceau est plus fort de %.1f dB, attendu 9 (quiet −12 dB, loud −3 dB) ; niveaux %v", d, levels)
	}

	// les titres ont été annoncés, dans l'ordre, avec les identifiants d'administration (la 1re annonce a dû être retentée :
	// l'encodeur n'était pas encore connecté)
	var songs []string
	for _, a := range ice.Admin() {
		if !a.Accepted {
			continue
		}
		q, _ := url.ParseQuery(strings.TrimPrefix(a.Query, "/admin/metadata?"))
		wantAdmin := "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:adminpw"))
		if a.Auth != wantAdmin || q.Get("mount") != "/live" || q.Get("mode") != "updinfo" {
			t.Errorf("annonce : %+v", a)
		}
		songs = append(songs, q.Get("song"))
	}
	if strings.Join(songs, "|") != "Artiste - Titre A|Titre B & co" {
		t.Errorf("titres annoncés : %q\n%s", songs, logs.all())
	}
}

// Un fichier cassé ne coupe pas la radio : il est signalé, sauté, et ni annoncé ni traité.
func TestRadioSkipsABrokenTrack(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 2)
	writeFile(t, dir, "casse.mp3", "ceci n'est pas du mp3")
	pl := writeFile(t, dir, "playlist.txt", "t.mp3 | Avant\ncasse.mp3 | Cassé\nabsent.mp3 | Absent\nt.mp3 | Après\n")
	ice := newFakeIcecast(t)
	var logs syncLog
	r := run(t, radioOptions(t, pl, ice.URL("/live"), logs.logf))
	r.Wait()

	if !logs.has("morceau ignoré") || !logs.has("absent.mp3") || !logs.has("casse.mp3") {
		t.Errorf("les morceaux cassés doivent être signalés :\n%s", logs.all())
	}
	if n := len(decodeLevels(t, ice.Sources()[0].Body())); n < 7 || n > 9 { // 2 × 2 s
		t.Errorf("%d tranches, attendu ~8 : les deux bons morceaux doivent passer", n)
	}
	for _, a := range ice.Admin() {
		if strings.Contains(a.Query, "Cass") || strings.Contains(a.Query, "Absent") {
			t.Errorf("un morceau cassé ne doit pas être annoncé : %s", a.Query)
		}
	}
}

// Un profil inconnu dans la liste est signalé, et la chaîne garde le profil en place.
func TestRadioUnknownProfileKeepsThePrevious(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 2)
	cfg := writeFile(t, dir, "reglages.yaml", profilesYAML)
	pl := writeFile(t, dir, "playlist.txt", "#profile: quiet\nt.mp3 | A\n#profile: metal\nt.mp3 | B\n")
	ice := newFakeIcecast(t)
	var logs syncLog
	o := radioOptions(t, pl, ice.URL("/live"), logs.logf)
	o.config = cfg
	r := run(t, o)
	r.Wait()
	if !logs.has(`profil "metal" ignoré`) {
		t.Errorf("le profil inconnu doit être signalé :\n%s", logs.all())
	}
	levels := decodeLevels(t, ice.Sources()[0].Body())
	if d := mean(levels[5:7]) - mean(levels[1:3]); math.Abs(d) > 1.5 { // même niveau : le profil quiet est resté
		t.Errorf("le niveau a changé de %.1f dB malgré le profil refusé ; %v", d, levels)
	}
}

// Si Icecast coupe la connexion, l'encodeur est relancé et la diffusion reprend, sans que la radio s'arrête.
func TestRadioRestartsTheEncoder(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 3)
	pl := writeFile(t, dir, "playlist.txt", "t.mp3 | Boucle\n")
	ice := newFakeIcecast(t)
	ice.dropFirstAfter = 20000
	var logs syncLog
	o := radioOptions(t, pl, ice.URL("/live"), logs.logf)
	o.loop = true // la radio ne s'arrête pas d'elle-même
	r := run(t, o)

	waitFor(t, "une deuxième connexion source", 20*time.Second, func() bool {
		s := ice.Sources()
		return len(s) >= 2 && len(s[1].Body()) > 30000
	})
	r.Stop()
	if !logs.has("encodeur arrêté") {
		t.Errorf("l'arrêt de l'encodeur doit être signalé :\n%s", logs.all())
	}
	if n := len(decodeLevels(t, ice.Sources()[1].Body())); n < 2 {
		t.Errorf("la reprise doit contenir du son décodable (%d tranches)", n)
	}
}

// Avec le panneau, un réglage fait pendant la diffusion change le son qui part vers Icecast.
func TestRadioPanelTunesTheOutgoingStream(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 3)
	pl := writeFile(t, dir, "playlist.txt", "t.mp3 | Boucle\n")
	ice := newFakeIcecast(t)
	var logs syncLog
	o := radioOptions(t, pl, ice.URL("/live"), logs.logf)
	o.loop, o.panel = true, "127.0.0.1:0"
	r := run(t, o)
	if !strings.HasPrefix(r.PanelURL, "http://127.0.0.1:") {
		t.Fatalf("panneau : %q", r.PanelURL)
	}
	body := func() []byte {
		s := ice.Sources()
		if len(s) == 0 {
			return nil
		}
		return s[0].Body()
	}
	waitFor(t, "une seconde de son", 20*time.Second, func() bool { return len(body()) > 16000 })

	state := `{"kind":"profile","name":"reglage","intensity":1,"bypass":false,
	  "profile":{"bands":[{"type":"gain","freq":0,"gain":-12,"q":0,"order":0}],"compression":null,"saturation":null,"multiband":null,"width":0,"limiter":null},"fx":[]}`
	req, _ := http.NewRequest("PUT", r.PanelURL+"api/state", strings.NewReader(state))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PUT : %v %v", err, resp)
	}
	resp.Body.Close()
	before := len(body())
	waitFor(t, "deux secondes de son après le réglage", 20*time.Second, func() bool { return len(body()) > before+40000 })
	r.Stop()

	levels := decodeLevels(t, body())
	early, late := mean(levels[1:2]), mean(levels[len(levels)-3:len(levels)-1])
	if d := late - early; math.Abs(d+12) > 2 {
		t.Errorf("le réglage de −12 dB donne %.1f dB ; niveaux %v", d, levels)
	}
}

// ---------------------------------------------------------------- unités : planning, source, annonces

func TestParsePlaylist(t *testing.T) {
	text := `# la liste du matin
#profile: radio-talk
journal.mp3 | Le journal
 chronique.mp3

#PROFILE:   radio-hits
/abs/musique.mp3 | Artiste - Titre | avec une barre
#note: un commentaire quelconque
#profile:
fin.wav
`
	tracks, err := parsePlaylist(strings.NewReader(text), "/radio")
	if err != nil {
		t.Fatal(err)
	}
	want := []Track{
		{Path: "/radio/journal.mp3", Title: "Le journal", Profile: "radio-talk"},
		{Path: "/radio/chronique.mp3", Title: "chronique", Profile: "radio-talk"},
		{Path: "/abs/musique.mp3", Title: "Artiste - Titre | avec une barre", Profile: "radio-hits"},
		{Path: "/radio/fin.wav", Title: "fin", Profile: ""},
	}
	if len(tracks) != len(want) {
		t.Fatalf("%d morceaux : %+v", len(tracks), tracks)
	}
	for i := range want {
		if tracks[i] != want[i] {
			t.Errorf("morceau %d : %+v, attendu %+v", i, tracks[i], want[i])
		}
	}
	for name, text := range map[string]string{"vide": "", "que des commentaires": "# rien\n#profile: x\n", "chemin vide": " | titre\n"} {
		if _, err := parsePlaylist(strings.NewReader(text), "."); err == nil {
			t.Errorf("%s : doit échouer", name)
		}
	}
}

func TestPlaylistScheduler(t *testing.T) {
	tracks := []Track{{Path: "a"}, {Path: "b"}}
	var got []string
	once := &playlistScheduler{tracks: tracks}
	for {
		tr, ok := once.Next(context.Background())
		if !ok {
			break
		}
		got = append(got, tr.Path)
	}
	if strings.Join(got, "") != "ab" {
		t.Errorf("sans boucle : %v", got)
	}
	looping := &playlistScheduler{tracks: tracks, loop: true}
	got = nil
	for i := 0; i < 5; i++ {
		tr, _ := looping.Next(context.Background())
		got = append(got, tr.Path)
	}
	if strings.Join(got, "") != "ababa" {
		t.Errorf("en boucle : %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := looping.Next(ctx); ok {
		t.Error("après l'annulation, il n'y a plus rien à jouer")
	}
}

// fakeOpener sert des morceaux en mémoire, ou une erreur.
func fakeOpener(tracks map[string][]byte, closeErr map[string]error) opener {
	return func(ctx context.Context, path string) (io.ReadCloser, error) {
		data, ok := tracks[path]
		if !ok {
			return nil, errors.New("fichier introuvable")
		}
		return &fakeTrack{Reader: bytes.NewReader(data), err: closeErr[path]}, nil
	}
}

type fakeTrack struct {
	*bytes.Reader
	err error
}

func (f *fakeTrack) Close() error { return f.err }

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Les morceaux sont enchaînés sans trou ni doublon, chacun est annoncé avant son premier octet, les morceaux qui ne s'ouvrent
// pas sont sautés, et une trame incomplète en fin de morceau est jetée pour garder l'alignement.
func TestTrackSourceChainsTracks(t *testing.T) {
	data := map[string][]byte{
		"a": {1, 1, 1, 1, 2, 2, 2, 2},
		"b": {3, 3, 3, 3, 4, 4, 4, 4, 5, 5}, // 10 octets : la dernière trame est incomplète (2 octets)
		"c": {6, 6, 6, 6},
	}
	var logs syncLog
	var started []string
	src := &trackSource{
		ctx:       context.Background(),
		sched:     &playlistScheduler{tracks: []Track{{Path: "a", Title: "A"}, {Path: "absent", Title: "X"}, {Path: "b", Title: "B"}, {Path: "c", Title: "C"}}},
		open:      fakeOpener(data, map[string]error{"c": errors.New("décodeur en panne")}),
		frameSize: 4,
		onTrack:   func(tr Track) { started = append(started, tr.Title) },
		logf:      logs.logf,
	}
	got := readAll(t, src)
	want := []byte{1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 6, 6, 6, 6}
	if !bytes.Equal(got, want) {
		t.Errorf("flux %v, attendu %v", got, want)
	}
	if strings.Join(started, ",") != "A,B,C" {
		t.Errorf("annonces : %v (le morceau absent n'est pas annoncé)", started)
	}
	if !logs.has("morceau ignoré (absent)") || !logs.has("décodage de c : décodeur en panne") {
		t.Errorf("journal :\n%s", logs.all())
	}
}

// onTrack est appelé avant le premier octet du morceau, jamais après.
func TestTrackSourceAnnouncesBeforeTheFirstByte(t *testing.T) {
	var order []string
	src := &trackSource{
		ctx:       context.Background(),
		sched:     &playlistScheduler{tracks: []Track{{Path: "a", Title: "A"}, {Path: "b", Title: "B"}}},
		open:      fakeOpener(map[string][]byte{"a": {1, 1, 1, 1}, "b": {2, 2, 2, 2}}, nil),
		frameSize: 4, logf: func(string, ...any) {},
	}
	src.onTrack = func(tr Track) { order = append(order, "début "+tr.Title) }
	buf := make([]byte, 4)
	for i := 0; i < 2; i++ {
		if _, err := io.ReadFull(src, buf); err != nil {
			t.Fatal(err)
		}
		order = append(order, fmt.Sprint("octet ", buf[0]))
	}
	if got := strings.Join(order, ", "); got != "début A, octet 1, début B, octet 2" {
		t.Errorf("ordre : %s", got)
	}
}

func TestTrackSourceStopsOnCancelAndWhenEmpty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &trackSource{ctx: ctx, frameSize: 4, logf: func(string, ...any) {},
		sched: &playlistScheduler{tracks: []Track{{Path: "a"}}, loop: true}, open: fakeOpener(map[string][]byte{"a": {1, 1, 1, 1}}, nil)}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(src, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := src.Read(buf); err != io.EOF {
		t.Errorf("après l'annulation : %v, attendu io.EOF", err)
	}
	// une liste dont aucun morceau ne s'ouvre se termine au lieu de tourner à vide
	empty := &trackSource{ctx: context.Background(), frameSize: 4, logf: func(string, ...any) {},
		sched: &playlistScheduler{tracks: []Track{{Path: "x"}, {Path: "y"}}}, open: fakeOpener(nil, nil)}
	if got := readAll(t, empty); len(got) != 0 {
		t.Errorf("%d octets", len(got))
	}
}

func TestPacedReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// 0,5 s de son à 10 fois le temps réel : ~50 ms
	r := newPacedReader(ctx, bytes.NewReader(make([]byte, 44100*4)), 44100, 2, 10)
	start := time.Now()
	buf := make([]byte, 44100*4/2)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 40*time.Millisecond || d > 400*time.Millisecond {
		t.Errorf("0,5 s à ×10 a pris %v, attendu ~50 ms", d)
	}
	cancel()
	if _, err := r.Read(buf); err != io.EOF {
		t.Errorf("après l'annulation : %v, attendu io.EOF", err)
	}
}

// Régression : une fois le contexte annulé, la source doit s'arrêter pour de bon, même quand l'échéance de livraison est
// déjà passée (un select peut alors choisir l'échéance à chaque fois, et io.ReadFull ignore un io.EOF qui accompagne
// un bloc complet).
func TestPacedReaderStopsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newPacedReader(ctx, &loopOf{data: make([]byte, 400)}, 44100, 2, 1000)
	buf := make([]byte, 3528)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	cancel()
	for i := 0; i < 200; i++ {
		if _, err := io.ReadFull(r, buf); err == nil {
			t.Fatalf("lecture %d réussie après l'annulation", i)
		}
	}
}

// loopOf lit data en boucle.
type loopOf struct {
	data []byte
	pos  int
}

func (l *loopOf) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c := copy(p[n:], l.data[l.pos:])
		n += c
		l.pos = (l.pos + c) % len(l.data)
	}
	return n, nil
}

func TestParseIcecastURL(t *testing.T) {
	tg, ok := parseIcecastURL("icecast://source:mdp@radio.example:8000/live.mp3")
	if !ok || tg.baseURL != "http://radio.example:8000" || tg.mount != "/live.mp3" {
		t.Errorf("%+v %v", tg, ok)
	}
	if tg, ok = parseIcecastURL("icecast+tls://source:mdp@radio.example/live"); !ok || tg.baseURL != "https://radio.example" {
		t.Errorf("tls : %+v %v", tg, ok)
	}
	for _, out := range []string{"sortie.mp3", "/tmp/sortie.mp3", "http://radio.example/live", "icecast://", "icecast://hote"} {
		if _, ok := parseIcecastURL(out); ok {
			t.Errorf("%q n'est pas une sortie Icecast", out)
		}
	}
}

func TestAdminClientUpdateSong(t *testing.T) {
	var got struct {
		path, user, pass string
		query            url.Values
	}
	status, body := 200, "<iceresponse><message>Metadata update successful</message><return>1</return></iceresponse>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.query = r.URL.Path, r.URL.Query()
		got.user, got.pass, _ = r.BasicAuth()
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	defer srv.Close()
	a := newAdminClient(icecastTarget{baseURL: srv.URL, mount: "/live"}, "admin", "pw")

	if err := a.UpdateSong(context.Background(), "Beyoncé & Jay-Z = 100 %"); err != nil {
		t.Fatal(err)
	}
	if got.path != "/admin/metadata" || got.user != "admin" || got.pass != "pw" ||
		got.query.Get("mount") != "/live" || got.query.Get("mode") != "updinfo" || got.query.Get("song") != "Beyoncé & Jay-Z = 100 %" {
		t.Errorf("requête : %+v", got)
	}
	status = 400
	if err := a.UpdateSong(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "400") {
		t.Errorf("400 : %v", err)
	}
	status, body = 200, "<iceresponse><message>No such mount</message><return>0</return></iceresponse>"
	if err := a.UpdateSong(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "refusé") {
		t.Errorf("return 0 : %v", err)
	}
	srv.Close()
	if err := a.UpdateSong(context.Background(), "x"); err == nil {
		t.Error("serveur arrêté : doit échouer")
	}
}

func TestEncoderArgs(t *testing.T) {
	o := options{rate: 48000, channels: 1, bitrate: "96k", iceName: "Ma radio", output: "icecast://source:mdp@h:8000/live"}
	got := strings.Join(o.encoderArgs(), " ")
	for _, want := range []string{"-f s16le -ar 48000 -ac 1 -i pipe:0", "-c:a libmp3lame -b:a 96k", "-content_type audio/mpeg", "-ice_name Ma radio", "-f mp3 icecast://source:mdp@h:8000/live"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q absent de %q", want, got)
		}
	}
	o.output = "sortie.mp3" // un fichier : les options d'Icecast n'ont pas de sens
	got = strings.Join(o.encoderArgs(), " ")
	if strings.Contains(got, "ice_name") || strings.Contains(got, "content_type") || !strings.HasSuffix(got, "-f mp3 sortie.mp3") {
		t.Errorf("sortie fichier : %q", got)
	}
}

// applyProfile : un profil identique ne coûte rien, un profil inconnu laisse le précédent, "" retire le traitement.
func TestApplyProfile(t *testing.T) {
	cfgFile := writeFile(t, t.TempDir(), "reglages.yaml", profilesYAML)
	var cfgErr error
	r := &radio{o: options{profile: "quiet", logf: func(string, ...any) {}}}
	r.cfg, cfgErr = loadTestConfig(cfgFile)
	if cfgErr != nil {
		t.Fatal(cfgErr)
	}
	r.pl = live.Pipeline{Profile: "quiet", Intensity: 1, Rate: 44100, Channels: 2}
	build, err := r.pl.Build(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	in := bytes.Repeat([]byte{0x40, 0x1f}, 44100*2) // 1 s de stéréo à 8000
	r.sw = audioprofile.NewSwitcher(bytes.NewReader(in), 44100, 2, build)
	r.current = "quiet"

	if err := r.applyProfile("quiet"); err != nil {
		t.Errorf("profil identique : %v", err)
	}
	if err := r.applyProfile("metal"); err == nil || r.current != "quiet" {
		t.Errorf("profil inconnu : %v, profil en place %q", err, r.current)
	}
	if err := r.applyProfile("loud"); err != nil || r.current != "loud" {
		t.Errorf("loud : %v %q", err, r.current)
	}
	if err := r.applyProfile(""); err != nil || r.current != "" {
		t.Errorf("sans traitement : %v %q", err, r.current)
	}
}

func TestStartErrors(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "playlist.txt", "a.mp3\n")
	empty := writeFile(t, dir, "vide.txt", "# rien\n")
	cfg := writeFile(t, dir, "reglages.yaml", profilesYAML)
	base := options{playlist: good, output: "sortie.mp3", bitrate: "128k", rate: 44100, channels: 2, intensity: 1,
		ffmpeg: os.Args[0]} // un exécutable qui existe : seule la validation est en jeu, rien n'est lancé
	for name, tc := range map[string]struct {
		mod  func(*options)
		want string
	}{
		"fréquence":          {func(o *options) { o.rate = 0 }, "-rate"},
		"canaux":             {func(o *options) { o.channels = 0 }, "-channels"},
		"sans sortie":        {func(o *options) { o.output = "" }, "-output"},
		"ffmpeg absent":      {func(o *options) { o.ffmpeg = "/nulle/part/ffmpeg" }, "ffmpeg introuvable"},
		"liste absente":      {func(o *options) { o.playlist = "/nulle/part.txt" }, "part.txt"},
		"liste vide":         {func(o *options) { o.playlist = empty }, "aucun morceau"},
		"profil inconnu":     {func(o *options) { o.profile = "metal" }, `profil inconnu "metal"`},
		"effet invalide":     {func(o *options) { o.fx = []string{"reverb:room=3"} }, "room doit être dans"},
		"config absente":     {func(o *options) { o.config = "/nulle/part.yaml" }, "part.yaml"},
		"adresse du panneau": {func(o *options) { o.config, o.panel = cfg, "256.256.256.256:1" }, "256.256.256.256"},
	} {
		o := base
		tc.mod(&o)
		r, err := start(context.Background(), o)
		if err == nil {
			r.Stop()
			t.Errorf("%s : doit échouer", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : %v, attendu %q", name, err, tc.want)
		}
	}
}

func loadTestConfig(path string) (*config.Config, error) { return config.Load(path) }

// Avec le panneau, les mesures portent sur le flux qui part vers Icecast : l'entrée est le son décodé, la sortie le son traité.
func TestRadioPanelMetersTheOutgoingStream(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	makeTrack(t, dir, "t.mp3", 3)
	pl := writeFile(t, dir, "playlist.txt", "t.mp3 | Boucle\n")
	ice := newFakeIcecast(t)
	o := radioOptions(t, pl, ice.URL("/live"), func(string, ...any) {})
	o.loop, o.panel = true, "127.0.0.1:0"
	r := run(t, o)
	size := func() int {
		s := ice.Sources()
		if len(s) == 0 {
			return 0
		}
		return len(s[0].Body())
	}
	waitFor(t, "une seconde de son", 20*time.Second, func() bool { return size() > 16000 })

	state := `{"kind":"profile","name":"reglage","intensity":1,"bypass":false,
	  "profile":{"bands":[{"type":"gain","freq":0,"gain":-12,"q":0,"order":0}],"compression":null,"saturation":null,"multiband":null,"width":0,"limiter":null},"fx":[]}`
	req, _ := http.NewRequest("PUT", r.PanelURL+"api/state", strings.NewReader(state))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PUT : %v %v", err, resp)
	}
	resp.Body.Close()
	before := size()
	waitFor(t, "une seconde de son après le réglage", 20*time.Second, func() bool { return size() > before+16000 })

	sse, err := http.Get(r.PanelURL + "api/meters")
	if err != nil {
		t.Fatal(err)
	}
	defer sse.Body.Close()
	var f panel.MeterFrame
	br := bufio.NewReader(sse.Body)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			json.Unmarshal([]byte(data), &f)
			if f.In.PeakDB[0] > -60 { // le flux a commencé
				break
			}
		}
	}
	// la sinusoïde de lavfi est à −18 dBFS, et le passage de mono à stéréo la baisse de 3 dB : ~−21 dBFS. Après −12 dB, la
	// sortie est ~12 dB plus bas que l'entrée
	if math.Abs(f.In.PeakDB[0]+21) > 1.5 {
		t.Errorf("entrée : %.1f dBFS, attendu ~−21", f.In.PeakDB[0])
	}
	if d := f.Out.PeakDB[0] - f.In.PeakDB[0]; math.Abs(d+12) > 1.5 {
		t.Errorf("écart sortie − entrée : %.1f dB, attendu −12", d)
	}
	if len(f.Out.Spectrum) != 64 || len(f.Curve) != 64 {
		t.Errorf("%d bandes, %d points de courbe", len(f.Out.Spectrum), len(f.Curve))
	}
}
