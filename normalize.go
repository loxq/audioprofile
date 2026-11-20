package audioprofile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
)

// NormalizeOptions règle NormalizeLoudness.
type NormalizeOptions struct {
	TargetLUFS float64 // sonie intégrée visée, dans [-70, 0] (par exemple -14 pour le streaming, -23 pour la diffusion EBU R128)
	CeilingDB  float64 // plafond des crêtes d'échantillon en dBFS, dans [-40, 0] (0 = pleine échelle) ; voir Limiter
	MaxBoostDB float64 // gain maximal accordé, en dB ; <= 0 → 24
}

// NormalizeResult décrit ce que NormalizeLoudness a fait.
type NormalizeResult struct {
	Before, After Loudness // sonie avant et après (After = Before si rien n'a été fait)
	GainDB        float64  // gain appliqué avant le limiteur
	Reached       bool     // la sonie intégrée visée est atteinte à 0,1 LU près
	Limited       bool     // le limiteur a dû réduire des crêtes
	Reason        string   // si Reached est faux : pourquoi
}

const (
	defaultMaxBoostDB    = 24.0
	normalizeToleranceLU = 0.1
	normalizeMaxPasses   = 8
)

// gainBands exprime un gain en dB par des bandes de type Gain (chacune est limitée à ±24 dB).
func gainBands(db float64) []Band {
	var bands []Band
	for db != 0 {
		step := math.Max(-maxBandDB, math.Min(maxBandDB, db))
		bands = append(bands, Band{Type: Gain, GainDB: step})
		db -= step
	}
	return bands
}

// NormalizeLoudness amène la sonie intégrée (LUFS, voir MeasureLoudness) d'un signal PCM
// (s16le, entrelacé, en mémoire) à o.TargetLUFS, et renvoie le résultat sans modifier l'entrée.
//
// Le signal reçoit un gain (en nombres flottants, sans écrêtage), puis un [Limiter] à o.CeilingDB (anticipation 5 ms) qui garantit que
// les crêtes d'échantillon ne dépassent pas le plafond. Comme le limiteur baisse un peu la sonie,
// la mesure est refaite et le gain corrigé jusqu'à tomber sur la cible (jusqu'à 8 passes). Le
// signal d'origine est toujours repris pour chaque passe : le résultat ne dépend pas du nombre de
// passes.
//
// La cible n'est pas toujours atteignable ; le résultat le dit (Reached, Reason) et rend ce qui a
// été obtenu de plus proche, sans dépasser le plafond :
//   - un morceau très faible demanderait plus que MaxBoostDB de gain ;
//   - un morceau très « pointu » (crêtes fortes, peu d'énergie) ne peut pas être monté sans que
//     le limiteur ne l'écrase : on s'arrête quand le gain n'apporte plus de sonie.
//
// Un signal silencieux, ou de moins de 400 ms de signal audible (sans sonie intégrée), est
// renvoyé tel quel, sans erreur. Une cible ou un plafond invalide (NaN, hors bornes) donne une
// erreur et le signal tel quel.
//
// Le plafond porte sur les crêtes d'échantillon, pas sur les crêtes inter-échantillons (true peak).
func NormalizeLoudness(pcm []byte, sampleRate, channels int, o NormalizeOptions) ([]byte, NormalizeResult, error) {
	same := func() []byte { return ApplyGain(pcm, 0) }
	var res NormalizeResult
	if math.IsNaN(o.TargetLUFS) || o.TargetLUFS < -70 || o.TargetLUFS > 0 {
		return same(), res, fmt.Errorf("cible %g LUFS hors de [-70, 0]", o.TargetLUFS)
	}
	if math.IsNaN(o.CeilingDB) || o.CeilingDB < minCeilingDB || o.CeilingDB > 0 {
		return same(), res, fmt.Errorf("plafond %g dBFS hors de [%g, 0]", o.CeilingDB, minCeilingDB)
	}
	if math.IsNaN(o.MaxBoostDB) {
		return same(), res, errors.New("gain maximal NaN")
	}
	if sampleRate <= 0 || channels < 1 {
		return same(), res, errors.New("fréquence d'échantillonnage ou nombre de canaux invalide")
	}
	maxBoost := o.MaxBoostDB
	if maxBoost <= 0 {
		maxBoost = defaultMaxBoostDB
	}
	pcm = pcm[:len(pcm)-len(pcm)%(channels*bytesPerSm)] // trames entières

	before, err := MeasureLoudness(bytes.NewReader(pcm), sampleRate, channels)
	if err != nil {
		return same(), res, err
	}
	res.Before, res.After = before, before
	if !before.Valid {
		res.Reason = "signal silencieux ou de moins de 400 ms : pas de sonie à normaliser"
		return same(), res, nil
	}

	// render applique le gain puis le limiteur, et mesure le résultat. Le gain et le limiteur sont
	// dans la même chaîne, en nombres flottants : un gain qui dépasse la pleine échelle n'est pas
	// écrêté avant que le limiteur ne le voie.
	render := func(gain float64) ([]byte, Loudness, error) {
		out, err := io.ReadAll(newChainReader(bytes.NewReader(pcm), sampleRate, channels,
			chain{bands: gainBands(gain), limit: &LimiterSettings{CeilingDB: o.CeilingDB}}))
		if err != nil {
			return nil, Loudness{}, err
		}
		l, err := MeasureLoudness(bytes.NewReader(out), sampleRate, channels)
		return out, l, err
	}

	gain := math.Min(maxBoost, o.TargetLUFS-before.IntegratedLUFS)
	var best []byte
	var bestL Loudness
	bestGain, bestErr := 0.0, math.Inf(1)
	prevGain, prevL := math.NaN(), math.NaN()
	for pass := 0; pass < normalizeMaxPasses; pass++ {
		out, l, err := render(gain)
		if err != nil {
			return same(), res, err
		}
		if !l.Valid {
			break
		}
		if e := math.Abs(l.IntegratedLUFS - o.TargetLUFS); e < bestErr {
			best, bestL, bestGain, bestErr = out, l, gain, e
		}
		if bestErr <= normalizeToleranceLU {
			break
		}
		// Le gain suivant corrige l'écart observé. Si le dernier gain n'a presque rien ajouté à la
		// sonie (le limiteur mange tout), on est au bout : continuer ne ferait que durcir le limiteur.
		next := gain + (o.TargetLUFS - l.IntegratedLUFS)
		if next > maxBoost {
			next = maxBoost
		}
		if !math.IsNaN(prevGain) && gain != prevGain && (l.IntegratedLUFS-prevL) < 0.25*(gain-prevGain) {
			res.Reason = fmt.Sprintf("le limiteur à %g dBFS empêche d'aller plus fort", o.CeilingDB)
			break
		}
		if math.Abs(next-gain) < 0.01 {
			break
		}
		prevGain, prevL, gain = gain, l.IntegratedLUFS, next
	}
	if best == nil {
		return same(), res, nil
	}
	res.GainDB, res.After = bestGain, bestL
	res.Reached = bestErr <= normalizeToleranceLU
	if !res.Reached && res.Reason == "" {
		if bestGain >= maxBoost-0.01 {
			res.Reason = fmt.Sprintf("gain maximal de %g dB atteint", maxBoost)
		} else {
			res.Reason = fmt.Sprintf("le limiteur à %g dBFS empêche d'aller plus fort", o.CeilingDB)
		}
	}
	// le limiteur a-t-il agi ? oui si le gain seul aurait dépassé le plafond
	res.Limited = !bytes.Equal(best, ApplyGain(pcm, bestGain))
	return best, res, nil
}
