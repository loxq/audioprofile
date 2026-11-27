package live_test

import (
	"bytes"
	"fmt"
	"io"

	"github.com/loxq/audioprofile/live"
)

func ExamplePipeline_Build() {
	// la chaîne demandée : un profil, puis des effets au format de fx.Parse
	pl := live.Pipeline{
		Profile: "rock", Intensity: 1, Rate: 44100, Channels: 2,
		FX: []string{"delay:time=300"},
	}
	build, err := pl.Build(nil) // nil : les valeurs par défaut (sinon, un *config.Config)
	fmt.Println(err)

	in := make([]byte, 4*44100) // une seconde de stéréo
	out, _ := io.ReadAll(build(bytes.NewReader(in)))
	fmt.Println(len(out) == len(in))
	// Output:
	// <nil>
	// true
}

func ExamplePipeline_Build_error() {
	// une chaîne invalide est refusée avant de toucher au flux
	pl := live.Pipeline{Profile: "rock", Intensity: 1, Rate: 44100, Channels: 2, FX: []string{"reverb:room=3"}}
	_, err := pl.Build(nil)
	fmt.Println(err)
	// Output: fx reverb : room doit être dans [0, 1] (reçu 3)
}
