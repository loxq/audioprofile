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
	"sync"
	"time"

	"github.com/loxq/audioprofile"
	"github.com/loxq/audioprofile/internal/panel"
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
		profile    = fs.String("profile", "", "profil d'égaliseur à appliquer (voir -list)")
		amp        = fs.String("amp", "", "préréglage d'ampli à tubes à appliquer à la place d'un profil (voir -list-amps)")
		cabinet    = fs.Bool("cabinet", false, "avec -amp : ajoute la simulation de baffle (son étouffé sur un morceau complet)")
		intensity  = fs.Float64("intensity", 1, fmt.Sprintf("intensité du profil ou de l'ampli : 0 = aucun effet, 1 = complet, jusqu'à %g pour l'exagérer", audioprofile.MaxIntensity))
		ab         = fs.Bool("ab", false, "compare à l'oreille : alterne original et traité, à niveau égal (lit tout le flux avant de jouer, mémoire proportionnelle à la durée)")
		abSeconds  = fs.Float64("ab-seconds", 5, "avec -ab : durée de chaque segment, en secondes")
		abDelay    = fs.Float64("ab-delay", 0, "avec -ab : retarde l'affichage de A / B de ce nombre de secondes, pour tenir compte de la mémoire tampon du lecteur (ffplay lit environ 1 s d'avance)")
		targetLUFS = fs.Float64("target-lufs", 0, "amène la sonie intégrée de la sortie à cette valeur (LUFS, de -70 à 0 ; -14 : streaming, -23 : diffusion EBU R128), avec un limiteur sur les crêtes (lit tout le flux avant d'écrire)")
		ceiling    = fs.Float64("ceiling", -1, "avec -target-lufs : plafond des crêtes d'échantillon en dBFS, de -40 à 0")
		loudness   = fs.Bool("loudness", false, "affiche sur la sortie d'erreur la sonie (EBU R128 : intégrée, plage, crête) de l'original et du traité")
		rate       = fs.Int("rate", 44100, "fréquence d'échantillonnage en Hz (doit correspondre à celle de ffmpeg)")
		channels   = fs.Int("channels", 2, "nombre de canaux (doit correspondre à celui de ffmpeg)")
		list       = fs.Bool("list", false, "affiche les profils disponibles et quitte")
		listAmps   = fs.Bool("list-amps", false, "affiche les préréglages d'ampli disponibles et quitte")
		configPath = fs.String("config", "", "fichier YAML de réglages : profils, préréglages d'ampli et valeurs par défaut des effets (ce qu'il ne précise pas garde sa valeur par défaut ; voir -dump-config)")
		watch      = fs.Bool("watch", false, "avec -config : recharge le fichier quand il change et change les réglages en cours de lecture, sans clic (la mise en régime rejoue 2 s de signal)")
		serve      = fs.String("serve", "", "ouvre un panneau de réglage dans le navigateur à cette adresse (ex. 127.0.0.1:8080, ou 127.0.0.1:0 pour un port libre) : curseurs sur le profil, l'ampli et les effets, appliqués en cours de lecture")
		dumpConfig = fs.Bool("dump-config", false, "écrit en YAML les réglages effectifs de tous les profils, amplis et effets (avec -config : fichier compris) et quitte : un point de départ à éditer")
		listFX     = fs.Bool("list-fx", false, "affiche les effets de -fx et leurs paramètres, et quitte")
	)
	var fxs fxList
	fs.Var(&fxs, "fx", "ajoute un effet après le profil ou l'ampli : nom ou nom:cle=valeur,cle=valeur (répétable, appliqués dans l'ordre ; voir -list-fx)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage : audioprofile (-profile NOM | -amp NOM) [options] < entrée.raw > sortie.raw

Lit du PCM 16 bits signé little-endian entrelacé (s16le) sur l'entrée standard
et écrit le même format sur la sortie standard. Exemple :

  ffmpeg -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
    | audioprofile -profile rock \
    | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

Panneau             : -serve 127.0.0.1:8080 ouvre un panneau de curseurs dans le navigateur (profil, ampli,
                      effets), appliqués en cours de lecture ; boucler un morceau : ffmpeg -stream_loop -1 -i ...
Réglages            : -config reglages.yaml change les réglages des profils, amplis et effets
                      (ce qu'il ne précise pas garde sa valeur par défaut) ; -dump-config
                      écrit tous les réglages actuels, un point de départ à éditer
Effets              : -fx hpf:freq=80 -fx lpf:freq=12000,order=4 (après le profil ou l'ampli,
                      dans l'ordre ; -list-fx pour la liste ; -profile devient facultatif)
Profils d'égaliseur : -profile rock, pop, jazz, tube…
Profils radio       : -profile radio-hits, radio-rock, radio-talk… (multibande, stéréo
                      élargie, limiteur à -1 dBFS : niveau ramené vers une valeur
                      commune, un morceau calme sort plus fort, un master très fort plus faible)
Ampli à tubes       : -amp clean, crunch ou lead (-cabinet ajoute le baffle)

Comparer à l'oreille : -ab alterne l'original (A) et le traité (B) toutes les 5 s, à sonie
égale (le plus fort est atténué), et annonce sur la console la source en cours. Lit tout le
flux avant de jouer : mémoire proportionnelle à la durée, quelques secondes d'attente.
Mesurer : -loudness affiche la sonie (EBU R128) avant et après, sur la sortie d'erreur.
Régler la sonie : -target-lufs -14 amène la sortie à -14 LUFS (plafond des crêtes : -ceiling).
Lit tout le flux avant d'écrire, comme -ab.

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

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	if *dumpConfig {
		return cfg.dumpConfig(stdout)
	}

	if *list {
		for _, n := range cfg.profileNames() {
			fmt.Fprintln(stdout, n)
		}
		return nil
	}

	if *listFX {
		fmt.Fprint(stdout, fxListing())
		return nil
	}

	if *listAmps {
		for _, n := range cfg.ampNames() {
			fmt.Fprintln(stdout, n)
		}
		return nil
	}

	var effects []fxEffect
	for _, spec := range fxs {
		e, err := parseFX(spec, cfg.fxDefaults)
		if err != nil {
			return err
		}
		effects = append(effects, e)
	}
	if *profile == "" && *amp == "" && len(effects) == 0 && *serve == "" {
		fs.Usage()
		return errors.New("-profile ou -amp est obligatoire, sauf si au moins un -fx est donné (profils : " + strings.Join(cfg.profileNames(), ", ") + " ; amplis : " + strings.Join(cfg.ampNames(), ", ") + " ; effets : " + fxNames() + ")")
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
	if set["ab-delay"] && !*ab {
		return errors.New("-ab-delay ne s'utilise qu'avec -ab")
	}
	if *abDelay < 0 || math.IsNaN(*abDelay) || math.IsInf(*abDelay, 0) {
		return fmt.Errorf("-ab-delay doit être >= 0 (reçu %g)", *abDelay)
	}
	if *serve != "" {
		switch {
		case *watch:
			return errors.New("-serve et -watch sont exclusifs : le panneau et le fichier se corrigeraient l'un l'autre")
		case *ab:
			return errors.New("-serve et -ab sont exclusifs : -ab lit tout le flux avant de jouer")
		case set["target-lufs"]:
			return errors.New("-serve et -target-lufs sont exclusifs : -target-lufs lit tout le flux avant d'écrire")
		}
	}
	if *watch {
		switch {
		case *configPath == "":
			return errors.New("-watch ne s'utilise qu'avec -config")
		case *ab:
			return errors.New("-watch et -ab sont exclusifs : -ab lit tout le flux avant de jouer")
		case set["target-lufs"]:
			return errors.New("-watch et -target-lufs sont exclusifs : -target-lufs lit tout le flux avant d'écrire")
		}
	}
	if set["ceiling"] && !set["target-lufs"] {
		return errors.New("-ceiling ne s'utilise qu'avec -target-lufs")
	}
	if set["target-lufs"] {
		if *ab {
			return errors.New("-target-lufs et -ab sont exclusifs : -ab égalise déjà les sonies de l'original et du traité")
		}
		if math.IsNaN(*targetLUFS) || *targetLUFS < -70 || *targetLUFS > 0 {
			return fmt.Errorf("-target-lufs doit être dans [-70, 0] (reçu %g)", *targetLUFS)
		}
		if math.IsNaN(*ceiling) || *ceiling < -40 || *ceiling > 0 {
			return fmt.Errorf("-ceiling doit être dans [-40, 0] (reçu %g)", *ceiling)
		}
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
	pl := pipeline{profile: *profile, amp: *amp, cabinet: *cabinet, intensity: *intensity, rate: *rate, channels: *channels, fxSpecs: fxs}
	process, err := pl.build(cfg)
	if err != nil {
		return err
	}
	if isTerminal(stdin) {
		return errors.New("aucune donnée sur l'entrée standard : utilisez un pipe (ffmpeg ... - | audioprofile ...) ou une redirection (< fichier.raw)")
	}
	if isTerminal(stdout) {
		return errors.New("refus d'écrire du PCM brut sur un terminal : redirigez la sortie (> fichier.raw) ou utilisez un pipe (| ffplay ...)")
	}

	outSize := 64 * 1024
	if *watch || *serve != "" { // petits paquets : un réglage doit s'entendre vite, pas après 370 ms de tampon
		outSize = 8 * 1024
	}
	out := bufio.NewWriterSize(stdout, outSize)
	if *ab {
		var parts []string
		switch {
		case *amp != "":
			parts = append(parts, "ampli "+*amp)
		case *profile != "":
			parts = append(parts, *profile)
		}
		for _, e := range effects {
			parts = append(parts, e.spec)
		}
		label := strings.Join(parts, " + ")
		if err := runAB(out, stderr, stdin, process, *rate, *channels, *abSeconds, *abDelay, label, *loudness); err != nil {
			return err
		}
		return out.Flush()
	}
	if set["target-lufs"] {
		if err := runNormalize(out, stderr, stdin, process, *rate, *channels, audioprofile.NormalizeOptions{TargetLUFS: *targetLUFS, CeilingDB: *ceiling}, *loudness); err != nil {
			return err
		}
		return out.Flush()
	}
	var src io.Reader = stdin
	var dst io.Writer = out
	var inMeter, outMeter *audioprofile.LoudnessMeter
	if *loudness { // mesure au fil de l'eau, sans rien garder en mémoire
		inMeter = audioprofile.NewLoudnessMeter(*rate, *channels)
		outMeter = audioprofile.NewLoudnessMeter(*rate, *channels)
		src = io.TeeReader(stdin, inMeter)
		dst = io.MultiWriter(out, outMeter)
	}
	var proc io.Reader
	stopWatch := func() {}
	if *watch || *serve != "" {
		sw := audioprofile.NewSwitcher(src, *rate, *channels, process)
		if *watch {
			if stopWatch, err = startWatch(*configPath, sw, pl, stderr); err != nil {
				return err
			}
		} else {
			pn, err := newPanelBackend(cfg, pl, sw)
			if err != nil {
				return err
			}
			url, stopServe, err := panel.Serve(*serve, pn)
			if err != nil {
				return fmt.Errorf("-serve : %w", err)
			}
			fmt.Fprintf(stderr, "panneau de réglage : %s\n", url)
			stopWatch = stopServe
		}
		defer stopWatch()
		proc = sw
	} else {
		proc = process(src)
	}
	_, err = io.Copy(dst, proc)
	stopWatch() // avant d'écrire sur stderr : le goroutine de surveillance (ou le serveur) y écrit aussi
	if err != nil {
		return fmt.Errorf("traitement : %w", err)
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if *loudness {
		printLoudness(stderr, inMeter.Loudness(), outMeter.Loudness())
	}
	return nil
}

// runNormalize lit tout le flux, le traite, amène la sonie du résultat à la cible (avec un limiteur
// sur les crêtes) et écrit le tout. Le résumé va sur la sortie d'erreur.
func runNormalize(out io.Writer, stderr io.Writer, stdin io.Reader, process func(io.Reader) io.Reader, rate, channels int, opts audioprofile.NormalizeOptions, loudness bool) error {
	orig, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("lecture : %w", err)
	}
	orig = orig[:len(orig)-len(orig)%(channels*2)] // trames entières
	proc, err := io.ReadAll(process(bytes.NewReader(orig)))
	if err != nil {
		return fmt.Errorf("traitement : %w", err)
	}
	final, res, err := audioprofile.NormalizeLoudness(proc, rate, channels, opts)
	if err != nil {
		return fmt.Errorf("normalisation : %w", err)
	}
	switch {
	case !res.Before.Valid:
		fmt.Fprintf(stderr, "Normalisation : %s.\n", res.Reason)
	case res.Reached:
		fmt.Fprintf(stderr, "Normalisation : sonie intégrée %.1f → %.1f LUFS (cible %.1f), gain de %+.1f dB avant le limiteur à %.1f dBFS%s.\n",
			res.Before.IntegratedLUFS, res.After.IntegratedLUFS, opts.TargetLUFS, res.GainDB, opts.CeilingDB, limitedNote(res.Limited))
	default:
		fmt.Fprintf(stderr, "Normalisation : cible de %.1f LUFS non atteinte : %.1f LUFS obtenus, à partir de %.1f (%s).\n",
			opts.TargetLUFS, res.After.IntegratedLUFS, res.Before.IntegratedLUFS, res.Reason)
	}
	if loudness {
		lo, _ := audioprofile.MeasureLoudness(bytes.NewReader(orig), rate, channels)
		lf, _ := audioprofile.MeasureLoudness(bytes.NewReader(final), rate, channels)
		printLoudness(stderr, lo, lf)
	}
	_, err = out.Write(final)
	return err
}

func limitedNote(limited bool) string {
	if limited {
		return ", limiteur intervenu"
	}
	return ""
}

// printLoudness affiche la sonie de l'original et du traité, sur deux colonnes.
func printLoudness(w io.Writer, orig, proc audioprofile.Loudness) {
	num := func(ok bool, v float64) string {
		if !ok {
			return "—"
		}
		return fmt.Sprintf("%.1f", v)
	}
	peak := func(l audioprofile.Loudness) string { return num(l.PeakDBFS > -150, l.PeakDBFS) }
	fmt.Fprintf(w, "Sonie (EBU R128)             %10s %10s\n", "original", "traité")
	fmt.Fprintf(w, "  intégrée (LUFS)            %10s %10s\n", num(orig.Valid, orig.IntegratedLUFS), num(proc.Valid, proc.IntegratedLUFS))
	fmt.Fprintf(w, "  plage (LU)                 %10s %10s\n", num(orig.HasRange, orig.RangeLU), num(proc.HasRange, proc.RangeLU))
	fmt.Fprintf(w, "  crête d'échantillon (dBFS) %10s %10s\n", peak(orig), peak(proc))
	if !orig.Valid || !proc.Valid {
		fmt.Fprintln(w, "  (« — » : moins de 400 ms de signal audible, ou silence ; la plage demande en plus 3 s)")
	} else if !orig.HasRange || !proc.HasRange {
		fmt.Fprintln(w, "  (« — » : la plage de sonie demande au moins 3 s de signal audible)")
	}
}

// runAB lit tout le flux, le traite, égalise les niveaux de l'original et du traité, puis écrit
// un flux qui alterne l'un et l'autre (l'original en premier).
func runAB(out *bufio.Writer, stderr io.Writer, stdin io.Reader, process func(io.Reader) io.Reader, rate, channels int, seconds, delay float64, label string, loudness bool) error {
	fmt.Fprintln(stderr, "A/B : traitement du morceau en cours, la lecture commencera dans quelques secondes…")
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
	if loudness {
		lo, _ := audioprofile.MeasureLoudness(bytes.NewReader(orig), rate, channels)
		lp, _ := audioprofile.MeasureLoudness(bytes.NewReader(proc), rate, channels)
		printLoudness(stderr, lo, lp)
	}
	fmt.Fprintf(stderr, "A/B : A = original, B = traité (%s) ; segments de %g s : A de 0 à %g s, B de %g à %g s, etc.\n",
		label, seconds, seconds, seconds, 2*seconds)
	switch {
	case gOrig == 0 && gProc == 0:
		fmt.Fprintln(stderr, "A/B : sonies déjà égales (ou signal silencieux), aucun gain appliqué.")
	case gOrig < 0:
		fmt.Fprintf(stderr, "A/B : l'original est plus fort que le traité : gain de %.1f dB appliqué à A pour égaliser la sonie (le traité n'est pas modifié).\n", gOrig)
	default:
		fmt.Fprintf(stderr, "A/B : le traité est plus fort que l'original : gain de %.1f dB appliqué à B pour égaliser la sonie (l'original n'est pas modifié).\n", gProc)
	}
	mixed := audioprofile.Alternate(audioprofile.ApplyGain(orig, gOrig), audioprofile.ApplyGain(proc, gProc), rate, channels, seconds)
	return playAB(out, stderr, mixed, rate, channels, seconds, delay, label)
}

// playAB écrit le flux alterné segment par segment et annonce sur la sortie d'erreur la source
// en cours (A ou B). Chaque annonce part juste avant l'écriture du segment, et chaque segment est
// vidé avant le suivant : si la sortie est un pipe vers un lecteur, l'écriture se bloque quand le
// pipe est plein, ce qui cale l'affichage sur la lecture, à la mémoire tampon du lecteur près
// (ffplay lit environ 1 s d'avance : -ab-delay retarde l'affichage d'autant).
func playAB(out *bufio.Writer, stderr io.Writer, mixed []byte, rate, channels int, seconds, delay float64, label string) error {
	step := channels * 2
	segBytes := audioprofile.SegmentFrames(rate, seconds) * step
	color := isTerminal(stderr) && os.Getenv("NO_COLOR") == ""
	announce := func(k int, at float64) {
		name, text, code := "A", "original", "1;36"
		if k%2 == 1 {
			name, text, code = "B", "traité ("+label+")", "1;33"
		}
		line := fmt.Sprintf("[%d:%04.1f] %s  %s", int(at)/60, at-60*float64(int(at)/60), name, text)
		if color {
			line = "\x1b[" + code + "m" + line + "\x1b[0m"
		}
		fmt.Fprintln(stderr, line)
	}
	// Les annonces retardées passent par une seule goroutine, dans l'ordre : deux annonces proches
	// ne peuvent pas se doubler.
	type due struct {
		when time.Time
		k    int
		at   float64
	}
	var queue chan due
	var done sync.WaitGroup
	if delay > 0 {
		queue = make(chan due, len(mixed)/segBytes+2)
		done.Add(1)
		go func() {
			defer done.Done()
			for d := range queue {
				time.Sleep(time.Until(d.when))
				announce(d.k, d.at)
			}
		}()
		defer func() {
			close(queue)
			done.Wait()
		}()
	}
	for k, start := 0, 0; start < len(mixed); k, start = k+1, start+segBytes {
		end := start + segBytes
		if end > len(mixed) {
			end = len(mixed)
		}
		at := float64(start/step) / float64(rate)
		if delay > 0 {
			queue <- due{when: time.Now().Add(time.Duration(delay * float64(time.Second))), k: k, at: at}
		} else {
			announce(k, at)
		}
		if _, err := out.Write(mixed[start:end]); err != nil {
			return err
		}
		if err := out.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// isTerminal indique si r est un terminal (et non un pipe ou un fichier).
func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// /dev/null est un périphérique caractère, mais pas un terminal : on peut y envoyer la sortie
	// (utile pour ne garder que la mesure : ... | audioprofile -profile flat -loudness > /dev/null)
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}
