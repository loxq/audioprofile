package audioprofile

import (
	"bytes"
	"math"
)

const (
	// abCrossfadeMs est la durée du fondu enchaîné entre deux segments d'Alternate :
	// assez long pour éviter un clic (les deux flux n'ont pas la même phase), assez court
	// pour ne pas gêner la comparaison.
	abCrossfadeMs = 15.0

	levelWindowMs  = 100.0 // fenêtre de mesure du niveau
	levelGateDBFS  = -60.0 // les fenêtres plus faibles (silence numérique, fin de fondu) ne comptent pas
	silenceLevelDB = -200.0
)

// matchLevels donne le niveau (en dB) de chacun des deux signaux, par la même mesure : la
// sonie intégrée en LUFS (voir MeasureLoudness) quand les deux ont au moins 400 ms de signal
// audible, sinon le niveau efficace (rmsLevelDB), qui marche aussi sur un extrait très court.
// Un signal silencieux donne silenceLevelDB.
func matchLevels(original, processed []byte, sampleRate, channels int) (lo, lp float64) {
	a, errA := MeasureLoudness(bytes.NewReader(original), sampleRate, channels)
	b, errB := MeasureLoudness(bytes.NewReader(processed), sampleRate, channels)
	if errA == nil && errB == nil && a.Valid && b.Valid {
		return a.IntegratedLUFS, b.IntegratedLUFS
	}
	return rmsLevelDB(original, sampleRate, channels), rmsLevelDB(processed, sampleRate, channels)
}

// rmsLevelDB est le niveau efficace (RMS, en dBFS) de la moyenne des fenêtres de
// 100 ms qui dépassent −60 dBFS.
func rmsLevelDB(pcm []byte, sampleRate, channels int) float64 {
	if sampleRate <= 0 || channels < 1 {
		return silenceLevelDB
	}
	win := int(levelWindowMs / 1000 * float64(sampleRate))
	if win < 1 {
		win = 1
	}
	step := channels * bytesPerSm
	frames := len(pcm) / step
	var sum float64
	var kept int
	for start := 0; start < frames; start += win {
		end := start + win
		if end > frames {
			end = frames
		}
		var e float64
		for i := start * channels; i < end*channels; i++ {
			v := float64(int16(uint16(pcm[2*i]) | uint16(pcm[2*i+1])<<8))
			e += v * v
		}
		n := (end - start) * channels
		if 10*math.Log10(e/float64(n)/(32768*32768)+1e-30) < levelGateDBFS {
			continue
		}
		sum += e
		kept += n
	}
	if kept == 0 {
		return silenceLevelDB
	}
	return 10 * math.Log10(sum/float64(kept)/(32768*32768))
}

// MatchGains renvoie les gains (en dB, <= 0) à appliquer à l'original et au signal traité
// pour qu'ils aient la même sonie (LUFS intégrés, pondération K de l'UIT-R BS.1770, qui suit
// mieux ce qu'on entend que le niveau efficace). On atténue toujours le plus fort et on ne
// relève jamais l'autre : aucun risque d'écrêtage. Les signaux silencieux ne reçoivent aucun
// gain. Sur un extrait de moins de 400 ms, qui n'a pas de sonie intégrée, la mesure retombe
// sur le niveau efficace.
//
// Comparer deux sons à des niveaux différents est trompeur : le plus fort paraît toujours
// « meilleur ». C'est pourquoi la commande -ab égalise les niveaux avant d'alterner.
func MatchGains(original, processed []byte, sampleRate, channels int) (originalDB, processedDB float64) {
	lo, lp := matchLevels(original, processed, sampleRate, channels)
	if lo <= silenceLevelDB || lp <= silenceLevelDB {
		return 0, 0
	}
	if lo > lp {
		return lp - lo, 0
	}
	return 0, lo - lp
}

// ApplyGain renvoie une copie du signal PCM (s16le) multipliée par gainDB, arrondie et
// écrêtée à l'échelle 16 bits. Un gain de 0 dB renvoie le signal à l'identique.
func ApplyGain(pcm []byte, gainDB float64) []byte {
	out := make([]byte, len(pcm)-len(pcm)%bytesPerSm)
	if gainDB == 0 || math.IsNaN(gainDB) {
		copy(out, pcm)
		return out
	}
	g := math.Pow(10, gainDB/20)
	for i := 0; i+1 < len(out); i += 2 {
		v := float64(int16(uint16(pcm[i])|uint16(pcm[i+1])<<8)) * g
		v = math.Max(-32768, math.Min(32767, math.Round(v)))
		u := uint16(int16(v))
		out[i], out[i+1] = byte(u), byte(u>>8)
	}
	return out
}

// SegmentFrames renvoie la longueur, en trames, des segments d'Alternate pour segmentSeconds
// (au moins 1), ou 0 si sampleRate ou segmentSeconds est invalide. Elle permet à l'appelant de
// savoir où tombent les changements de source, par exemple pour les annoncer.
func SegmentFrames(sampleRate int, segmentSeconds float64) int {
	if sampleRate <= 0 || !(segmentSeconds > 0) || math.IsInf(segmentSeconds, 0) {
		return 0
	}
	seg := int(math.Round(segmentSeconds * float64(sampleRate)))
	if seg < 1 {
		seg = 1
	}
	return seg
}

// Alternate renvoie un flux PCM (s16le, entrelacé) qui alterne entre a et b toutes les
// segmentSeconds secondes, en commençant par a : a pendant le premier segment, b pendant le
// deuxième, etc. C'est un vrai A/B : les deux flux sont lus à la même position, la musique
// ne s'interrompt pas, seule la source change.
//
// Chaque changement est un fondu enchaîné linéaire de 15 ms (gain constant : a et b sont la
// même musique, un fondu à puissance constante ajouterait 3 dB), ce qui évite le clic.
//
// La sortie a la longueur du plus court des deux flux, arrondie à une trame entière. Elle
// renvoie a tel quel si sampleRate <= 0, channels < 1 ou segmentSeconds <= 0.
func Alternate(a, b []byte, sampleRate, channels int, segmentSeconds float64) []byte {
	step := channels * bytesPerSm
	if sampleRate <= 0 || channels < 1 || !(segmentSeconds > 0) || math.IsInf(segmentSeconds, 0) {
		return a
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	frames := n / step
	seg := SegmentFrames(sampleRate, segmentSeconds)
	fade := int(math.Round(abCrossfadeMs / 1000 * float64(sampleRate)))
	if fade > seg {
		fade = seg // un fondu ne déborde pas sur le segment suivant
	}
	out := make([]byte, frames*step)
	src := [2][]byte{a, b}
	half := fade / 2
	for i := 0; i < frames; i++ {
		// Fondu enchaîné centré sur chaque frontière de segment m*seg (m >= 1) : de
		// m*seg - fade/2 (inclus) à m*seg + fade - fade/2 (exclu). Les fenêtres ne se
		// chevauchent pas, puisque fade <= seg.
		cur, w := (i/seg)%2, 1.0 // source courante et son poids
		if m := (i + half) / seg; m >= 1 && fade > 0 {
			if offset := i - m*seg; offset < fade-half {
				cur = m % 2
				w = (float64(offset+half) + 0.5) / float64(fade) // 0 → 1 : de l'ancienne source à la nouvelle
			}
		}
		for c := 0; c < channels; c++ {
			j := i*step + c*bytesPerSm
			x := float64(int16(uint16(src[cur][j]) | uint16(src[cur][j+1])<<8))
			if w != 1 {
				y := float64(int16(uint16(src[1-cur][j]) | uint16(src[1-cur][j+1])<<8))
				x = x*w + y*(1-w)
			}
			u := uint16(int16(math.Round(x)))
			out[j], out[j+1] = byte(u), byte(u>>8)
		}
	}
	return out
}
