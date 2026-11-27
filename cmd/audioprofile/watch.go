package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/loxq/audioprofile"
)

// pipeline est la chaîne demandée sur la ligne de commande : un profil ou un ampli, puis les effets. Ses
// réglages viennent d'une configuration, qu'on peut donc remplacer à chaud (-watch).
type pipeline struct {
	profile, amp string
	cabinet      bool
	intensity    float64
	rate         int
	channels     int
	fxSpecs      []string
}

// build résout et vérifie les réglages dans cfg et renvoie la fonction qui enveloppe un flux dans la chaîne.
func (p pipeline) build(cfg *config) (func(io.Reader) io.Reader, error) {
	var (
		ps audioprofile.ProfileSettings
		as audioprofile.TubeAmpSettings
		ok bool
	)
	if p.amp != "" {
		if as, ok = cfg.amp(p.amp); !ok {
			return nil, fmt.Errorf("ampli inconnu %q (amplis : %s)", p.amp, strings.Join(cfg.ampNames(), ", "))
		}
	} else if p.profile != "" {
		if ps, ok = cfg.profile(p.profile); !ok {
			return nil, fmt.Errorf("profil inconnu %q (profils : %s)", p.profile, strings.Join(cfg.profileNames(), ", "))
		}
	}
	if err := cfg.validate(p.rate); err != nil {
		return nil, err
	}
	var effects []fxEffect
	for _, spec := range p.fxSpecs {
		e, err := parseFX(spec, cfg.fxDefaults)
		if err != nil {
			return nil, err
		}
		if err := e.validate(p.rate, p.channels); err != nil {
			return nil, err
		}
		effects = append(effects, e)
	}
	return func(src io.Reader) io.Reader {
		r := src
		switch {
		case p.amp != "":
			s := as.Scaled(p.intensity)
			if p.cabinet { // sans -cabinet, le fichier de configuration décide
				s.Cabinet = true
			}
			r = audioprofile.TubeAmp(r, p.rate, p.channels, s)
		case p.profile != "":
			r = audioprofile.ApplyProfileSettings(r, p.rate, p.channels, ps, p.intensity)
		}
		for _, e := range effects {
			r = e.apply(r, p.rate, p.channels)
		}
		return r
	}, nil
}

// watchInterval est l'intervalle entre deux regards sur le fichier (une variable pour les tests).
var watchInterval = 250 * time.Millisecond

type fileKey struct {
	mod  time.Time
	size int64
}

func statKey(path string) (fileKey, error) {
	st, err := os.Stat(path)
	if err != nil {
		return fileKey{}, err
	}
	return fileKey{st.ModTime(), st.Size()}, nil
}

// watchConfig surveille le fichier de configuration jusqu'à la fermeture de stop. Quand il change, il le
// relit et appelle apply avec la nouvelle configuration ; une erreur (YAML invalide, valeur hors bornes, profil
// disparu) est signalée par logf et laisse les réglages précédents en place.
//
// Un éditeur écrit parfois en deux temps (fichier vidé, puis rempli) : on n'agit que lorsque le fichier est resté
// identique pendant un intervalle entier. Un contenu déjà vu, valide ou non, n'est pas rechargé deux fois, et un
// fichier qui disparaît un instant (enregistrement par renommage) est simplement attendu.
func watchConfig(path string, last []byte, interval time.Duration, stop <-chan struct{}, apply func(*config) error, logf func(format string, args ...any)) {
	seen, _ := statKey(path)
	dirty := false
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		k, err := statKey(path)
		if err != nil {
			continue
		}
		if k != seen {
			seen, dirty = k, true
			continue
		}
		if !dirty {
			continue
		}
		dirty = false
		data, err := os.ReadFile(path)
		if err != nil || bytes.Equal(data, last) {
			continue
		}
		last = data
		cfg, err := parseConfig(bytes.NewReader(data))
		if err == nil {
			err = apply(cfg)
		}
		if err != nil {
			logf("-config %s : %v (réglages précédents conservés)", path, err)
			continue
		}
		logf("-config %s : rechargé", path)
	}
}

// startWatch lance watchConfig dans un goroutine et renvoie la fonction qui l'arrête et attend sa fin.
func startWatch(path string, sw *audioprofile.Switcher, pl pipeline, stderr io.Writer) (stop func(), err error) {
	last, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("-watch : " + err.Error())
	}
	done, quit := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		watchConfig(path, last, watchInterval, quit, func(cfg *config) error {
			process, err := pl.build(cfg)
			if err == nil {
				sw.Swap(process)
			}
			return err
		}, func(format string, args ...any) { fmt.Fprintf(stderr, format+"\n", args...) })
	}()
	var once sync.Once
	return func() { once.Do(func() { close(quit); <-done }) }, nil
}
