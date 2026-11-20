package bassboost

import (
	"io"
	"math"
)

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
	return newFilterReader(src, sampleRate, channels, intensity, midCenterHz, peaking)
}

// peaking calcule un filtre en cloche (RBJ Audio EQ Cookbook, peakingEQ).
// Le gain gainDB est atteint à la fréquence centrale f0 ; les extrémités du
// spectre restent inchangées.
func peaking(fs, f0, gainDB float64) coeffs {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * f0 / fs
	cosw, sinw := math.Cos(w0), math.Sin(w0)
	alpha := sinw / (2 * midQ)

	a0 := 1 + alpha/A
	return coeffs{
		b0: (1 + alpha*A) / a0,
		b1: -2 * cosw / a0,
		b2: (1 - alpha*A) / a0,
		a1: -2 * cosw / a0,
		a2: (1 - alpha/A) / a0,
	}
}
