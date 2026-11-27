package config_test

import (
	"fmt"
	"strings"

	"github.com/loxq/audioprofile/config"
)

func ExampleParse() {
	// ce que le fichier ne précise pas garde sa valeur par défaut : ici, seul le ratio du compresseur du rock change
	c, err := config.Parse(strings.NewReader(`
profiles:
  rock:
    compression: {ratio: 6}
`))
	rock, _ := c.Profile("rock")
	fmt.Println(err, rock.Compression.Ratio, rock.Compression.ThresholdDB, len(rock.Bands))
	// Output: <nil> 6 -20 4
}

func ExampleConfig_Profile() {
	// nil est la configuration vide : les profils prédéfinis, tels quels
	var none *config.Config
	rock, ok := none.Profile("rock")
	fmt.Println(ok, rock.Compression.Ratio)

	_, ok = none.Profile("metal")
	fmt.Println(ok)
	// Output:
	// true 3
	// false
}

func ExampleConfig_Validate() {
	// les valeurs hors bornes sont refusées, pour tous les profils du fichier, utilisés ou non
	c, _ := config.Parse(strings.NewReader(`
profiles:
  mon-profil:
    bands:
      - {type: peaking, freq: 30000, gain: 3}
`))
	fmt.Println(c.Validate(44100))
	// Output: config : profil "mon-profil" : bands[0] (peaking) : freq doit être dans ]0, 22050[ Hz (reçu 30000)
}
