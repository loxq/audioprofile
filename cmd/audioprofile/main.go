// Commande audioprofile : applique un profil d'égaliseur à un flux PCM
// s16le lu sur l'entrée standard et écrit le résultat sur la sortie standard.
//
//	ffmpeg -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
//	  | audioprofile -profile rock \
//	  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
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
		intensity = fs.Float64("intensity", 1, fmt.Sprintf("intensité du profil ou de l'ampli : 0 = aucun effet, 1 = complet, jusqu'à %g pour l'exagérer", audioprofile.MaxIntensity))
		ab        = fs.Bool("ab", false, "compare à l'oreille : alterne original et traité, à niveau égal (lit tout le flux avant de jouer, mémoire proportionnelle à la durée)")
		abSeconds = fs.Float64("ab-seconds", 5, "avec -ab : durée de chaque segment, en secondes")
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
Profils radio       : -profile radio-hits, radio-rock, radio-talk… (multibande, stéréo
                      élargie, limiteur à -1 dBFS : niveau ramené vers une valeur
                      commune, un morceau calme sort plus fort, un master très fort plus faible)
Ampli à tubes       : -amp clean, crunch ou lead (-cabinet ajoute le baffle)

Comparer à l'oreille : -ab alterne l'original (A) et le traité (B) toutes les 5 s, à niveau
égal (le plus fort est atténué). Lit tout le flux avant de jouer : mémoire proportionnelle
à la durée, quelques secondes d'attente.

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
	if *abSeconds <= 0 || math.IsNaN(*abSeconds) || math.IsInf(*abSeconds, 0) {
		return fmt.Errorf("-ab-seconds doit être > 0 (reçu %g)", *abSeconds)
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["ab-seconds"] && !*ab {
		return errors.New("-ab-seconds ne s'utilise qu'avec -ab")
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
	process := func(src io.Reader) io.Reader {
		if *amp != "" {
			s := preset.Settings().Scaled(*intensity)
			s.Cabinet = *cabinet
			return audioprofile.TubeAmp(src, *rate, *channels, s)
		}
		return audioprofile.ApplyProfile(src, *rate, *channels, p, *intensity)
	}
	if *ab {
		label := *profile
		if *amp != "" {
			label = "ampli " + *amp
		}
		if err := runAB(out, stderr, stdin, process, *rate, *channels, *abSeconds, label); err != nil {
			return err
		}
		return out.Flush()
	}
	processed := process(stdin)
	if _, err := io.Copy(out, processed); err != nil {
		return fmt.Errorf("traitement : %w", err)
	}
	return out.Flush()
}

// runAB lit tout le flux, le traite, égalise les niveaux de l'original et du traité, puis écrit
// un flux qui alterne l'un et l'autre (l'original en premier).
func runAB(out io.Writer, stderr io.Writer, stdin io.Reader, process func(io.Reader) io.Reader, rate, channels int, seconds float64, label string) error {
	orig, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("lecture : %w", err)
	}
	orig = orig[:len(orig)-len(orig)%(channels*2)] // trames entières
	proc, err := io.ReadAll(process(bytes.NewReader(orig)))
	if err != nil {
		return fmt.Errorf("traitement : %w", err)
	}
	gOrig, gProc := audioprofile.MatchGains(orig, proc, rate, channels)
	fmt.Fprintf(stderr, "A/B : A = original, B = traité (%s) ; segments de %g s : A de 0 à %g s, B de %g à %g s, etc.\n",
		label, seconds, seconds, seconds, 2*seconds)
	switch {
	case gOrig == 0 && gProc == 0:
		fmt.Fprintln(stderr, "A/B : niveaux déjà égaux (ou signal silencieux), aucun gain appliqué.")
	case gOrig < 0:
		fmt.Fprintf(stderr, "A/B : l'original est plus fort que le traité : gain de %.1f dB appliqué à A (le traité n'est pas modifié).\n", gOrig)
	default:
		fmt.Fprintf(stderr, "A/B : le traité est plus fort que l'original : gain de %.1f dB appliqué à B (l'original n'est pas modifié).\n", gProc)
	}
	_, err = out.Write(audioprofile.Alternate(audioprofile.ApplyGain(orig, gOrig), audioprofile.ApplyGain(proc, gProc), rate, channels, seconds))
	return err
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
