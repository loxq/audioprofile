// Package panel est le panneau de réglage : un serveur HTTP local et une page de curseurs qui règlent, en
// écoutant, le traitement d'un flux audio.
//
// Le paquet ne sait que parler HTTP et décrire ce que règle la page (State, Schema). Ce qu'un état veut dire (quel
// profil, quelles bornes, comment fabriquer la chaîne de traitement) est le travail de l'hôte, qui implémente Backend.
package panel

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed panel.html
var page []byte

// Backend est ce que l'hôte fournit au panneau.
type Backend interface {
	// Schema décrit les paramètres : bornes des curseurs, effets, noms des profils et des amplis.
	Schema() Schema
	// State donne l'état actuel, celui que la page affiche au chargement.
	State() State
	// Apply vérifie un nouvel état et, s'il est valable, le met en œuvre. Une erreur est affichée à l'utilisateur et
	// laisse l'état précédent en place.
	Apply(State) error
	// Preset donne les réglages de départ d'un profil (kind KindProfile) ou d'un ampli (KindAmp) ; une erreur si le nom
	// est inconnu.
	Preset(kind, name string) (*Profile, *Amp, error)
	// Export écrit l'état actuel sous une forme à garder (le YAML d'un profil, la ligne de commande équivalente).
	Export() (string, error)
}

const maxBody = 1 << 20

// Handler renvoie le gestionnaire HTTP du panneau.
//
// Le serveur n'a pas d'authentification : il ne parle qu'à une page de la même origine. Les requêtes avec une origine
// étrangère sont refusées, et les modifications n'acceptent que du JSON (ce qui impose une requête préalable aux pages
// d'un autre site). Pour se protéger du rebond DNS sur une adresse de bouclage, voir Serve.
func Handler(b Backend) http.Handler {
	return handler(b, nil)
}

// handler est Handler avec, en plus, un filtre sur l'en-tête Host (nil : aucun).
func handler(b Backend, allowHost func(host string) bool) http.Handler {
	schema := b.Schema()
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	fail := func(w http.ResponseWriter, code int, err error) {
		writeJSON(w, code, map[string]string{"error": err.Error()})
	}

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(page)
	})
	mux.HandleFunc("GET /api/schema", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, schema) })
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, b.State()) })
	mux.HandleFunc("PUT /api/state", func(w http.ResponseWriter, r *http.Request) {
		if mt := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mt != "application/json" {
			fail(w, http.StatusUnsupportedMediaType, errors.New("Content-Type doit être application/json"))
			return
		}
		var s State
		dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		if err := b.Apply(s); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/preset", func(w http.ResponseWriter, r *http.Request) {
		pp, pa, err := b.Preset(r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
		if err != nil {
			fail(w, http.StatusNotFound, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": pp, "amp": pa})
	})
	mux.HandleFunc("GET /api/yaml", func(w http.ResponseWriter, r *http.Request) {
		out, err := b.Export()
		if err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, out)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if allowHost != nil && !allowHost(r.Host) {
			http.Error(w, "hôte refusé", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if u, err := url.Parse(o); err != nil || u.Host != r.Host {
				http.Error(w, "origine refusée", http.StatusForbidden)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// loopbackHost accepte les noms d'hôte d'une adresse de bouclage : une page d'un autre site qui ferait pointer son
// nom vers 127.0.0.1 (rebond DNS) présente son propre nom dans Host et est refusée.
func loopbackHost(host string) bool {
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// Serve écoute sur addr (« 127.0.0.1:8080 », ou « 127.0.0.1:0 » pour un port libre) et sert le panneau. Il renvoie
// l'URL de la page et la fonction qui arrête le serveur (sûre à appeler plusieurs fois).
//
// Sur une adresse de bouclage, les requêtes dont l'hôte n'est pas localhost, 127.0.0.1 ou ::1 sont refusées (rebond DNS).
// Sur toute autre adresse, rien ne protège le panneau : ne l'exposez pas sur un réseau.
func Serve(addr string, b Backend) (pageURL string, stop func(), err error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	var allow func(string) bool
	if ta, ok := ln.Addr().(*net.TCPAddr); ok && ta.IP.IsLoopback() {
		allow = loopbackHost
	}
	srv := &http.Server{Handler: handler(b, allow), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() { defer close(done); srv.Serve(ln) }()
	var once sync.Once
	return fmt.Sprintf("http://%s/", ln.Addr()), func() { once.Do(func() { srv.Close(); <-done }) }, nil
}
