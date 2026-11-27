package live

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
)

func run(t *testing.T, build func(io.Reader) io.Reader, in []byte) []byte {
	t.Helper()
	out, err := io.ReadAll(build(bytes.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPipelineBuild(t *testing.T) {
	in := pcm(100, 20000, 2)
	base := Pipeline{Rate: 44100, Channels: 2, Intensity: 1}

	// rien demandé : le flux passe tel quel, avec ou sans configuration
	build, err := base.Build(nil)
	if err != nil || !bytes.Equal(run(t, build, in), in) {
		t.Errorf("chaîne vide : %v", err)
	}

	// profil prédéfini = ApplyProfile ; ampli = TubeAmp ; effet = fx
	p := base
	p.Profile = "rock"
	build, err = p.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := io.ReadAll(audioprofile.ApplyProfile(bytes.NewReader(in), 44100, 2, audioprofile.Rock, 1))
	if got := run(t, build, in); !bytes.Equal(got, want) {
		t.Error("le profil rock doit donner le même flux qu'ApplyProfile")
	}
	p = base
	p.Amp, p.Profile = "crunch", "rock" // l'ampli est prioritaire
	build, err = p.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ = io.ReadAll(audioprofile.TubeAmp(bytes.NewReader(in), 44100, 2, audioprofile.AmpCrunch.Settings()))
	if got := run(t, build, in); !bytes.Equal(got, want) {
		t.Error("l'ampli crunch doit donner le même flux que TubeAmp")
	}
	p.Cabinet = true
	cab, _ := p.Build(nil)
	if bytes.Equal(run(t, cab, in), want) {
		t.Error("Cabinet doit activer le baffle")
	}

	// les réglages du fichier s'appliquent
	cfg, err := config.Parse(strings.NewReader("profiles:\n  reglage:\n    bands:\n      - {type: gain, gain: -12}\n"))
	if err != nil {
		t.Fatal(err)
	}
	p = base
	p.Profile = "reglage"
	build, err = p.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if a := amplitude(run(t, build, in), 5000, 20000); math.Abs(a-2000) > 150 {
		t.Errorf("amplitude %g avec le gain du fichier, attendu ~2000", a)
	}

	// les effets passent après, avec les défauts du fichier
	cfg, _ = config.Parse(strings.NewReader("fx:\n  lpf: {freq: 500}\n"))
	p = base
	p.FX = []string{"lpf"}
	build, err = p.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	direct := Pipeline{Rate: 44100, Channels: 2, Intensity: 1, FX: []string{"lpf:freq=500"}}
	build2, _ := direct.Build(nil)
	if !bytes.Equal(run(t, build, in), run(t, build2, in)) {
		t.Error("un défaut du fichier doit valoir le paramètre écrit")
	}
}

func TestPipelineBuildErrors(t *testing.T) {
	base := Pipeline{Rate: 44100, Channels: 2, Intensity: 1}
	for name, tc := range map[string]struct {
		p    Pipeline
		yml  string
		want string
	}{
		"profil inconnu":    {Pipeline{Profile: "nope"}, "", `profil inconnu "nope"`},
		"ampli inconnu":     {Pipeline{Amp: "nope"}, "", `ampli inconnu "nope"`},
		"effet inconnu":     {Pipeline{FX: []string{"wah"}}, "", `effet inconnu "wah"`},
		"param obligatoire": {Pipeline{FX: []string{"hpf"}}, "", `"freq" obligatoire`},
		"param hors bornes": {Pipeline{FX: []string{"reverb:room=3"}}, "", "room doit être dans"},
		"fichier invalide":  {Pipeline{Profile: "rock"}, "profiles:\n  rock:\n    compression: {ratio: 500}\n", "ratio"},
	} {
		tc.p.Rate, tc.p.Channels, tc.p.Intensity = base.Rate, base.Channels, base.Intensity
		cfg, err := config.Parse(strings.NewReader(tc.yml))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tc.p.Build(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : %v, attendu %q", name, err, tc.want)
		}
	}
}

// Watch recharge le fichier dans la chaîne du Switcher, signale ce qui est invalide et s'arrête proprement.
func TestWatch(t *testing.T) {
	if _, err := Watch("/nulle/part.yaml", nil, Pipeline{}, WatchOptions{}); err == nil {
		t.Error("un fichier absent doit être une erreur")
	}
	path := writeCfg(t, "profiles:\n  reglage:\n    bands:\n      - {type: gain, gain: -12}\n")
	pl := Pipeline{Profile: "reglage", Rate: 44100, Channels: 2, Intensity: 1}
	cfg, _ := config.Load(path)
	build, err := pl.Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 4 * 44100
	in := pcm(100, frames, 2)
	pr, pw := io.Pipe()
	sw := audioprofile.NewSwitcher(pr, 44100, 2, build)

	var logs syncLog
	stop, err := Watch(path, sw, pl, WatchOptions{Interval: 5 * time.Millisecond, Logf: logs.logf})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	go func() { // première moitié, puis attente du rechargement, puis le reste
		pw.Write(in[:2*44100*4])
		waitForT(logs.has, "rechargé")
		pw.Write(in[2*44100*4:])
		pw.Close()
	}()
	head := make([]byte, 44100*4)
	if _, err := io.ReadFull(sw, head); err != nil {
		t.Fatal(err)
	}
	rewrite(t, path, "profiles:\n  reglage:\n    bands:\n      - {type: gain, gain: -3}\n")
	waitFor(t, "rechargement", func() bool { return logs.has("rechargé") })
	rest, err := io.ReadAll(sw)
	if err != nil {
		t.Fatal(err)
	}
	out := append(head, rest...)
	if len(out) != len(in) {
		t.Fatalf("%d octets, attendu %d", len(out), len(in))
	}
	if a := amplitude(out, 10000, 40000); math.Abs(a-2000) > 150 {
		t.Errorf("avant : amplitude %g, attendu ~2000 (−12 dB)", a)
	}
	if a := amplitude(out, 3*44100, frames); math.Abs(a-5660) > 300 {
		t.Errorf("après : amplitude %g, attendu ~5660 (−3 dB)", a)
	}
	stop()
	stop() // sans effet
	n := logs.count()
	rewrite(t, path, "profiles: [\n")
	time.Sleep(60 * time.Millisecond)
	if logs.count() != n {
		t.Error("après stop, Logf ne doit plus être appelé")
	}
}
