package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// opener ouvre un morceau et renvoie son PCM s16le entrelacé, au format du flux. C'est l'autre point d'entrée de votre
// programme : si vous décodez déjà vos mp3 (en Go, avec une bibliothèque), renvoyez ce que votre décodeur produit.
// Close doit libérer le décodeur ; son erreur signale un décodage qui a mal fini.
type opener func(ctx context.Context, path string) (io.ReadCloser, error)

// ffmpegOpener décode avec ffmpeg : un processus par morceau, le PCM sur sa sortie standard. L'ouverture attend les
// premiers octets : un fichier absent ou illisible est refusé ici, avec le message de ffmpeg, avant que le morceau soit
// annoncé ou que son profil soit appliqué.
func ffmpegOpener(ffmpeg string, rate, channels int) opener {
	return func(ctx context.Context, path string) (io.ReadCloser, error) {
		cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
			"-i", path, "-vn", "-f", "s16le", "-ar", strconv.Itoa(rate), "-ac", strconv.Itoa(channels), "pipe:1")
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		d := &decoder{cmd: cmd, out: out, r: bufio.NewReaderSize(out, 64*1024)}
		if _, err := d.r.Peek(1); err != nil {
			d.Close()
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return nil, errors.New(msg)
			}
			return nil, fmt.Errorf("aucun son décodé : %w", err)
		}
		return d, nil
	}
}

type decoder struct {
	cmd *exec.Cmd
	out io.ReadCloser
	r   *bufio.Reader
	eof bool // le décodeur a tout rendu : il se termine de lui-même
}

func (d *decoder) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err == io.EOF {
		d.eof = true
	}
	return n, err
}

// Close libère le décodeur. Arrêté avant la fin (on passe au morceau suivant, ou la radio s'arrête), il est tué sans que
// ce soit une erreur ; arrivé au bout, il rapporte un échec de décodage éventuel.
func (d *decoder) Close() error {
	d.out.Close()
	if !d.eof && d.cmd.Process != nil {
		d.cmd.Process.Kill()
		d.cmd.Wait()
		return nil
	}
	return d.cmd.Wait()
}

// trackSource enchaîne les morceaux en un seul flux PCM continu : c'est ce qui permet à la chaîne de traitement de vivre
// d'un morceau à l'autre (l'AGC, le compresseur et le limiteur gardent leur état, rien n'est vidé à chaque fin de fichier).
//
// Un morceau qui ne s'ouvre pas, ou dont le décodage échoue, est signalé et sauté : une radio ne s'arrête pas pour un
// fichier cassé. onTrack est appelé quand le premier octet d'un morceau va être lu.
type trackSource struct {
	ctx       context.Context
	sched     scheduler
	open      opener
	frameSize int
	onTrack   func(Track)
	logf      func(format string, args ...any)

	cur     io.ReadCloser
	curName string
	carry   []byte // octets lus qui ne forment pas encore une trame entière
	ready   []byte // trames entières prêtes à être rendues
}

func (s *trackSource) Read(p []byte) (int, error) {
	for len(s.ready) == 0 {
		if s.ctx.Err() != nil {
			return 0, io.EOF
		}
		if s.cur == nil {
			if err := s.openNext(); err != nil {
				return 0, err
			}
			continue
		}
		buf := make([]byte, 16*1024)
		n, err := s.cur.Read(buf)
		s.carry = append(s.carry, buf[:n]...)
		whole := len(s.carry) - len(s.carry)%s.frameSize
		s.ready = append(s.ready, s.carry[:whole]...)
		s.carry = append(s.carry[:0], s.carry[whole:]...)
		if err != nil { // fin du morceau (io.EOF) ou erreur de lecture : une trame incomplète finale est ignorée
			if cerr := s.cur.Close(); cerr != nil && s.ctx.Err() == nil {
				s.logf("décodage de %s : %v", s.curName, cerr)
			}
			s.cur, s.carry = nil, s.carry[:0]
		}
	}
	n := copy(p, s.ready)
	s.ready = s.ready[n:]
	return n, nil
}

// openNext ouvre le prochain morceau jouable. Il renvoie io.EOF quand il n'y en a plus.
func (s *trackSource) openNext() error {
	for {
		t, ok := s.sched.Next(s.ctx)
		if !ok {
			return io.EOF
		}
		rc, err := s.open(s.ctx, t.Path)
		if err != nil {
			s.logf("morceau ignoré (%s) : %v", t.Path, err)
			continue
		}
		s.cur, s.curName = rc, t.Path
		if s.onTrack != nil {
			s.onTrack(t)
		}
		return nil
	}
}

// pacedReader livre r à la vitesse du temps réel (fois speed) : une radio émet au rythme de la musique, pas au rythme du
// disque. Il s'arrête (io.EOF) quand ctx est annulé. Si la livraison a pris plus d'une seconde de retard (le
// décodeur a calé), il repart de l'instant présent au lieu de rattraper d'un coup.
type pacedReader struct {
	ctx         context.Context
	r           io.Reader
	bytesPerSec float64
	start       time.Time
	sent        int64
}

const paceChunk = 20 * time.Millisecond

func newPacedReader(ctx context.Context, r io.Reader, rate, channels int, speed float64) *pacedReader {
	return &pacedReader{ctx: ctx, r: r, bytesPerSec: float64(rate*channels*2) * speed}
}

func (p *pacedReader) Read(b []byte) (int, error) {
	if p.ctx.Err() != nil { // sinon, quand l'échéance est déjà passée, select peut choisir l'autre branche à chaque fois
		return 0, io.EOF
	}
	now := time.Now()
	if p.start.IsZero() {
		p.start = now
	}
	frame := 4 // borne basse : une trame stéréo s16le
	max := int(p.bytesPerSec * paceChunk.Seconds())
	max -= max % frame
	if max < frame {
		max = frame
	}
	if len(b) > max {
		b = b[:max]
	}
	n, err := p.r.Read(b)
	p.sent += int64(n)
	due := p.start.Add(time.Duration(float64(p.sent) / p.bytesPerSec * float64(time.Second)))
	if late := now.Sub(due); late > time.Second {
		p.start = p.start.Add(late) // on ne rattrape pas : le direct ne revient pas en arrière
		due = now
	}
	select {
	case <-time.After(time.Until(due)):
	case <-p.ctx.Done():
		return 0, io.EOF // sans les octets lus : io.ReadFull ignorerait l'erreur si le bloc est complet
	}
	return n, err
}
