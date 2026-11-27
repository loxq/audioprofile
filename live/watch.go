package live

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/config"
)

// DefaultWatchInterval est l'intervalle entre deux regards sur le fichier.
const DefaultWatchInterval = 250 * time.Millisecond

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
func watchConfig(path string, last []byte, interval time.Duration, stop <-chan struct{}, apply func(*config.Config) error, logf func(format string, args ...any)) {
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
		cfg, err := config.Parse(bytes.NewReader(data))
		if err == nil {
			err = apply(cfg)
		}
		if err != nil {
			logf("config %s : %v (réglages précédents conservés)", path, err)
			continue
		}
		logf("config %s : rechargé", path)
	}
}

// WatchOptions règle Watch.
type WatchOptions struct {
	Interval time.Duration                    // intervalle entre deux regards ; <= 0 : DefaultWatchInterval
	Logf     func(format string, args ...any) // reçoit « rechargé » et les erreurs de rechargement ; nil : rien
}

// Watch surveille le fichier de réglages et, quand il change, reconstruit la chaîne de p avec la nouvelle configuration
// et la passe à sw (Switcher.Swap) : le changement s'entend sans clic. Un fichier invalide ou refusé est signalé par
// Logf et laisse les réglages précédents en place.
//
// La surveillance attend que le fichier soit resté identique pendant un intervalle entier (un éditeur écrit parfois
// en deux temps) et ne recharge pas deux fois le même contenu. Watch lit le fichier une fois en entrant : une erreur
// est renvoyée. Il renvoie la fonction qui arrête la surveillance et attend sa fin (sûre à appeler plusieurs fois) ;
// après elle, Logf n'est plus appelé.
func Watch(path string, sw *audioprofile.Switcher, p Pipeline, o WatchOptions) (stop func(), err error) {
	last, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("watch : " + err.Error())
	}
	if o.Interval <= 0 {
		o.Interval = DefaultWatchInterval
	}
	logf := o.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	done, quit := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		watchConfig(path, last, o.Interval, quit, func(cfg *config.Config) error {
			process, err := p.Build(cfg)
			if err == nil {
				sw.Swap(process)
			}
			return err
		}, logf)
	}()
	var once sync.Once
	return func() { once.Do(func() { close(quit); <-done }) }, nil
}
