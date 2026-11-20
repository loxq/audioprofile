package audioprofile

import (
	"io"
	"math"
)

// LimiterSettings décrit un limiteur de crête à anticipation (look-ahead).
type LimiterSettings struct {
	CeilingDB   float64 // plafond des crêtes en dBFS, ramené dans [-40, 0] (0 = pleine échelle)
	LookaheadMs float64 // anticipation en ms, ramenée dans [0,1 ; 50] ; <= 0 → 5
	ReleaseMs   float64 // temps de relâchement en ms ; <= 0 → 100
}

const (
	defaultLookaheadMs = 5.0
	maxLookaheadMs     = 50.0
	defaultLimitRelMs  = 100.0
	minCeilingDB       = -40.0
	limiterResync      = 1 << 16 // trames entre deux recalculs exacts de la moyenne glissante
)

// Limiter renvoie un flux PCM (s16le, entrelacé) dont aucune crête
// d'échantillon ne dépasse le plafond CeilingDB.
//
// Le limiteur lit à l'avance : le gain nécessaire pour chaque trame est calculé
// LookaheadMs plus tôt, puis la réduction est étalée en rampe sur cette durée,
// de sorte qu'elle est déjà complète quand le pic arrive (pas de distorsion
// d'attaque comme avec un compresseur). Le relâchement est exponentiel. Le niveau est
// détecté sur le canal le plus fort et le même gain s'applique à tous les canaux.
//
// Le signal est retardé de l'anticipation en interne, mais le flux de sortie
// est recalé : il a exactement la même longueur et le même alignement temporel
// que l'entrée (le retard est rattrapé en vidant le tampon à la fin du flux).
// Tant qu'aucune crête ne dépasse le plafond, le signal ressort bit à bit
// identique.
//
// Le plafond porte sur les valeurs d'échantillons, pas sur les crêtes
// inter-échantillons (true peak) : un convertisseur en aval peut en reconstruire
// qui dépassent de 1 à 2 dB. Prenez un plafond de −1 dBFS pour de la marge.
//
// Le flux est renvoyé tel quel si sampleRate <= 0 ou si CeilingDB est NaN.
func Limiter(src io.Reader, sampleRate, channels int, s LimiterSettings) io.Reader {
	return newChainReader(src, sampleRate, channels, chain{limit: &s})
}

// limiter : état d'un limiteur à anticipation. Pour la trame entrante n, avec
// D trames d'avance :
//
//	g[n] = gain minimal qui garde la trame n sous le plafond
//	m[n] = min g[n-D..n]                            (le gain est prêt D trames avant le pic)
//	r[n] = m[n] aussitôt si plus bas, sinon remonte exponentiellement   (relâchement ; r <= m)
//	G    = moyenne de r[n-D..n]                     (rampe d'attaque sur D trames)
//
// et la trame sortie est x[n-D] × G. La fenêtre de G contient toujours m[n-D+i]
// pour i = 0..D, chacun <= g[n-D] : le plafond est donc garanti.
type limiter struct {
	channels int
	delay    int // D
	ceil     float64
	rc       float64 // coefficient de relâchement

	frames []float64 // anneau des D+1 dernières trames d'entrée : frames[pos*channels+ch]
	rs     []float64 // anneau des D+1 derniers r
	sum    float64   // somme de rs
	rPrev  float64
	n      int64 // nombre de trames reçues

	// file monotone (minimum glissant de g sur D+1 trames), en anneau
	qIdx         []int64
	qVal         []float64
	qHead, qSize int
}

// newLimiter renvoie nil si le limiteur est sans effet.
func newLimiter(fs float64, channels int, s *LimiterSettings) *limiter {
	if s == nil || fs <= 0 || channels < 1 || math.IsNaN(s.CeilingDB) || math.IsNaN(s.LookaheadMs) || math.IsNaN(s.ReleaseMs) {
		return nil
	}
	look, rel := s.LookaheadMs, s.ReleaseMs
	if look <= 0 {
		look = defaultLookaheadMs
	}
	look = math.Max(0.1, math.Min(maxLookaheadMs, look))
	if rel <= 0 {
		rel = defaultLimitRelMs
	}
	d := int(math.Round(look / 1000 * fs))
	if d < 1 {
		d = 1
	}
	ceil := 32768 * math.Pow(10, math.Max(minCeilingDB, math.Min(0, s.CeilingDB))/20)
	l := &limiter{
		channels: channels,
		delay:    d,
		ceil:     math.Min(32767, ceil),
		rc:       math.Exp(-1 / (rel / 1000 * fs)),
		frames:   make([]float64, (d+1)*channels),
		rs:       make([]float64, d+1),
		sum:      float64(d + 1),
		rPrev:    1,
		qIdx:     make([]int64, d+2),
		qVal:     make([]float64, d+2),
	}
	for i := range l.rs {
		l.rs[i] = 1
	}
	return l
}

// pushMin ajoute g (trame n) à la fenêtre glissante et renvoie son minimum.
func (l *limiter) pushMin(g float64) float64 {
	c := len(l.qIdx)
	for l.qSize > 0 && l.qVal[(l.qHead+l.qSize-1)%c] >= g { // les valeurs plus grandes ne seront jamais le minimum
		l.qSize--
	}
	t := (l.qHead + l.qSize) % c
	l.qIdx[t], l.qVal[t] = l.n, g
	l.qSize++
	for l.qIdx[l.qHead] < l.n-int64(l.delay) { // sortie de la fenêtre
		l.qHead = (l.qHead + 1) % c
		l.qSize--
	}
	return l.qVal[l.qHead]
}

// apply prend la trame entrante (échelle int16) et la remplace sur place par la
// trame limitée, retardée de D trames. Elle renvoie false tant que le retard
// n'est pas rempli : il n'y a alors pas encore de sortie.
func (l *limiter) apply(frame []float64) bool {
	ring := l.delay + 1
	pos := int(l.n % int64(ring))
	old := int((l.n + 1) % int64(ring)) // trame n-D : la plus ancienne de l'anneau

	peak := 0.0
	for _, y := range frame {
		peak = math.Max(peak, math.Abs(y))
	}
	g := 1.0
	if peak > l.ceil {
		g = l.ceil / peak
	}
	m := l.pushMin(g)
	r := m
	if m > l.rPrev {
		r = m + l.rc*(l.rPrev-m) // relâchement
	}
	l.rPrev = r
	l.sum += r - l.rs[pos]
	l.rs[pos] = r

	ready := l.n >= int64(l.delay)
	gain := l.sum / float64(ring)
	for ch := range frame {
		delayed := l.frames[old*l.channels+ch]
		l.frames[pos*l.channels+ch] = frame[ch]
		frame[ch] = delayed * gain
	}
	l.n++
	if l.n%limiterResync == 0 { // évite la dérive de la somme glissante
		l.sum = 0
		for _, v := range l.rs {
			l.sum += v
		}
	}
	return ready
}
