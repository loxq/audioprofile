package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
	"github.com/loxq/audioprofile/live"
	"github.com/loxq/audioprofile/panel"
)

// options règle la radio.
type options struct {
	playlist string // fichier de la liste de morceaux (voir parsePlaylist)
	output   string // où l'encodeur envoie le son : icecast://source:mdp@hote:8000/montage, ou un fichier
	bitrate  string // débit mp3 de l'encodeur
	rate     int    // fréquence d'échantillonnage du flux traité
	channels int    // nombre de canaux du flux traité

	config    string   // fichier de réglages YAML (facultatif) : profils personnalisés
	profile   string   // profil par défaut ; vide : pas de traitement
	intensity float64  // intensité du profil
	fx        []string // effets après le profil, au format de fx.Parse
	panel     string   // adresse du panneau de réglage (loopback) ; vide : pas de panneau
	loop      bool     // repasser la liste quand elle est finie

	ffmpeg        string // programme ffmpeg
	iceName       string // nom de la station, annoncé par l'encodeur à Icecast
	adminUser     string // administration d'Icecast : pour annoncer le titre en cours
	adminPassword string // vide : pas d'annonce
	retry         time.Duration
	announceEvery time.Duration // pause entre deux essais d'annonce du titre ; <= 0 : une demi-seconde
	speed         float64       // 1 = temps réel ; plus : pour les tests

	hooks hooks // vos propres planificateur et décodeur, à la place de la liste et de ffmpeg
	logf  func(format string, args ...any)
}

// hooks sont les deux endroits où votre programme se branche : ce qu'on diffuse ensuite, et comment on le décode.
// Un champ nul prend la valeur de l'exemple (la liste de -playlist, le décodeur ffmpeg).
type hooks struct {
	sched scheduler
	open  opener
}

// radio est la chaîne en marche : planificateur → décodeur → traitement → encodeur → Icecast.
type radio struct {
	o        options
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      *config.Config
	pl       live.Pipeline
	sw       *audioprofile.Switcher
	out      io.Reader   // le flux traité que lit l'encodeur : le Switcher, ou le Switcher mesuré
	tuner    *live.Tuner // non nil si le panneau est ouvert
	admin    *adminClient
	frame    int
	done     chan struct{}
	stopPane func()
	wg       sync.WaitGroup
	stopOnce sync.Once

	PanelURL string // l'adresse du panneau (vide sans panneau)

	mu      sync.Mutex
	current string // profil en place

	annMu  sync.Mutex
	annSeq int // numéro de la dernière annonce : une annonce plus ancienne n'est plus retentée
}

// start monte toute la chaîne et la lance. Elle tourne jusqu'à Stop, ou jusqu'à la fin de la liste si elle ne boucle pas.
func start(parent context.Context, o options) (*radio, error) {
	if o.rate <= 0 || o.channels < 1 {
		return nil, fmt.Errorf("-rate et -channels doivent être positifs (reçu %d et %d)", o.rate, o.channels)
	}
	if o.output == "" {
		return nil, errors.New("-output est obligatoire (icecast://source:mot-de-passe@hote:8000/montage, ou un fichier .mp3)")
	}
	if o.speed <= 0 {
		o.speed = 1
	}
	if o.logf == nil {
		o.logf = func(string, ...any) {}
	}
	if o.retry <= 0 {
		o.retry = 3 * time.Second
	}
	if o.ffmpeg == "" {
		o.ffmpeg = "ffmpeg"
	}
	if _, err := exec.LookPath(o.ffmpeg); err != nil {
		return nil, fmt.Errorf("ffmpeg introuvable (%w) : il encode le flux traité", err)
	}

	// 1. ce qu'on diffuse : la liste (ou votre planificateur) et le décodeur
	sched, open := o.hooks.sched, o.hooks.open
	if sched == nil {
		f, err := os.Open(o.playlist)
		if err != nil {
			return nil, err
		}
		tracks, err := parsePlaylist(f, filepath.Dir(o.playlist))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s : %w", o.playlist, err)
		}
		sched = &playlistScheduler{tracks: tracks, loop: o.loop}
	}
	if open == nil {
		open = ffmpegOpener(o.ffmpeg, o.rate, o.channels)
	}

	// 2. le traitement : le profil par défaut, vérifié avant de démarrer
	cfg, err := config.Load(o.config) // "" : les valeurs par défaut
	if err != nil {
		return nil, err
	}
	pl := live.Pipeline{Profile: o.profile, Intensity: o.intensity, Rate: o.rate, Channels: o.channels, FX: o.fx}
	build, err := pl.Build(cfg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	r := &radio{o: o, ctx: ctx, cancel: cancel, cfg: cfg, pl: pl, frame: o.channels * 2, done: make(chan struct{}), current: o.profile}

	// 3. la source : les morceaux enchaînés, livrés à la vitesse du temps réel, dans un Switcher qui permet de changer de
	// profil sans clic (au changement d'émission, par exemple)
	tracks := &trackSource{ctx: ctx, sched: sched, open: open, frameSize: r.frame, onTrack: r.trackStarted, logf: o.logf}
	paced := newPacedReader(ctx, tracks, o.rate, o.channels, o.speed)
	// avec le panneau, des mesures avant et après la chaîne : vumètres, sonie et spectre du flux qui part vers Icecast
	var src io.Reader = paced
	var meterIn, meterOut *audioprofile.Meter
	if o.panel != "" {
		meterIn, meterOut = audioprofile.NewMeter(o.rate, o.channels), audioprofile.NewMeter(o.rate, o.channels)
		src = meterIn.Wrap(paced)
	}
	r.sw = audioprofile.NewSwitcher(src, o.rate, o.channels, build)
	r.out = r.sw // ce que lit l'encodeur
	if meterOut != nil {
		r.out = meterOut.Wrap(r.sw)
	}

	// 4. l'outillage de réglage, à garder en développement
	if o.panel != "" {
		if r.tuner, err = live.NewTuner(cfg, pl, r.sw); err != nil {
			cancel()
			return nil, err
		}
		r.tuner.SetMeters(meterIn, meterOut)
		url, stop, err := panel.Serve(o.panel, r.tuner)
		if err != nil {
			cancel()
			return nil, err
		}
		r.PanelURL, r.stopPane = url, stop
		o.logf("panneau de réglage : %s", url)
	}

	// 5. l'annonce des titres
	if target, ok := parseIcecastURL(o.output); ok && o.adminPassword != "" {
		r.admin = newAdminClient(target, o.adminUser, o.adminPassword)
	}

	r.wg.Add(1)
	go func() { defer r.wg.Done(); defer close(r.done); r.run() }()
	return r, nil
}

// Wait attend la fin de la diffusion : la liste est finie (sans boucle), ou Stop a été appelé.
func (r *radio) Wait() { <-r.done }

// Stop arrête tout et attend la fin des processus et des goroutines.
func (r *radio) Stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		if r.stopPane != nil {
			r.stopPane()
		}
		r.wg.Wait()
	})
}

// trackStarted est appelé quand un morceau commence à entrer dans la chaîne : c'est ici que le planning agit sur le son
// (profil) et sur l'affichage (titre).
func (r *radio) trackStarted(t Track) {
	r.o.logf("en cours : %s", t.Title)
	profile := t.Profile
	if profile == "" {
		profile = r.o.profile
	}
	if err := r.applyProfile(profile); err != nil {
		r.o.logf("profil %q ignoré, le précédent reste : %v", profile, err)
	}
	if r.admin != nil {
		r.announce(t.Title)
	}
}

// Icecast ne connaît pas encore le point de montage quand le premier morceau démarre (l'encodeur n'est pas connecté) :
// l'annonce échoue alors. On la retente un moment, tant qu'aucun titre plus récent n'a pris la place.
const (
	announceAttempts = 20
	announceEvery    = 500 * time.Millisecond
)

// announce annonce le titre en cours, hors du chemin du son : un échec n'interrompt jamais la diffusion.
func (r *radio) announce(title string) {
	r.annMu.Lock()
	r.annSeq++
	seq := r.annSeq
	r.annMu.Unlock()
	every := r.o.announceEvery
	if every <= 0 {
		every = announceEvery
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		var err error
		for attempt := 0; attempt < announceAttempts; attempt++ {
			ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
			err = r.admin.UpdateSong(ctx, title)
			cancel()
			r.annMu.Lock()
			stale := r.annSeq != seq
			r.annMu.Unlock()
			if err == nil || stale {
				return
			}
			select {
			case <-time.After(every):
			case <-r.ctx.Done():
				return
			}
		}
		r.o.logf("titre %q non annoncé : %v", title, err)
	}()
}

// applyProfile fait passer la chaîne au profil demandé, sans clic. Un profil identique à celui en place ne coûte rien.
// Avec le panneau ouvert, le changement passe par lui pour qu'il reste cohérent : le planning l'emporte alors sur les
// réglages faits à la main, à chaque changement de profil.
func (r *radio) applyProfile(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == r.current {
		return nil
	}
	if r.tuner != nil {
		st := r.tuner.State()
		st.Kind, st.Name, st.Profile, st.Amp = panel.KindNone, "", nil, nil
		if name != "" {
			pp, _, err := r.tuner.Preset(panel.KindProfile, name)
			if err != nil {
				return err
			}
			st.Kind, st.Name, st.Profile = panel.KindProfile, name, pp
		}
		if err := r.tuner.Apply(st); err != nil {
			return err
		}
	} else {
		pl := r.pl
		pl.Profile = name
		build, err := pl.Build(r.cfg)
		if err != nil {
			return err
		}
		r.sw.Swap(build)
	}
	r.current = name
	return nil
}

// ---------------------------------------------------------------- l'encodeur et Icecast

// encoderArgs est la ligne de commande de l'encodeur : du PCM sur l'entrée standard, du mp3 vers la sortie.
func (o options) encoderArgs() []string {
	args := []string{"-hide_banner", "-loglevel", "warning",
		"-f", "s16le", "-ar", strconv.Itoa(o.rate), "-ac", strconv.Itoa(o.channels), "-i", "pipe:0",
		"-c:a", "libmp3lame", "-b:a", o.bitrate}
	if strings.HasPrefix(o.output, "icecast://") || strings.HasPrefix(o.output, "icecast+tls://") {
		args = append(args, "-content_type", "audio/mpeg")
		if o.iceName != "" {
			args = append(args, "-ice_name", o.iceName)
		}
	}
	return append(args, "-f", "mp3", o.output)
}

// run entretient l'encodeur : s'il s'arrête (Icecast redémarré, réseau coupé), il le relance après une pause, et la
// diffusion reprend là où en est la playlist, comme une radio en direct (le son de la coupure est perdu).
func (r *radio) run() {
	for r.ctx.Err() == nil {
		finished, err := r.encodeOnce()
		if finished {
			if err != nil {
				r.o.logf("encodeur : %v", err)
			}
			return
		}
		if r.ctx.Err() != nil {
			return
		}
		r.o.logf("encodeur arrêté (%v) : nouvel essai dans %v", err, r.o.retry)
		select {
		case <-time.After(r.o.retry):
		case <-r.ctx.Done():
		}
	}
}

// encodeOnce lance l'encodeur et lui envoie le flux traité. finished est vrai quand le flux est terminé (fin de la
// liste) : l'encodeur a alors tout reçu et se termine de lui-même.
func (r *radio) encodeOnce() (finished bool, err error) {
	cmd := exec.CommandContext(r.ctx, r.o.ffmpeg, r.o.encoderArgs()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &lineLogger{logf: r.o.logf, prefix: "ffmpeg : ", buf: &stderr}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	_, copyErr := io.Copy(stdin, r.out) // le flux traité, au rythme du temps réel
	stdin.Close()
	waitErr := cmd.Wait()
	if copyErr == nil { // le Switcher est arrivé à sa fin
		return true, waitErr
	}
	if waitErr != nil {
		return false, waitErr
	}
	return false, copyErr
}

// lineLogger transmet à logf chaque ligne écrite (la sortie d'erreur de ffmpeg).
type lineLogger struct {
	logf   func(string, ...any)
	prefix string
	buf    *bytes.Buffer
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf.Write(p)
	for {
		i := bytes.IndexByte(l.buf.Bytes(), '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(l.buf.Next(i + 1)))
		if line != "" {
			l.logf("%s%s", l.prefix, line)
		}
	}
	return len(p), nil
}
