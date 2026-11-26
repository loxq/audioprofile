package audioprofile

import (
	"io"
	"math"
)

// DeEsserSettings règle le de-esser.
type DeEsserSettings struct {
	FreqHz      float64 // fréquence de séparation : tout ce qui est au-dessus est surveillé ; <= 0 → 5000
	ThresholdDB float64 // seuil en dBFS, mesuré sur la bande haute, au-delà duquel elle est réduite
	Ratio       float64 // rapport de compression de la bande haute (par exemple 4 = 4:1) ; <= 1 : sans effet
}

const (
	defaultDeEsserHz = 5000.0
	deEsserAttackMs  = 0.5  // les sifflantes démarrent d'un coup
	deEsserReleaseMs = 40.0 // et ne durent qu'une centaine de millisecondes
	deEsserKneeDB    = 3.0
)

// DeEsser renvoie un flux PCM (s16le, entrelacé) dont les sifflantes (les « s » et les « ch » d'une
// voix, vers 5 à 8 kHz) sont atténuées quand elles ressortent trop, sans toucher au reste du son.
//
// Le spectre est coupé en deux à FreqHz par un filtre de Linkwitz-Riley d'ordre 4 (comme
// MultibandCompressor, dont c'est un cas à deux bandes) : la bande basse passe intacte, la bande
// haute traverse un compresseur à attaque rapide (0,5 ms) et relâchement de 40 ms. Tant que la
// bande haute reste sous le seuil, elle n'est pas touchée.
//
// Limites : le seuil est **absolu** (en dBFS) : il dépend du niveau de l'enregistrement, et
// il faut le régler pour chaque source. La bande haute contient tout ce qui dépasse FreqHz (les cymbales
// comme les sifflantes) : une cymbale forte est donc atténuée aussi. La détection est liée (le même
// gain pour tous les canaux). Même sous le seuil, le flux n'est pas rendu à l'identique : la somme des
// deux bandes a un module plat à ±0,1 dB, mais sa phase tourne.
//
// Le flux est renvoyé tel quel si Ratio <= 1, si sampleRate <= 0 ou si FreqHz est >= la fréquence
// de Nyquist.
func DeEsser(src io.Reader, sampleRate, channels int, s DeEsserSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage du de-esser (nil s'il est sans effet).
func (s DeEsserSettings) stage(fs float64, channels int) frameStage {
	f := s.FreqHz
	if f <= 0 {
		f = defaultDeEsserHz
	}
	if math.IsNaN(f) || math.IsNaN(s.ThresholdDB) || !(s.Ratio > 1) || fs <= 0 || f >= fs/2 {
		return nil
	}
	m := newMultiband(fs, channels, &MultibandSettings{
		Crossovers: []float64{f},
		Bands: []CompressorSettings{
			{}, // la bande basse n'est pas touchée
			{ThresholdDB: s.ThresholdDB, Ratio: s.Ratio, AttackMs: deEsserAttackMs, ReleaseMs: deEsserReleaseMs, KneeDB: deEsserKneeDB},
		},
	})
	if m == nil {
		return nil
	}
	return m
}
