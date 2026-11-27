package main

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIcecast est un Icecast de poche : assez de protocole pour qu'un encodeur ffmpeg s'y connecte comme à un vrai
// (PUT avec Expect: 100-continue, le son brut ensuite) et pour recevoir les annonces de titre de l'administration. Il
// garde ce qu'on lui envoie. Comme le vrai, il refuse une annonce tant qu'aucune source n'est connectée.
type fakeIcecast struct {
	ln net.Listener
	wg sync.WaitGroup

	mu      sync.Mutex
	sources []*fakeSource
	admin   []adminRequest

	// dropFirstAfter : coupe la première connexion source après ce nombre d'octets (0 : jamais), pour simuler un Icecast
	// qui redémarre.
	dropFirstAfter int
}

type fakeSource struct {
	Method, Path string
	Header       map[string]string // clés en minuscules
	mu           sync.Mutex
	body         []byte
}

func (s *fakeSource) Body() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.body...)
}

type adminRequest struct {
	Query    string
	Auth     string // l'en-tête Authorization
	Accepted bool
}

func newFakeIcecast(t *testing.T) *fakeIcecast {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIcecast{ln: ln}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() { defer f.wg.Done(); f.handle(c) }()
		}
	}()
	t.Cleanup(func() { ln.Close(); f.wg.Wait() })
	return f
}

// URL renvoie l'URL de sortie de l'encodeur vers ce faux Icecast.
func (f *fakeIcecast) URL(mount string) string {
	return "icecast://source:hackme@" + f.ln.Addr().String() + mount
}

func (f *fakeIcecast) Sources() []*fakeSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*fakeSource(nil), f.sources...)
}

func (f *fakeIcecast) Admin() []adminRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]adminRequest(nil), f.admin...)
}

func (f *fakeIcecast) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	br := bufio.NewReader(c)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return
	}
	method, target := parts[0], parts[1]
	header := map[string]string{}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return
		}
		l = strings.TrimRight(l, "\r\n")
		if l == "" {
			break
		}
		if k, v, ok := strings.Cut(l, ":"); ok {
			header[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}

	if method == "GET" && strings.HasPrefix(target, "/admin/metadata") {
		f.mu.Lock()
		accepted := len(f.sources) > 0
		f.admin = append(f.admin, adminRequest{Query: target, Auth: header["authorization"], Accepted: accepted})
		f.mu.Unlock()
		if accepted {
			c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nConnection: close\r\n\r\n<?xml version=\"1.0\"?>\r\n<iceresponse><message>Metadata update successful</message><return>1</return></iceresponse>\r\n"))
		} else {
			c.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Type: text/xml\r\nConnection: close\r\n\r\n<?xml version=\"1.0\"?>\r\n<iceresponse><message>No such mount</message><return>0</return></iceresponse>\r\n"))
		}
		return
	}

	// une source : PUT (ou SOURCE) puis le son jusqu'à la fermeture
	src := &fakeSource{Method: method, Path: target, Header: header}
	f.mu.Lock()
	f.sources = append(f.sources, src)
	first := len(f.sources) == 1
	drop := f.dropFirstAfter
	f.mu.Unlock()
	if strings.EqualFold(header["expect"], "100-continue") {
		c.Write([]byte("HTTP/1.1 100 Continue\r\n\r\n"))
	}
	buf := make([]byte, 8192)
	for {
		n, err := br.Read(buf)
		if n > 0 {
			src.mu.Lock()
			src.body = append(src.body, buf[:n]...)
			size := len(src.body)
			src.mu.Unlock()
			if first && drop > 0 && size >= drop {
				return // la connexion se ferme d'un coup
			}
		}
		if err != nil {
			return
		}
	}
}
