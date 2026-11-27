package audioprofile_test

import "math"

// tone renvoie une sinusoïde PCM s16le de la durée donnée, à 44,1 kHz, la même sur chaque canal.
func tone(freq, amp, seconds float64, channels int) []byte {
	frames := int(seconds * 44100)
	b := make([]byte, 0, frames*channels*2)
	for i := 0; i < frames; i++ {
		v := int16(math.Round(amp * math.Sin(2*math.Pi*freq*float64(i)/44100)))
		for c := 0; c < channels; c++ {
			b = append(b, byte(uint16(v)), byte(uint16(v)>>8))
		}
	}
	return b
}

// sample lit l'échantillon i d'un flux PCM s16le.
func sample(pcm []byte, i int) float64 {
	return float64(int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8))
}

// frame lit la valeur du premier canal à la trame i d'un flux stéréo.
func frame(pcm []byte, i int) float64 { return sample(pcm, 2*i) }

// peak renvoie la plus grande valeur absolue d'échantillon.
func peak(pcm []byte) float64 {
	m := 0.0
	for i := 0; i < len(pcm)/2; i++ {
		m = math.Max(m, math.Abs(sample(pcm, i)))
	}
	return m
}

// levelDB renvoie le niveau efficace (dBFS) de la seconde moitié d'un flux (on laisse passer le régime transitoire).
// channels est le nombre de canaux du flux.
func levelDB(pcm []byte, channels int) float64 {
	n := len(pcm) / 2
	var sum float64
	count := 0
	for i := n / 2; i < n; i++ {
		v := sample(pcm, i)
		sum += v * v
		count++
	}
	return 10 * math.Log10(sum/float64(count)/32768/32768)
}

// ceiling renvoie la valeur d'échantillon qu'un plafond en dBFS ne doit pas dépasser : les échantillons sont des
// entiers, le signal est arrondi, d'où la demi-valeur de tolérance.
func ceiling(dB float64) float64 { return 32768*math.Pow(10, dB/20) + 0.5 }
