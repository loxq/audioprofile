package audioprofile

import (
	"bytes"
	"math"
	"testing"
)

// musicLike : signal stéréo déterministe qui ressemble un peu à de la musique (quatre sinus dont
// l'amplitude est modulée lentement, des impulsions de grosse caisse), à peu près au niveau levelDB
// (en dBFS, niveau efficace).
func musicLike(fs, seconds int, levelDB float64) []byte {
	n := fs * seconds
	l, r := make([]float64, n), make([]float64, n)
	for i := range l {
		t := float64(i) / float64(fs)
		am := 0.6 + 0.4*math.Sin(2*math.Pi*0.4*t)
		kick := math.Exp(-math.Mod(t, 0.5)*30) * math.Sin(2*math.Pi*60*t)
		a := am*math.Sin(2*math.Pi*220*t) + 0.5*math.Sin(2*math.Pi*1500*t) + 0.3*math.Sin(2*math.Pi*6000*t) + 1.2*kick
		b := am*math.Sin(2*math.Pi*330*t+1) + 0.5*math.Sin(2*math.Pi*2500*t) + 0.2*math.Sin(2*math.Pi*8000*t) + 1.2*kick
		l[i], r[i] = a, b
	}
	var e float64
	for i := range l {
		e += l[i]*l[i] + r[i]*r[i]
	}
	scale := ampOf(levelDB) / math.Sqrt(e/float64(2*n))
	for i := range l {
		l[i], r[i] = math.Round(l[i]*scale), math.Round(r[i]*scale)
	}
	return interleave(l, r)
}

func normalize(t *testing.T, pcm []byte, fs int, o NormalizeOptions) ([]byte, NormalizeResult) {
	t.Helper()
	out, res, err := NormalizeLoudness(pcm, fs, 2, o)
	if err != nil {
		t.Fatal(err)
	}
	return out, res
}

func peakOf(pcm []byte) float64 { return maxAbs(samples(pcm)) }

func TestNormalizeReachesTarget(t *testing.T) {
	const fs = 44100
	for name, tc := range map[string]struct {
		levelDB float64 // niveau efficace du morceau d'essai
		target  float64
	}{
		"monter un morceau calme":   {-34, -23},
		"descendre un morceau fort": {-10, -16},
		"cible streaming":           {-24, -14},
	} {
		pcm := musicLike(fs, 12, tc.levelDB)
		out, res := normalize(t, pcm, fs, NormalizeOptions{TargetLUFS: tc.target, CeilingDB: -1})
		got := measure(t, out, fs, 2)
		t.Logf("%s : %.2f → %.2f LUFS (cible %g), gain %+.2f dB, limité=%v, crête %.0f", name, res.Before.IntegratedLUFS, got.IntegratedLUFS, tc.target, res.GainDB, res.Limited, peakOf(out))
		if !res.Reached || math.Abs(got.IntegratedLUFS-tc.target) > 0.15 {
			t.Errorf("%s : %.3f LUFS (atteint=%v, raison %q), attendu %g ± 0,15", name, got.IntegratedLUFS, res.Reached, res.Reason, tc.target)
		}
		if got.IntegratedLUFS != res.After.IntegratedLUFS {
			t.Errorf("%s : After (%.3f) ne correspond pas à la sortie (%.3f)", name, res.After.IntegratedLUFS, got.IntegratedLUFS)
		}
		if p := peakOf(out); p > ceilingAmp(-1)+0.5 {
			t.Errorf("%s : crête %.0f au-dessus du plafond de −1 dBFS", name, p)
		}
		if len(out) != len(pcm) {
			t.Errorf("%s : longueur %d, attendu %d", name, len(out), len(pcm))
		}
	}
}

// Baisser n'a pas besoin du limiteur : le résultat est exactement le signal au gain près.
func TestNormalizeAttenuationIsPlainGain(t *testing.T) {
	const fs = 44100
	pcm := musicLike(fs, 8, -20)
	out, res := normalize(t, pcm, fs, NormalizeOptions{TargetLUFS: -30, CeilingDB: -1})
	if res.Limited || !res.Reached {
		t.Fatalf("limité=%v atteint=%v", res.Limited, res.Reached)
	}
	if !bytes.Equal(out, ApplyGain(pcm, res.GainDB)) {
		t.Error("baisser la sonie ne doit être qu'un gain")
	}
	if res.GainDB >= 0 {
		t.Errorf("gain %.2f dB, attendu négatif", res.GainDB)
	}
}

// Monter un morceau dont les crêtes dépassent : le limiteur intervient, la cible est atteinte si
// l'énergie le permet, et le plafond tient.
func TestNormalizeBoostUsesLimiter(t *testing.T) {
	const fs = 44100
	pcm := musicLike(fs, 12, -22)
	// sans limiteur, la cible ferait dépasser le plafond : le gain nécessaire (+12 dB) amène les
	// pointes à environ +2 dBFS
	out, res := normalize(t, pcm, fs, NormalizeOptions{TargetLUFS: -10, CeilingDB: -3})
	if !res.Limited {
		t.Error("le limiteur doit avoir agi")
	}
	if p := peakOf(out); p > ceilingAmp(-3)+0.5 {
		t.Errorf("crête %.0f > plafond de −3 dBFS", p)
	}
	if !res.Reached {
		t.Errorf("cible non atteinte : %.2f LUFS (%s)", res.After.IntegratedLUFS, res.Reason)
	}
}

// Cible inatteignable : le résultat le dit et rend ce qui est le plus proche, sous le plafond.
func TestNormalizeUnreachableTargets(t *testing.T) {
	const fs = 44100
	// 1) plafond trop bas pour la sonie demandée : un sinus à −20 dBFS (crête) fait −20 LUFS au plus
	sine := tone(1000, ampOf(-3), fs, 6*fs)
	pcm := interleave(sampleFloats(sine), sampleFloats(sine))
	out, res := normalize(t, pcm, fs, NormalizeOptions{TargetLUFS: -5, CeilingDB: -20})
	if res.Reached || res.Reason == "" {
		t.Errorf("plafond de −20 dBFS, cible −5 LUFS : atteinte=%v raison=%q, attendu non atteinte avec une raison", res.Reached, res.Reason)
	}
	if p := peakOf(out); p > ceilingAmp(-20)+0.5 {
		t.Errorf("crête %.0f au-dessus du plafond", p)
	}
	if res.After.IntegratedLUFS > -19 || res.After.IntegratedLUFS < -21 {
		t.Errorf("sonie obtenue %.2f LUFS, attendu ≈ −20 (le plafond)", res.After.IntegratedLUFS)
	}
	// 2) gain maximal : un morceau à −70 LUFS environ ne peut pas monter de plus de 24 dB
	quiet := musicLike(fs, 12, -62)
	out2, res2 := normalize(t, quiet, fs, NormalizeOptions{TargetLUFS: -14, CeilingDB: -1})
	if res2.Reached || res2.GainDB > 24.01 {
		t.Errorf("morceau très faible : atteint=%v gain=%.2f dB, attendu non atteint et gain ≤ 24 dB", res2.Reached, res2.GainDB)
	}
	if res2.Reason == "" || res2.After.IntegratedLUFS >= -14 || len(out2) != len(quiet) {
		t.Errorf("raison %q, sonie %.2f", res2.Reason, res2.After.IntegratedLUFS)
	}
	// 3) un gain maximal explicite plus petit
	_, res3 := normalize(t, musicLike(fs, 12, -40), fs, NormalizeOptions{TargetLUFS: -14, CeilingDB: -1, MaxBoostDB: 6})
	if res3.Reached || math.Abs(res3.GainDB-6) > 0.01 {
		t.Errorf("MaxBoostDB 6 : atteint=%v gain=%.2f", res3.Reached, res3.GainDB)
	}
}

func sampleFloats(b []byte) []float64 {
	s := samples(b)
	out := make([]float64, len(s))
	for i, v := range s {
		out[i] = float64(v)
	}
	return out
}

// Normaliser deux fois ne change plus rien d'audible.
func TestNormalizeIsIdempotent(t *testing.T) {
	const fs = 44100
	opts := NormalizeOptions{TargetLUFS: -18, CeilingDB: -1}
	once, _ := normalize(t, musicLike(fs, 10, -30), fs, opts)
	twice, res := normalize(t, once, fs, opts)
	if math.Abs(res.GainDB) > 0.15 || !res.Reached {
		t.Errorf("deuxième passe : gain %.3f dB (atteint=%v), attendu ≈ 0", res.GainDB, res.Reached)
	}
	if a, b := measure(t, once, fs, 2).IntegratedLUFS, measure(t, twice, fs, 2).IntegratedLUFS; math.Abs(a-b) > 0.15 {
		t.Errorf("sonie %.3f puis %.3f LUFS", a, b)
	}
}

// Un seul gain pour les deux canaux : l'image stéréo ne bouge pas.
func TestNormalizeKeepsStereoBalance(t *testing.T) {
	const fs = 44100
	pcm := musicLike(fs, 10, -34)
	out, _ := normalize(t, pcm, fs, NormalizeOptions{TargetLUFS: -23, CeilingDB: -1})
	lin, rin := channel(pcm, 0), channel(pcm, 1)
	lout, rout := channel(out, 0), channel(out, 1)
	var el, er, el2, er2 float64
	for i := range lin {
		el += float64(lin[i]) * float64(lin[i])
		er += float64(rin[i]) * float64(rin[i])
		el2 += float64(lout[i]) * float64(lout[i])
		er2 += float64(rout[i]) * float64(rout[i])
	}
	if d := math.Abs(10*math.Log10(el/er) - 10*math.Log10(el2/er2)); d > 0.1 {
		t.Errorf("équilibre gauche/droite modifié de %.2f dB", d)
	}
}

func TestNormalizeSilenceShortAndInvalid(t *testing.T) {
	const fs = 44100
	for name, pcm := range map[string][]byte{
		"silence": make([]byte, 4*fs*2),
		"vide":    nil,
		"100 ms":  musicLike(fs, 1, -20)[:4*fs/10],
	} {
		out, res, err := NormalizeLoudness(pcm, fs, 2, NormalizeOptions{TargetLUFS: -14, CeilingDB: -1})
		if err != nil || !bytes.Equal(out, pcm) || res.GainDB != 0 || res.Reached || res.Reason == "" {
			t.Errorf("%s : err=%v, identique=%v, %+v", name, err, bytes.Equal(out, pcm), res)
		}
	}
	pcm := musicLike(fs, 2, -20)
	for name, o := range map[string]NormalizeOptions{
		"cible NaN":        {TargetLUFS: math.NaN()},
		"cible > 0":        {TargetLUFS: 3},
		"cible < -70":      {TargetLUFS: -80},
		"plafond NaN":      {TargetLUFS: -14, CeilingDB: math.NaN()},
		"plafond > 0":      {TargetLUFS: -14, CeilingDB: 1},
		"plafond < -40":    {TargetLUFS: -14, CeilingDB: -60},
		"gain maximal NaN": {TargetLUFS: -14, MaxBoostDB: math.NaN()},
	} {
		out, _, err := NormalizeLoudness(pcm, fs, 2, o)
		if err == nil || !bytes.Equal(out, pcm) {
			t.Errorf("%s : erreur attendue et signal intact (err=%v)", name, err)
		}
	}
	if _, _, err := NormalizeLoudness(pcm, 0, 2, NormalizeOptions{TargetLUFS: -14}); err == nil {
		t.Error("fréquence nulle : erreur attendue")
	}
	// l'entrée n'est jamais modifiée
	orig := append([]byte{}, pcm...)
	NormalizeLoudness(pcm, fs, 2, NormalizeOptions{TargetLUFS: -30, CeilingDB: -1})
	if !bytes.Equal(pcm, orig) {
		t.Error("l'entrée ne doit pas être modifiée")
	}
}

// Un gain de plus de 24 dB (une bande de Gain est limitée à ±24 dB) est réparti sur plusieurs bandes.
func TestNormalizeLargeGainSplitsAcrossBands(t *testing.T) {
	for _, db := range []float64{0, 5, 24, 30, 55.5, -30, -60} {
		var sum float64
		for _, b := range gainBands(db) {
			if math.Abs(b.GainDB) > maxBandDB {
				t.Errorf("%g dB : bande de %g dB hors de ±%g", db, b.GainDB, maxBandDB)
			}
			sum += b.GainDB
		}
		if math.Abs(sum-db) > 1e-9 {
			t.Errorf("%g dB : somme des bandes %g", db, sum)
		}
	}
	const fs = 44100
	quiet := musicLike(fs, 12, -66)
	out, res := normalize(t, quiet, fs, NormalizeOptions{TargetLUFS: -23, CeilingDB: -1, MaxBoostDB: 60})
	if !res.Reached || res.GainDB < 25 {
		t.Errorf("gain %.1f dB, atteint=%v (%s) : une montée de plus de 24 dB était demandée", res.GainDB, res.Reached, res.Reason)
	}
	if got := measure(t, out, fs, 2).IntegratedLUFS; math.Abs(got+23) > 0.15 {
		t.Errorf("sonie %.3f LUFS, attendu -23", got)
	}
}
