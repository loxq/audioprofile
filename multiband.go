package audioprofile

import (
	"io"
	"math"
)

// MultibandSettings décrit un compresseur multibande.
type MultibandSettings struct {
	// Crossovers donne les fréquences de séparation en Hz, strictement croissantes :
	// n fréquences découpent le spectre en n+1 bandes.
	Crossovers []float64
	// Bands donne le compresseur de chaque bande, de la plus grave à la plus aiguë :
	// len(Bands) doit valoir len(Crossovers)+1. Un compresseur sans effet (ratio ≤ 1 et
	// compensation nulle) laisse sa bande intacte.
	Bands []CompressorSettings
}

// MultibandCompressor renvoie un flux PCM (s16le, entrelacé) dont chaque bande
// de fréquences est compressée séparément : les basses fortes ne font plus
// « pomper » les aigus, comme avec un compresseur large bande.
//
// Le spectre est découpé par des filtres de Linkwitz-Riley d'ordre 4 (deux
// Butterworth en cascade de chaque côté, −6 dB à la fréquence de séparation).
// Avec trois séparations ou plus, les bandes graves traversent en plus un filtre
// passe-tout aux séparations plus hautes, de sorte que la somme des bandes garde
// un module plat : sans compression, la réponse est plate à ±0,1 dB (seule la
// phase tourne). Les filtres sont à phase minimale, pas à phase linéaire.
//
// Dans chaque bande, le niveau est détecté sur le canal le plus fort et le même
// gain est appliqué à tous les canaux (détection liée, comme Compressor).
//
// Le flux est renvoyé tel quel si sampleRate <= 0, si len(Bands) != len(Crossovers)+1,
// si les séparations ne sont pas strictement croissantes et comprises entre 0 et
// la fréquence de Nyquist, ou si aucune bande n'a d'effet.
func MultibandCompressor(src io.Reader, sampleRate, channels int, s MultibandSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{multi: &s})
}

// lr4 est un filtre de Linkwitz-Riley d'ordre 4 : deux Butterworth d'ordre 2 en cascade.
type lr4 [2]biquad

func newLR4(c coeffs) lr4 { return lr4{{coef: c}, {coef: c}} }

func (f *lr4) process(x float64) float64 { return f[1].process(f[0].process(x)) }

// bandSplitter découpe un canal en bandes et les recompose.
type bandSplitter struct {
	lp, hp []lr4   // séparation à chaque fréquence : lp[j] garde la bande j, hp[j] passe au reste
	ap     [][]lr4 // ap[i][k] : moitié basse du passe-tout appliqué à la bande i à la séparation i+1+k
	aph    [][]lr4 // ... et sa moitié haute (basse + haute = passe-tout)
}

// multiband compresse chaque bande d'un flux multicanal.
type multiband struct {
	split []bandSplitter // un par canal
	comps []*compressor  // un par bande (nil = bande intacte)
	sig   [][]float64    // sig[bande][canal] : tampon de travail
}

// newMultiband renvoie nil si la configuration est invalide ou sans effet.
func newMultiband(fs float64, channels int, s *MultibandSettings) *multiband {
	if s == nil || fs <= 0 || channels < 1 || len(s.Bands) != len(s.Crossovers)+1 || len(s.Bands) < 2 {
		return nil
	}
	prev := 0.0
	for _, f := range s.Crossovers {
		if math.IsNaN(f) || f <= prev || f >= fs/2 {
			return nil
		}
		prev = f
	}
	nb := len(s.Bands)
	m := &multiband{comps: make([]*compressor, nb), sig: make([][]float64, nb)}
	active := false
	for b := range s.Bands {
		m.comps[b] = newCompressor(fs, &s.Bands[b])
		active = active || m.comps[b] != nil
		m.sig[b] = make([]float64, channels)
	}
	if !active {
		return nil
	}
	n := nb - 1
	m.split = make([]bandSplitter, channels)
	for ch := range m.split {
		sp := &m.split[ch]
		for _, f := range s.Crossovers {
			sp.lp = append(sp.lp, newLR4(lowPass(fs, f, defaultQ)))
			sp.hp = append(sp.hp, newLR4(highPass(fs, f, defaultQ)))
		}
		sp.ap = make([][]lr4, n)
		sp.aph = make([][]lr4, n)
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				sp.ap[i] = append(sp.ap[i], newLR4(lowPass(fs, s.Crossovers[j], defaultQ)))
				sp.aph[i] = append(sp.aph[i], newLR4(highPass(fs, s.Crossovers[j], defaultQ)))
			}
		}
	}
	return m
}

// apply traite une trame (un échantillon par canal, échelle int16) sur place.
func (m *multiband) apply(frame []float64) {
	n := len(m.comps) - 1
	for ch, x := range frame {
		sp := &m.split[ch]
		rest := x
		for i := 0; i < n; i++ {
			low := sp.lp[i].process(rest)
			rest = sp.hp[i].process(rest)
			for k := range sp.ap[i] { // passe-tout des séparations plus hautes
				low = sp.ap[i][k].process(low) + sp.aph[i][k].process(low)
			}
			m.sig[i][ch] = low
		}
		m.sig[n][ch] = rest
	}
	for b, c := range m.comps {
		if c != nil {
			c.apply(m.sig[b])
		}
	}
	for ch := range frame {
		y := 0.0
		for b := range m.sig {
			y += m.sig[b][ch]
		}
		frame[ch] = y
	}
}

// scaleMultiband met un réglage multibande à l'échelle d'une intensité (voir scaleCompression).
func scaleMultiband(s MultibandSettings, intensity float64) MultibandSettings {
	bands := make([]CompressorSettings, len(s.Bands))
	for i, c := range s.Bands {
		bands[i] = scaleCompression(c, intensity)
	}
	s.Bands = bands
	return s
}
