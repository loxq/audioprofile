package audioprofile_test

import (
	"bytes"
	"fmt"
	"io"
	"math"

	"github.com/loxq/audioprofile"
)

// Les exemples travaillent sur des signaux synthétiques : tone(freq, amp, secondes, canaux) est une sinusoïde
// d'amplitude amp (sur 32767) à 44,1 kHz. Dans un vrai programme, src est le flux PCM de votre application.

func Example() {
	src := bytes.NewReader(tone(1000, 30000, 1, 2)) // crête à −0,8 dBFS

	// Chaque brique enveloppe la précédente : rien n'est calculé avant qu'on lise r.
	var r io.Reader = src
	r = audioprofile.HighPassFilter(r, 44100, 2, 80, 2) // coupe sous 80 Hz
	r = audioprofile.Compressor(r, 44100, 2, audioprofile.CompressorSettings{ThresholdDB: -18, Ratio: 3, AttackMs: 10, ReleaseMs: 120, KneeDB: 6})
	r = audioprofile.Limiter(r, 44100, 2, audioprofile.LimiterSettings{CeilingDB: -3}) // crêtes sous −3 dBFS

	out, _ := io.ReadAll(r)
	fmt.Println(len(out), "octets en sortie")
	fmt.Println("crêtes sous -3 dBFS :", peak(out) <= ceiling(-3))
	// Output:
	// 176400 octets en sortie
	// crêtes sous -3 dBFS : true
}

func ExampleApplyProfile() {
	in := tone(60, 8000, 1, 2)

	// le profil Rock à 80 % de son intensité (0 = aucun effet, 1 = complet, jusqu'à 3 pour l'exagérer)
	out, _ := io.ReadAll(audioprofile.ApplyProfile(bytes.NewReader(in), 44100, 2, audioprofile.Rock, 0.8))

	fmt.Println("même longueur :", len(out) == len(in))
	fmt.Println("signal modifié :", !bytes.Equal(out, in))
	// Output:
	// même longueur : true
	// signal modifié : true
}

func ExampleParseProfile() {
	p, ok := audioprofile.ParseProfile("Rock") // la casse est ignorée
	fmt.Println(p, ok)

	_, ok = audioprofile.ParseProfile("metal")
	fmt.Println(ok)

	for _, p := range audioprofile.Profiles()[:3] {
		fmt.Println(p)
	}
	// Output:
	// rock true
	// false
	// flat
	// rock
	// pop
}

func ExampleProfile_Settings() {
	// les réglages complets d'un profil, en copie : on les modifie à volonté
	s := audioprofile.Rock.Settings()
	fmt.Println(len(s.Bands), "bandes, compresseur :", s.Compression != nil)

	s.Compression = nil                                      // sans compresseur
	s.Bands[0].GainDB = 8                                    // et des basses plus fortes
	s.Limiter = &audioprofile.LimiterSettings{CeilingDB: -1} // avec un limiteur

	r := audioprofile.ApplyProfileSettings(bytes.NewReader(tone(60, 8000, 1, 2)), 44100, 2, s, 1)
	out, _ := io.ReadAll(r)
	fmt.Println(len(out), "octets")
	// Output:
	// 4 bandes, compresseur : true
	// 176400 octets
}

func ExampleEqualizer() {
	// une cloche de +6 dB à 1 kHz : le gain demandé est atteint exactement à la fréquence centrale
	bands := []audioprofile.Band{{Type: audioprofile.Peaking, FreqHz: 1000, GainDB: 6, Q: 1}}

	in := tone(1000, 8000, 1, 1)
	out, _ := io.ReadAll(audioprofile.Equalizer(bytes.NewReader(in), 44100, 1, bands))
	fmt.Printf("%+.0f dB à 1 kHz\n", levelDB(out, 1)-levelDB(in, 1))
	// Output: +6 dB à 1 kHz
}

func ExampleHighPassFilter() {
	// Butterworth d'ordre 2 à 1 kHz : 12 dB par octave, donc −24 dB deux octaves plus bas
	in := tone(250, 8000, 1, 1)
	out, _ := io.ReadAll(audioprofile.HighPassFilter(bytes.NewReader(in), 44100, 1, 1000, 2))
	fmt.Printf("%.0f dB à 250 Hz\n", levelDB(out, 1)-levelDB(in, 1))
	// Output: -24 dB à 250 Hz
}

func ExampleCompressor() {
	in := tone(1000, 16000, 1, 1)
	out, _ := io.ReadAll(audioprofile.Compressor(bytes.NewReader(in), 44100, 1, audioprofile.CompressorSettings{
		ThresholdDB: -20, // au-delà de ce niveau, le signal est réduit
		Ratio:       4,   // 4:1
		AttackMs:    1,
		ReleaseMs:   50,
	}))
	fmt.Printf("réduction : %.0f dB\n", levelDB(in, 1)-levelDB(out, 1))
	// Output: réduction : 10 dB
}

func ExampleLimiter() {
	in := tone(1000, 30000, 1, 2) // crête à −0,8 dBFS
	out, _ := io.ReadAll(audioprofile.Limiter(bytes.NewReader(in), 44100, 2, audioprofile.LimiterSettings{CeilingDB: -3}))

	// aucune crête au-dessus du plafond, et la sortie est recalée sur l'entrée (même longueur)
	fmt.Println(peak(out) <= ceiling(-3), len(out) == len(in))
	// Output: true true
}

func ExampleTubeAmp() {
	// le préréglage « crunch », fait pour un morceau complet : le niveau reste proche de l'entrée
	in := tone(200, 8000, 1, 1)
	s := audioprofile.AmpCrunch.Settings().Scaled(1.5) // 1,5 = plus marqué
	out, _ := io.ReadAll(audioprofile.TubeAmp(bytes.NewReader(in), 44100, 1, s))

	fmt.Println("signal saturé :", !bytes.Equal(out, in))
	fmt.Println("niveau à 1 dB près :", math.Abs(levelDB(out, 1)-levelDB(in, 1)) < 1)
	// Output:
	// signal saturé : true
	// niveau à 1 dB près : true
}

func ExampleDelay() {
	// une impulsion d'amplitude 20000 dans un flux stéréo
	in := make([]byte, 2*44100*4)
	in[0], in[1] = 0x20, 0x4e

	out, _ := io.ReadAll(audioprofile.Delay(bytes.NewReader(in), 44100, 2, audioprofile.DelaySettings{
		TimeMs: 100, Feedback: 0.5, Mix: 0.5,
	}))

	// le signal sec, puis un écho toutes les 100 ms (4410 trames), deux fois plus faible à chaque fois
	fmt.Println(frame(out, 0), frame(out, 4410), frame(out, 8820))
	// Output: 20000 10000 5000
}

func ExampleMeasureLoudness() {
	// une sinusoïde stéréo de 10 s, crête à −20 dBFS
	l, err := audioprofile.MeasureLoudness(bytes.NewReader(tone(1000, 3277, 10, 2)), 44100, 2)
	fmt.Printf("%.1f LUFS, valide : %v, erreur : %v\n", l.IntegratedLUFS, l.Valid, err)
	// Output: -20.0 LUFS, valide : true, erreur : <nil>
}

func ExampleNormalizeLoudness() {
	in := tone(1000, 3277, 10, 2) // −20 LUFS

	// −23 LUFS (diffusion EBU R128), crêtes sous −1 dBFS
	out, res, err := audioprofile.NormalizeLoudness(in, 44100, 2, audioprofile.NormalizeOptions{TargetLUFS: -23, CeilingDB: -1})
	fmt.Printf("avant %.1f LUFS, après %.1f LUFS, gain %.1f dB, atteint : %v, erreur : %v\n",
		res.Before.IntegratedLUFS, res.After.IntegratedLUFS, res.GainDB, res.Reached, err)
	fmt.Println(len(out) == len(in))
	// Output:
	// avant -20.0 LUFS, après -23.0 LUFS, gain -3.0 dB, atteint : true, erreur : <nil>
	// true
}

func ExampleMatchGains() {
	original := tone(1000, 8000, 3, 1)
	louder, _ := io.ReadAll(audioprofile.Equalizer(bytes.NewReader(original), 44100, 1,
		[]audioprofile.Band{{Type: audioprofile.Gain, GainDB: 6}})) // un traitement qui monte le niveau de 6 dB

	// pour comparer à sonie égale, on baisse le plus fort : on ne relève jamais rien
	origDB, procDB := audioprofile.MatchGains(original, louder, 44100, 1)
	fmt.Printf("original %.1f dB, traité %.1f dB\n", origDB, procDB)

	// un vrai A/B : les deux sources en alternance toutes les secondes, sans interrompre la musique
	ab := audioprofile.Alternate(audioprofile.ApplyGain(original, origDB), audioprofile.ApplyGain(louder, procDB), 44100, 1, 1)
	fmt.Println(len(ab) == len(original))
	// Output:
	// original 0.0 dB, traité -6.0 dB
	// true
}

func ExampleNewSwitcher() {
	// la chaîne : un simple gain
	gain := func(db float64) func(io.Reader) io.Reader {
		return func(r io.Reader) io.Reader {
			return audioprofile.Equalizer(r, 44100, 1, []audioprofile.Band{{Type: audioprofile.Gain, GainDB: db}})
		}
	}
	in := tone(440, 8000, 4, 1)
	sw := audioprofile.NewSwitcher(bytes.NewReader(in), 44100, 1, gain(-12))

	// on lit deux secondes, puis on change de chaîne : le reste sort avec le nouveau réglage, sans clic
	head := make([]byte, 2*44100*2)
	io.ReadFull(sw, head)
	sw.Swap(gain(-3)) // sûr depuis n'importe quel goroutine
	rest, _ := io.ReadAll(sw)
	out := append(head, rest...)

	fmt.Printf("%.0f dB avant, %.0f dB après, %d octets\n",
		levelDB(out[:2*44100], 1)-levelDB(in[:2*44100], 1),     // la première seconde
		levelDB(out[3*2*44100:], 1)-levelDB(in[3*2*44100:], 1), // la dernière
		len(out))
	// Output: -12 dB avant, -3 dB après, 352800 octets
}
