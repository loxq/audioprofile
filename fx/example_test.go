package fx_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/loxq/audioprofile/fx"
)

func ExampleParse() {
	// un effet par son nom, avec des paramètres au format cle=valeur ; ceux qu'on ne donne pas prennent leur défaut
	e, err := fx.Parse("delay:time=300,feedback=0.4", nil)
	fmt.Println(err, e.Def.Name, e.Vals["time"], e.Vals["feedback"], e.Vals["mix"])
	// Output: <nil> delay 300 0.4 0.3
}

func ExampleEffect_Validate() {
	// Parse n'accepte que des noms et des nombres ; les bornes se vérifient avec Validate
	e, _ := fx.Parse("reverb:room=1.5", nil)
	fmt.Println(e.Validate(44100, 2))
	// Output: fx reverb : room doit être dans [0, 1] (reçu 1.5)
}

func ExampleEffect_Apply() {
	e, _ := fx.Parse("lpf:freq=2000,order=4", nil) // un passe-bas
	if err := e.Validate(44100, 2); err != nil {
		panic(err)
	}

	in := make([]byte, 4*1000) // 1000 trames stéréo
	out, _ := io.ReadAll(e.Apply(bytes.NewReader(in), 44100, 2))
	fmt.Println(len(out) == len(in))
	// Output: true
}

func ExampleNew() {
	// sans passer par du texte : des valeurs nommées, et des valeurs par défaut qui viennent d'ailleurs
	// (le fichier de réglages, par exemple)
	defaults := map[string]map[string]float64{"hpf": {"freq": 80}}

	e, err := fx.New("hpf", nil, defaults) // freq est obligatoire, mais quelqu'un l'a donnée
	fmt.Println(err, e.Vals["freq"], e.Vals["order"])

	_, err = fx.New("hpf", nil, nil) // personne ne l'a donnée
	fmt.Println(err)
	// Output:
	// <nil> 80 2
	// fx hpf : paramètre "freq" obligatoire (exemple : hpf:freq=...)
}

func ExampleNames() {
	fmt.Println(fx.Names()[:4])
	// Output: [hpf lpf parcomp lofi]
}
