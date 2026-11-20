package audioprofile

import "io"

const trebleCutoffHz = 4000.0 // fréquence de coupure du shelf des aigus

// TrebleBoost renvoie un flux PCM (s16le, entrelacé) dont les aigus sont
// renforcés ou atténués (filtre high-shelf à 4 kHz, ±15 dB).
//
// Les paramètres sont identiques à ceux de BassBoost :
// intensity va de -1.0 (aigus −15 dB) à +1.0 (aigus +15 dB), 0.0 = aucun effet.
// Si la fréquence de coupure dépasse la fréquence de Nyquist
// (sampleRate < 8000 Hz), le flux est renvoyé tel quel.
func TrebleBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader {
	return singleBand(src, sampleRate, channels, intensity, Band{Type: HighShelf, FreqHz: trebleCutoffHz})
}
