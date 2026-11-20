// Commande audioprofile : applique un profil d'égaliseur à un flux PCM
// s16le lu sur l'entrée standard et écrit le résultat sur la sortie standard.
//
//	ffmpeg -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
//	  | audioprofile -profile rock \
//	  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/loxq/audioprofile"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "audioprofile:", err)
		}
		os.Exit(2)
	}
}

// run contient toute la logique, séparée de main pour pouvoir être testée.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("audioprofile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		profile   = fs.String("profile", "", "profil d'égaliseur à appliquer (voir -list)")
		amp       = fs.String("amp", "", "préréglage d'ampli à tubes à appliquer à la place d'un profil (voir -list-amps)")
		cabinet   = fs.Bool("cabinet", false, "avec -amp : ajoute la simulation de baffle (son étouffé sur un morceau complet)")
		intensity = fs.Float64("intensity", 1, fmt.Sprintf("intensité du profil : 0 = aucun effet, 1 = complet, jusqu'à %g pour l'exagérer", audioprofile.MaxIntensity))
		rate      = fs.Int("rate", 44100, "fréquence d'échantillonnage en Hz (doit correspondre à celle de ffmpeg)")
		channels  = fs.Int("channels", 2, "nombre de canaux (doit correspondre à celui de ffmpeg)")
		list      = fs.Bool("list", false, "affiche les profils disponibles et quitte")
		listAmps  = fs.Bool("list-amps", false, "affiche les préréglages d'ampli disponibles et quitte")
	)
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage : audioprofile (-profile NOM | -amp NOM) [options] < entrée.raw > sortie.raw

Lit du PCM 16 bits signé little-endian entrelacé (s16le) sur l'entrée standard
et écrit le même format sur la sortie standard. Exemple :

  ffmpeg -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
    | audioprofile -profile rock \
    | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

Profils d'égaliseur : -profile rock, pop, jazz, tube…
Ampli à tubes       : -amp clean, crunch ou lead (-cabinet ajoute le baffle)

Options :
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("argument inattendu %q (les données passent par l'entrée standard)", fs.Arg(0))
	}

	if *list {
		for _, p := range audioprofile.Profiles() {
			fmt.Fprintln(stdout, p)
		}
		return nil
	}

	if *listAmps {
		for _, p := range audioprofile.TubeAmpPresets() {
			fmt.Fprintln(stdout, p)
		}
		return nil
	}

	if *profile == "" && *amp == "" {
		fs.Usage()
		return errors.New("-profile ou -amp est obligatoire (profils : " + profileNames() + " ; amplis : " + ampNames() + ")")
	}
	if *profile != "" && *amp != "" {
		return errors.New("-profile et -amp sont exclusifs : choisissez l'un ou l'autre")
	}
	if *cabinet && *amp == "" {
		return errors.New("-cabinet ne s'utilise qu'avec -amp")
	}
	var (
		p      audioprofile.Profile
		preset audioprofile.TubeAmpPreset
		ok     bool
	)
	if *amp != "" {
		if preset, ok = audioprofile.ParseTubeAmpPreset(*amp); !ok {
			return fmt.Errorf("ampli inconnu %q (amplis : %s)", *amp, ampNames())
		}
	} else if p, ok = audioprofile.ParseProfile(*profile); !ok {
		return fmt.Errorf("profil inconnu %q (profils : %s)", *profile, profileNames())
	}
	if *rate <= 0 {
		return fmt.Errorf("-rate doit être > 0 (reçu %d)", *rate)
	}
	if *channels < 1 {
		return fmt.Errorf("-channels doit être >= 1 (reçu %d)", *channels)
	}
	if *intensity < 0 || *intensity > audioprofile.MaxIntensity {
		return fmt.Errorf("-intensity doit être dans [0, %g] (reçu %g)", audioprofile.MaxIntensity, *intensity)
	}
	if isTerminal(stdin) {
		return errors.New("aucune donnée sur l'entrée standard : utilisez un pipe (ffmpeg ... - | audioprofile ...) ou une redirection (< fichier.raw)")
	}
	if isTerminal(stdout) {
		return errors.New("refus d'écrire du PCM brut sur un terminal : redirigez la sortie (> fichier.raw) ou utilisez un pipe (| ffplay ...)")
	}

	out := bufio.NewWriterSize(stdout, 64*1024)
	var processed io.Reader
	if *amp != "" {
		s := preset.Settings().Scaled(*intensity)
		s.Cabinet = *cabinet
		processed = audioprofile.TubeAmp(stdin, *rate, *channels, s)
	} else {
		processed = audioprofile.ApplyProfile(stdin, *rate, *channels, p, *intensity)
	}
	if _, err := io.Copy(out, processed); err != nil {
		return fmt.Errorf("traitement : %w", err)
	}
	return out.Flush()
}

func profileNames() string {
	var names []string
	for _, p := range audioprofile.Profiles() {
		names = append(names, p.String())
	}
	return strings.Join(names, ", ")
}

func ampNames() string {
	var names []string
	for _, p := range audioprofile.TubeAmpPresets() {
		names = append(names, p.String())
	}
	return strings.Join(names, ", ")
}

// isTerminal indique si r est un terminal (et non un pipe ou un fichier).
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
