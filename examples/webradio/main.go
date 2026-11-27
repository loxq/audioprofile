// Command webradio montre où s'intercaler audioprofile dans une webradio Go → Icecast : une playlist planifiée est
// décodée, traitée par un profil (qui peut changer d'une émission à l'autre), ré-encodée en mp3 puis poussée sur
// Icecast, avec l'annonce du titre en cours.
//
//	go run ./examples/webradio -playlist playlist.txt -output icecast://source:mot-de-passe@127.0.0.1:8000/live
//
// Voir README.md dans ce dossier.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
)

type fxFlags []string

func (f *fxFlags) String() string     { return strings.Join(*f, " ") }
func (f *fxFlags) Set(v string) error { *f = append(*f, v); return nil }

func main() {
	var o options
	var fx fxFlags
	flag.StringVar(&o.playlist, "playlist", "", "fichier de la liste de morceaux (un chemin par ligne ; « #profile: nom » change de profil)")
	flag.StringVar(&o.output, "output", "", "sortie de l'encodeur : icecast://source:mot-de-passe@hote:8000/montage, ou un fichier .mp3 pour essayer")
	flag.StringVar(&o.bitrate, "bitrate", "128k", "débit mp3")
	flag.IntVar(&o.rate, "rate", 44100, "fréquence d'échantillonnage du flux, en Hz")
	flag.IntVar(&o.channels, "channels", 2, "nombre de canaux du flux")
	flag.StringVar(&o.config, "config", "", "fichier de réglages YAML : profils personnalisés (facultatif)")
	flag.StringVar(&o.profile, "profile", "", "profil par défaut, pour les morceaux qui n'en demandent pas (vide : aucun traitement)")
	flag.Float64Var(&o.intensity, "intensity", 1, "intensité du profil")
	flag.Var(&fx, "fx", "effet après le profil, au format nom:cle=valeur (répétable)")
	flag.StringVar(&o.panel, "panel", "", "adresse du panneau de réglage, par exemple 127.0.0.1:8080 (vide : pas de panneau)")
	flag.BoolVar(&o.loop, "loop", true, "repasser la liste quand elle est finie")
	flag.StringVar(&o.ffmpeg, "ffmpeg", "ffmpeg", "programme ffmpeg (encodeur et décodeur)")
	flag.StringVar(&o.iceName, "ice-name", "audioprofile webradio", "nom de la station annoncé à Icecast")
	flag.StringVar(&o.adminUser, "admin-user", "admin", "utilisateur d'administration d'Icecast")
	flag.StringVar(&o.adminPassword, "admin-password", "", "mot de passe d'administration d'Icecast : annonce le titre en cours (vide : pas d'annonce)")
	flag.DurationVar(&o.retry, "retry", 3*time.Second, "pause avant de relancer l'encodeur s'il s'arrête")
	flag.Float64Var(&o.speed, "speed", 1, "vitesse de lecture, 1 = temps réel (plus vite : pour les tests)")
	flag.Parse()
	o.fx = fx
	o.logf = log.Printf

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	r, err := start(ctx, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webradio :", err)
		os.Exit(1)
	}
	defer r.Stop()
	go func() { <-ctx.Done(); r.Stop() }()
	r.Wait()
}
