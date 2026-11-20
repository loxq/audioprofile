package bassboost

import (
	"io"
	"math"
)

const trebleCutoffHz = 4000.0 // fréquence de coupure du shelf des aigus

// TrebleBoost renvoie un flux PCM (s16le, entrelacé) dont les aigus sont
// renforcés ou atténués (filtre high-shelf à 4 kHz, ±15 dB).
//
// Les paramètres sont identiques à ceux de BassBoost :
// intensity va de -1.0 (aigus −15 dB) à +1.0 (aigus +15 dB), 0.0 = aucun effet.
// Si la fréquence de coupure dépasse la fréquence de Nyquist
// (sampleRate < 8000 Hz), le flux est renvoyé tel quel.
func TrebleBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader {
	return newFilterReader(src, sampleRate, channels, intensity, trebleCutoffHz, highShelf)
}

// highShelf calcule un filtre high-shelf (RBJ Audio EQ Cookbook, pente S = 1).
func highShelf(fs, f0, gainDB float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / 2 * math.Sqrt2
	tsa := 2 * math.Sqrt(A) * alpha

	a0 := (A + 1) - (A-1)*cosw + tsa
	return coeffs{
		b0: A * ((A + 1) + (A-1)*cosw + tsa) / a0,
		b1: -2 * A * ((A - 1) + (A+1)*cosw) / a0,
		b2: A * ((A + 1) + (A-1)*cosw - tsa) / a0,
		a1: 2 * ((A - 1) - (A+1)*cosw) / a0,
		a2: ((A + 1) - (A-1)*cosw - tsa) / a0,
	}
}
