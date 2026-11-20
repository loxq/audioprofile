package bassboost

import "io"

const (
	midCenterHz = 1000.0 // fréquence centrale de la cloche des médiums
	midQ        = 0.7    // facteur de qualité : cloche large (~2 octaves)
)

// MidBoost renvoie un flux PCM (s16le, entrelacé) dont les fréquences
// intermédiaires sont renforcées ou atténuées (filtre en cloche / peaking
// centré à 1 kHz, ±15 dB).
//
// Les paramètres sont identiques à ceux de BassBoost :
// intensity va de -1.0 (médiums −15 dB) à +1.0 (médiums +15 dB), 0.0 = aucun effet.
func MidBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader {
	return singleBand(src, sampleRate, channels, intensity, Band{Type: Peaking, FreqHz: midCenterHz, Q: midQ})
}
