package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Track est un morceau à diffuser.
type Track struct {
	Path    string // fichier à décoder
	Title   string // ce qu'on annonce aux auditeurs (« Artiste - Titre »)
	Profile string // profil de traitement pour ce morceau ; vide : le profil par défaut
}

// scheduler donne le morceau suivant. Remplacez-le par votre planificateur : c'est le seul point d'entrée de
// votre programme dans cet exemple. Next bloque jusqu'à ce qu'il y ait quelque chose à jouer ; false : plus rien
// (fin de la diffusion).
type scheduler interface {
	Next(ctx context.Context) (Track, bool)
}

// playlistScheduler rejoue une liste de morceaux, une fois ou en boucle.
type playlistScheduler struct {
	tracks []Track
	loop   bool
	next   int
}

func (p *playlistScheduler) Next(ctx context.Context) (Track, bool) {
	if ctx.Err() != nil || len(p.tracks) == 0 {
		return Track{}, false
	}
	if p.next >= len(p.tracks) {
		if !p.loop {
			return Track{}, false
		}
		p.next = 0
	}
	t := p.tracks[p.next]
	p.next++
	return t, true
}

// parsePlaylist lit une liste de morceaux, un par ligne :
//
//	# commentaire
//	#profile: radio-talk            (vaut pour les morceaux suivants ; « #profile: » seul revient au profil par défaut)
//	musique/a.mp3                   (le titre annoncé est le nom du fichier)
//	musique/b.mp3 | Artiste - Titre (tout ce qui suit la première barre verticale)
//
// Les chemins relatifs sont relatifs à baseDir.
func parsePlaylist(r io.Reader, baseDir string) ([]Track, error) {
	var tracks []Track
	profile := ""
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
			if d, ok := strings.CutPrefix(strings.ToLower(line), "#profile:"); ok {
				profile = strings.TrimSpace(d)
			} // les autres lignes « # » sont des commentaires
		default:
			path, title, hasTitle := strings.Cut(line, "|") // le titre est ce qui suit la première barre
			path = strings.TrimSpace(path)
			if path == "" {
				return nil, fmt.Errorf("ligne %d : chemin vide", n)
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}
			if title = strings.TrimSpace(title); !hasTitle || title == "" {
				title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			}
			tracks = append(tracks, Track{Path: path, Title: title, Profile: profile})
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, errors.New("la liste ne contient aucun morceau")
	}
	return tracks, nil
}
