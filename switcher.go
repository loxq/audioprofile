package audioprofile

import (
	"io"
	"math"
	"sync"
)

const (
	switchWarmupMs = 2000.0 // historique rejoué dans la nouvelle chaîne pour qu'elle atteigne son régime (AGC, réverb…)
	switchFadeMs   = 30.0   // fondu enchaîné entre l'ancienne et la nouvelle chaîne
	switchBlock    = 1024   // trames traitées par passage : granularité du changement
	hubReadSize    = 32 * 1024
)

// Switcher est un flux dont la chaîne de traitement peut être remplacée en cours de lecture, sans clic,
// par exemple pour régler des paramètres à l'oreille.
//
// Il fait traverser la source par une chaîne construite par build (ApplyProfile, TubeAmp, une
// suite d'effets… : toute fonction qui enveloppe un io.Reader). Swap en installe une autre :
//
//   - la nouvelle chaîne est construite sur la même source, relue sur les deux dernières secondes
//     déjà traitées. Sa sortie est jetée : elle sert à mettre en régime les filtres, les enveloppes des
//     compresseurs et les lignes à retard, pour que le changement ne soit pas un redémarrage à froid ;
//   - puis un fondu enchaîné de 30 ms fait passer de l'ancienne sortie à la nouvelle, trame par trame,
//     alignées sur le même instant du signal.
//
// Ce qui ne se rattrape pas : une queue plus longue que deux secondes (réverb très longue, écho à forte
// rétroaction), ou un effet dont l'état dépend de tout le passé (sonie intégrée), repart de ce que
// l'historique permet de reconstruire.
//
// Le flux garde la longueur de la source, et un changement n'ajoute aucune latence. Read doit être appelé par
// un seul goroutine à la fois ; Swap peut l'être depuis n'importe quel autre. Un changement demandé
// pendant un fondu attend sa fin ; deux changements demandés avant qu'il soit pris en compte : seul le dernier compte.
type Switcher struct {
	rate, channels int
	frameSize      int
	warmup, fade   int // en trames

	hub     *hub
	cur     io.Reader
	old     io.Reader // chaîne en fin de vie pendant un fondu (nil sinon)
	fadeAt  int       // trames de fondu déjà faites
	emitted int64     // trames de sortie déjà produites : l'instant du signal auquel appartient la prochaine
	out     []byte
	err     error

	mu      sync.Mutex
	pending func(io.Reader) io.Reader
}

// NewSwitcher renvoie un Switcher qui traite src avec la chaîne build. Si sampleRate <= 0, aucun
// changement n'est possible et build est appliquée telle quelle.
func NewSwitcher(src io.Reader, sampleRate, channels int, build func(io.Reader) io.Reader) *Switcher {
	if channels < 1 {
		channels = 1
	}
	s := &Switcher{
		rate: sampleRate, channels: channels, frameSize: channels * bytesPerSm,
		warmup: int(switchWarmupMs / 1000 * float64(sampleRate)),
		fade:   int(switchFadeMs / 1000 * float64(sampleRate)),
		hub:    &hub{src: src},
	}
	s.cur = build(s.hub.branch(0))
	return s
}

// Swap remplace la chaîne par build, au prochain bloc. Sûr depuis n'importe quel goroutine.
func (s *Switcher) Swap(build func(io.Reader) io.Reader) {
	s.mu.Lock()
	s.pending = build
	s.mu.Unlock()
}

func (s *Switcher) Read(p []byte) (int, error) {
	for len(s.out) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		s.next()
	}
	n := copy(p, s.out)
	s.out = s.out[n:]
	return n, nil
}

// next produit un bloc de sortie.
func (s *Switcher) next() {
	if s.old == nil && s.rate > 0 {
		s.mu.Lock()
		build := s.pending
		s.pending = nil
		s.mu.Unlock()
		if build != nil {
			s.startFade(build)
		}
	}
	buf := make([]byte, switchBlock*s.frameSize)
	n, err := io.ReadFull(s.cur, buf)
	buf = buf[:n-n%s.frameSize]
	if s.old != nil {
		old := make([]byte, len(buf))
		m, _ := io.ReadFull(s.old, old)
		m -= m % s.frameSize
		s.mix(buf[:m], old[:m])
		if m < len(buf) { // l'ancienne chaîne finit avant : ne devrait pas arriver, les deux ont la même longueur
			s.old = nil
		}
	}
	s.emitted += int64(len(buf) / s.frameSize)
	s.hub.trim((s.emitted - int64(s.warmup)) * int64(s.frameSize))
	s.out = buf
	if err != nil {
		if err == io.ErrUnexpectedEOF {
			err = io.EOF
		}
		s.err = err
	}
}

// startFade construit la nouvelle chaîne, la met en régime sur l'historique et lance le fondu.
func (s *Switcher) startFade(build func(io.Reader) io.Reader) {
	start := s.emitted - int64(s.warmup)
	if start < 0 {
		start = 0
	}
	if base := s.hub.base / int64(s.frameSize); start < base { // historique déjà jeté (ne devrait pas arriver)
		start = base
	}
	nc := build(s.hub.branch(start * int64(s.frameSize)))
	// la sortie des trames [start, emitted[ a déjà été entendue : on la jette
	io.CopyN(io.Discard, nc, (s.emitted-start)*int64(s.frameSize))
	s.old, s.cur, s.fadeAt = s.cur, nc, 0
}

// mix fait passer buf (nouvelle chaîne) de l'ancienne sortie old à la nouvelle, linéairement.
func (s *Switcher) mix(buf, old []byte) {
	frames := len(old) / s.frameSize
	for f := 0; f < frames; f++ {
		t := 1.0
		if s.fadeAt+f < s.fade {
			t = float64(s.fadeAt+f+1) / float64(s.fade)
		}
		for ch := 0; ch < s.channels; ch++ {
			j := f*s.frameSize + ch*bytesPerSm
			a := float64(int16(uint16(old[j]) | uint16(old[j+1])<<8))
			b := float64(int16(uint16(buf[j]) | uint16(buf[j+1])<<8))
			v := uint16(int16(math.Max(-32768, math.Min(32767, math.Round(a+(b-a)*t)))))
			buf[j], buf[j+1] = byte(v), byte(v>>8)
		}
	}
	s.fadeAt += frames
	if s.fadeAt >= s.fade {
		s.old = nil
	}
}

// ---------------------------------------------------------------- hub

// hub lit la source une seule fois et garde ce qu'il a lu : plusieurs branches (l'ancienne et la nouvelle
// chaîne) la parcourent chacune à leur position. Les octets avant base sont jetés.
type hub struct {
	src  io.Reader
	data []byte
	base int64 // position absolue (en octets) du premier octet de data
	err  error
}

func (h *hub) end() int64 { return h.base + int64(len(h.data)) }

// trim jette les octets avant pos, par paquets pour ne pas recopier l'historique à chaque bloc.
func (h *hub) trim(pos int64) {
	if n := pos - h.base; n >= 2*hubReadSize {
		if n > int64(len(h.data)) {
			n = int64(len(h.data))
		}
		h.data = append(h.data[:0], h.data[n:]...)
		h.base += n
	}
}

func (h *hub) branch(pos int64) *branch { return &branch{h: h, pos: pos} }

type branch struct {
	h   *hub
	pos int64
}

func (b *branch) Read(p []byte) (int, error) {
	h := b.h
	for b.pos >= h.end() {
		if h.err != nil {
			return 0, h.err
		}
		buf := make([]byte, hubReadSize)
		n, err := h.src.Read(buf)
		h.data = append(h.data, buf[:n]...)
		h.err = err
	}
	n := copy(p, h.data[b.pos-h.base:])
	b.pos += int64(n)
	return n, nil
}
