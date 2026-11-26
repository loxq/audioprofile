package audioprofile

import (
	"io"
	"math"
)

// ParallelCompressionSettings règle la compression parallèle.
type ParallelCompressionSettings struct {
	Compressor CompressorSettings // le compresseur, en général très marqué (seuil bas, rapport élevé, compensation forte)
	Mix        float64            // part du signal comprimé, de 0 (son sec, sans effet) à 1 (entièrement comprimé)
}

// ParallelCompressor renvoie un flux PCM (s16le, entrelacé) où un signal très comprimé est mélangé au
// signal d'origine (compression « parallèle » ou « New York »).
//
// Un compresseur ordinaire écrase les crêtes pour rapprocher les passages forts des faibles. Ici, le
// signal passe aussi par un compresseur très marqué, dont la compensation relève fortement les passages
// faibles, puis les deux sont mélangés : sortie = (1 − Mix) × sec + Mix × comprimé. Les passages faibles,
// remontés par le comprimé, gagnent en densité et en détail ; les passages forts, que le compresseur a
// aplatis, restent dominés par le sec et gardent leurs transitoires. Le sec et le comprimé étant presque
// identiques en forme (c'est le même signal, à un gain près), le mélange est linéaire.
//
// Le compresseur est celui de Compressor (détection liée sur le canal le plus fort, même gain pour tous
// les canaux). Si Mix est nul, si le compresseur est sans effet (rapport <= 1 et compensation nulle), ou si
// sampleRate <= 0, le flux est renvoyé tel quel. Mix est ramené dans [0, 1] (NaN compte pour 0).
// Un plafond doux (genou à 0,8) arrondit les pics qui dépasseraient la pleine échelle, car la compensation
// relève le niveau.
func ParallelCompressor(src io.Reader, sampleRate, channels int, s ParallelCompressionSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{effects: []effectSpec{s}})
}

// stage construit l'étage de compression parallèle (nil s'il est sans effet).
func (s ParallelCompressionSettings) stage(fs float64, channels int) frameStage {
	mix := s.Mix
	if math.IsNaN(mix) {
		mix = 0
	}
	mix = math.Max(0, math.Min(1, mix))
	c := newCompressor(fs, &s.Compressor)
	if mix == 0 || c == nil || channels < 1 {
		return nil
	}
	return &parallelComp{c: c, mix: mix, wet: make([]float64, channels)}
}

type parallelComp struct {
	c   *compressor
	mix float64
	wet []float64
}

func (p *parallelComp) apply(frame []float64) {
	copy(p.wet, frame)
	p.c.apply(p.wet)
	for i := range frame {
		frame[i] = frame[i]*(1-p.mix) + p.wet[i]*p.mix
	}
}
