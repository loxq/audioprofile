package main

import (
	"strings"
	"time"
)

// watchInterval est l'intervalle entre deux regards de -watch sur le fichier (une variable pour les tests).
var watchInterval = 250 * time.Millisecond

// fxList reçoit les occurrences de -fx, dans l'ordre. L'analyse se fait après coup, pour ne
// renvoyer qu'une erreur lisible.
type fxList []string

func (f *fxList) String() string     { return strings.Join(*f, " ") }
func (f *fxList) Set(v string) error { *f = append(*f, v); return nil }
