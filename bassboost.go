// Package audioprofile applique des filtres d'égalisation (basses, médiums,
// aigus, ou égaliseur paramétrique complet) sur un flux PCM 16 bits signé
// little-endian, entrelacé.
package audioprofile

import "io"

const (
	cutoffHz  = 120.0 // fréquence de coupure du shelf des basses
	maxGainDB = 15.0  // gain à |intensity| = 1 pour BassBoost, MidBoost, TrebleBoost
)

// BassBoost renvoie un flux PCM (s16le, entrelacé) dont les basses sont
// renforcées (low-shelf à 120 Hz). Le traitement se fait au fil de l'eau,
// sans tout charger en mémoire.
//
//   - src        : flux PCM d'entrée (16 bits signé little-endian)
//   - sampleRate : fréquence d'échantillonnage en Hz (ex. 44100)
//   - channels   : nombre de canaux (1 = mono, 2 = stéréo...)
//   - intensity  : intensité de l'effet de -1.0 (basses atténuées de 15 dB)
//     à +1.0 (basses renforcées de 15 dB), 0.0 = aucun effet.
//     Les valeurs hors de cet intervalle sont ramenées dans [-1, 1].
func BassBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader {
	return singleBand(src, sampleRate, channels, intensity, Band{Type: LowShelf, FreqHz: cutoffHz})
}
