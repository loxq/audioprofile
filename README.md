# audioprofile

Des briques de traitement audio **composables** pour Go, quelques préréglages bien sentis, et l'outillage pour les régler et les déboguer : une commande de test, un fichier de réglages rechargeable à chaud et un panneau web de curseurs.

Tout se fait **au fil de l'eau** sur des `io.Reader` : on branche un flux PCM, on récupère un autre `io.Reader`, sans charger le fichier en mémoire. Les briques s'enchaînent.

[![Le panneau de réglage d'audioprofile en train de régler le profil radio-hits : vumètres, sonie et spectre avant / après (la sortie est environ 5 LU plus forte) avec la courbe de l'égaliseur en pointillés, puis les curseurs des bandes, du compresseur, du limiteur et de la largeur stéréo](docs/audioprofile-web.png)](docs/reglage.md#panneau-de-réglage--serve)

*Le panneau de réglage (`-serve`) pendant la lecture, sur le profil `radio-hits` : vumètres, sonie et spectre avant / après avec la courbe de l'égaliseur, et les curseurs du profil, appliqués en direct.*

## Démarrage rapide

```sh
go get github.com/loxq/audioprofile
```

**Un profil prédéfini** sur un fichier :

```go
package main

import (
	"io"
	"os"

	"github.com/loxq/audioprofile"
)

func main() {
	in, _ := os.Open("musique.raw") // PCM s16le, 44,1 kHz, stéréo
	defer in.Close()

	out, _ := os.Create("musique_rock.raw")
	defer out.Close()

	// profil Rock à 80 % de son intensité
	io.Copy(out, audioprofile.ApplyProfile(in, 44100, 2, audioprofile.Rock, 0.8))
}
```

**Des briques enchaînées** : chacune prend un `io.Reader` et en renvoie un autre.

```go
r := audioprofile.HighPassFilter(src, 44100, 2, 80, 2) // coupe sous 80 Hz
r = audioprofile.Compressor(r, 44100, 2, audioprofile.CompressorSettings{ThresholdDB: -18, Ratio: 3, AttackMs: 10, ReleaseMs: 120, KneeDB: 6})
r = audioprofile.Reverb(r, 44100, 2, audioprofile.ReverbSettings{RoomSize: 0.4, Mix: 0.15})
r = audioprofile.Limiter(r, 44100, 2, audioprofile.LimiterSettings{CeilingDB: -1}) // crêtes sous −1 dBFS
```

**Essayer à l'oreille**, sans écrire de code, avec la commande et `ffmpeg` :

```sh
go build -o audioprofile ./cmd/audioprofile

ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
```

Pour **régler en direct** avec le panneau (curseurs, vumètres, sonie, spectre), boucler le morceau et ajouter `-serve` ; l'adresse du panneau s'affiche sur la sortie d'erreur :

```sh
ffmpeg -loglevel error -stream_loop -1 -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -serve 127.0.0.1:8080 -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
```

`./audioprofile -list` donne les profils, `-list-amps` les amplis, `-list-fx` les effets. Voir [la commande](docs/cli.md).

## Format d'entrée / sortie

PCM **16 bits signé, little-endian, entrelacé** (s16le). Aucun en-tête n'est géré : ni WAV, ni autre conteneur, seulement les données brutes. Une éventuelle trame incomplète en fin de flux est ignorée.

Tous les points d'entrée prennent les mêmes paramètres de base :

| Paramètre    | Description                                                              |
|--------------|--------------------------------------------------------------------------|
| `src`        | Flux PCM s16le entrelacé                                                 |
| `sampleRate` | Fréquence d'échantillonnage en Hz (ex. `44100`)                          |
| `channels`   | Nombre de canaux (1 = mono, 2 = stéréo…). Une valeur < 1 est ramenée à 1 |

Si `sampleRate <= 0`, le flux est renvoyé tel quel. Un état de filtre est tenu par canal, donc mono, stéréo et multicanal fonctionnent.

## Les briques

| Brique | Ce qu'elle fait |
|--------|-----------------|
| [`ApplyProfile`](docs/profils.md) | un **profil prédéfini** (Rock, Pop, Jazz…, ampli à tubes, profils **radio**) avec une intensité réglable |
| [`Equalizer`](docs/briques.md#égaliseur-paramétrique), `HighPassFilter`, `LowPassFilter`, `NotchFilter` | égaliseur paramétrique à bandes libres, filtres de Butterworth, coupe-bande |
| [`BassBoost`, `MidBoost`, `TrebleBoost`](docs/briques.md#préréglages-à-une-bande) | préréglages à une bande, réglés par une seule intensité |
| [`Compressor`](docs/briques.md#compresseur), [`ParallelCompressor`](docs/briques.md#compression-parallèle), [`MultibandCompressor`](docs/briques.md#compresseur-multibande) | compression de dynamique : simple, parallèle, par bandes |
| [`Limiter`](docs/briques.md#limiteur) | limiteur de crête à anticipation : plafond garanti |
| [`Gate`](docs/briques.md#gate--expandeur), [`DeEsser`](docs/briques.md#de-esser), [`TransientShaper`](docs/briques.md#renforçateur-de-transitoires) | réduction du bruit de fond, des sifflantes, mise en forme des attaques |
| [`TubeAmp`](docs/briques.md#ampli-à-tubes), [`Exciter`](docs/briques.md#exciter), [`LoFi`](docs/briques.md#lo-fi) | couleur : saturation de tubes, harmoniques dans les aigus, vieux supports |
| [`Chorus`, `Flanger`](docs/briques.md#chorus-et-flanger), [`Delay`](docs/briques.md#delay-écho), [`Reverb`](docs/briques.md#réverbération) | modulation, écho, réverbération |
| [`StereoWidth`](docs/briques.md#élargisseur-stéréo), [`Crossfeed`](docs/briques.md#crossfeed-pour-casque) | image stéréo : élargissement mid/side, crossfeed pour casque |
| [`MeasureLoudness`, `NormalizeLoudness`, `Alternate`](docs/mesure.md) | sonie EBU R128, normalisation, comparaison A/B à sonie égale |
| [`Meter`](docs/mesure.md#mesure-en-continu-meter) | mesure d'un flux en continu : vumètres, sonie, spectre, pour un affichage ou la surveillance |
| [`Switcher`](docs/streaming.md#le-switcher) | changement de chaîne **à chaud**, sans clic |

## Régler et déboguer

- **[La commande](docs/cli.md)** : un profil ou un ampli, des effets en ligne (`-fx`), une comparaison A/B à sonie égale (`-ab`), la mesure et le réglage de la sonie (`-loudness`, `-target-lufs`).
- **[Un fichier de réglages YAML](docs/reglage.md)** (`-config`) : on change les profils, les amplis et les valeurs par défaut des effets sans toucher au code. Avec `-watch`, le fichier est **rechargé à chaud** : éditer, enregistrer, écouter.
- **[Un panneau web](docs/reglage.md#panneau-de-réglage--serve)** (`-serve`) : des curseurs sur le profil, l'ampli et les effets, appliqués en cours de lecture, avec bascule A/B, export du réglage en YAML, et en direct les **vumètres, la sonie et le spectre avant / après** avec la courbe de l'égaliseur par-dessus.

```sh
ffmpeg -loglevel error -stream_loop -1 -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -serve 127.0.0.1:8080 -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
# ouvrir l'adresse affichée sur la sortie d'erreur
```

## Dans un programme de streaming

Les mêmes pièces que la commande, sans la commande : le flux traité est un `io.Reader`, et le panneau règle la chaîne pendant qu'il coule.

```go
cfg, _ := config.Load("reglages.yaml")
pl := live.Pipeline{Profile: "rock", Intensity: 1, Rate: 44100, Channels: 2, FX: []string{"delay:time=300"}}
build, _ := pl.Build(cfg)

sw := audioprofile.NewSwitcher(src, pl.Rate, pl.Channels, build) // lire sw : le flux traité
tuner, _ := live.NewTuner(cfg, pl, sw)
url, stop, _ := panel.Serve("127.0.0.1:8080", tuner) // le panneau, à garder en développement
defer stop()
```

Détails, paquets et conseils pour la production : [Dans un programme de streaming](docs/streaming.md). Pour une **webradio Go → Icecast**, un exemple complet et testé montre où s'intercaler entre le décodeur et l'encodeur, avec un profil par émission : [examples/webradio](examples/webradio/README.md).

## Structure du dépôt

```
.                  les traitements, les profils, le Switcher, la mesure de sonie (aucune dépendance externe)
fx/                registre des effets par nom : fx.Parse("delay:time=300")
config/            fichier de réglages YAML
panel/             panneau de réglage web
live/              Pipeline, rechargement à chaud (Watch), Tuner (panneau branché sur un Switcher)
cmd/audioprofile/  la commande de test
docs/              la documentation détaillée
examples/webradio/  une webradio Go → Icecast : playlist, décodage, traitement par émission, encodage, annonce des titres
```

## Documentation

| Document | Contenu |
|----------|---------|
| [Profils prédéfinis](docs/profils.md) | les profils, l'intensité, les profils radio, la chaîne de traitement |
| [Les briques](docs/briques.md) | chaque traitement : signature, paramètres, comportement, exemples |
| [Sonie, normalisation, A/B](docs/mesure.md) | mesure EBU R128, normalisation, comparaison à sonie égale |
| [La commande](docs/cli.md) | toutes les options de `audioprofile` |
| [Régler en direct](docs/reglage.md) | fichier de réglages, rechargement à chaud, panneau web |
| [Dans un programme de streaming](docs/streaming.md) | les paquets, le `Switcher`, brancher le tout, la production |
| [Référence Go](https://pkg.go.dev/github.com/loxq/audioprofile) | la documentation des paquets et leurs exemples exécutables (`go doc`, pkg.go.dev) |
| [Limites connues](docs/limites.md) | ce qui n'est pas fait, ce qui n'a pas été validé à l'oreille |
| [Tests](docs/tests.md) | ce que chaque test vérifie |

## Limites

Les réglages des profils sont des **points de départ vérifiés par mesure**, pas par l'oreille. Seul le format s16le est géré. `-ab`, `-target-lufs` et `NormalizeLoudness` travaillent en mémoire (environ 60 Mo par minute de stéréo à 44,1 kHz). Liste complète : [Limites connues](docs/limites.md).

```sh
go test ./...
```
