# audioprofile

Bibliothèque Go (et petit outil en ligne de commande) pour donner une couleur à un flux audio PCM : égaliseur, profils de genre (Rock, Pop, Jazz…), compresseur et saturation douce. Tout se fait **au fil de l'eau** sur des `io.Reader`, sans charger le fichier en mémoire.

Ce que le paquet `audioprofile` propose :

- `ApplyProfile` : applique un **profil prédéfini** (Rock, Pop, Classical, Jazz, HipHop, Electronic, Acoustic, Vocal) avec une intensité réglable ;
- `Equalizer` : **égaliseur paramétrique** à bandes libres (type, fréquence, gain en dB, Q) ;
- `Compressor` : **compresseur de dynamique** (seuil, ratio, attaque, relâchement, genou, compensation) ;
- `BassBoost`, `MidBoost`, `TrebleBoost` : **préréglages à une bande** (basses, médiums, aigus) réglés par une simple intensité ;
- `audioprofile` (commande) : applique un profil sur un flux lu sur l'entrée standard, pour se brancher sur un pipe `ffmpeg`.

Toutes ces fonctions prennent un `io.Reader` et en renvoient un autre : elles s'enchaînent, par exemple `TrebleBoost(MidBoost(BassBoost(src, …), …), …)`.

## Démarrage rapide

```sh
git clone https://github.com/loxq/audioprofile && cd audioprofile
go build -o audioprofile ./cmd/audioprofile

# Écouter un morceau avec le profil rock
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
```

Les profils disponibles s'affichent avec `./audioprofile -list`. Détails dans [Ligne de commande](#ligne-de-commande).

## Format d'entrée / sortie

PCM **16 bits signé, little-endian, entrelacé** (s16le). Aucun en-tête n'est géré : ni WAV, ni autre conteneur, seulement les données brutes. Une éventuelle trame incomplète en fin de flux est ignorée.

Tous les points d'entrée prennent les mêmes paramètres de base :

| Paramètre    | Description                                                              |
|--------------|--------------------------------------------------------------------------|
| `src`        | Flux PCM s16le entrelacé                                                 |
| `sampleRate` | Fréquence d'échantillonnage en Hz (ex. `44100`)                          |
| `channels`   | Nombre de canaux (1 = mono, 2 = stéréo…). Une valeur < 1 est ramenée à 1 |

Si `sampleRate <= 0`, le flux est renvoyé tel quel. Un état de filtre est tenu par canal, donc mono, stéréo et multicanal fonctionnent.

## Bibliothèque

```go
package main

import (
	"io"
	"os"

	"github.com/loxq/audioprofile"
)

func main() {
	in, _ := os.Open("musique.raw") // s16le, 44,1 kHz, stéréo
	defer in.Close()

	out, _ := os.Create("musique_rock.raw")
	defer out.Close()

	io.Copy(out, audioprofile.ApplyProfile(in, 44100, 2, audioprofile.Rock, 0.8))
}
```

### Profils prédéfinis

```go
func ApplyProfile(src io.Reader, sampleRate, channels int, p Profile, intensity float64) io.Reader
```

| Profil       | Nom (`ParseProfile`) | Caractère                                          |
|--------------|----------------------|----------------------------------------------------|
| `Flat`       | `flat`               | Aucune correction                                  |
| `Rock`       | `rock`               | Sourire : basses et aigus relevés, médiums creusés |
| `Pop`        | `pop`                | Voix en avant, basses et brillance modérées        |
| `Classical`  | `classical`          | Naturel, léger renfort des extrêmes                |
| `Jazz`       | `jazz`               | Chaud, médiums doux, aigus aérés                   |
| `HipHop`     | `hiphop`             | Basses profondes, voix détachée                    |
| `Electronic` | `electronic`         | Sub-basses et aigus marqués, médiums creusés       |
| `Acoustic`   | `acoustic`           | Naturel, présence des cordes et de l'air           |
| `Vocal`      | `vocal`              | Voix : intelligibilité, graves réduits             |

- **Intensité** : `0` = aucun effet, `1` = profil complet. Elle met à l'échelle les gains, le ratio du compresseur et le drive de la saturation.
- **Exagérer un profil** : une intensité **au-delà de 1** (jusqu'à `MaxIntensity` = 3) amplifie l'effet, utile pour juger d'un profil à l'oreille ou pour un rendu plus marqué. Le gain automatique grandit avec l'effet, donc le volume baisse (jusqu'à environ −7 dB à 3 selon le profil, mesuré à −18 dBFS et 1 kHz) ; la compensation du compresseur, elle, reste à sa valeur d'intensité 1 pour éviter l'écrêtage. Les valeurs hors de [0, `MaxIntensity`] sont ramenées dedans.
- **Gain automatique** : pour éviter l'écrêtage, un gain global négatif est ajouté afin que la fréquence la plus renforcée reste à 0 dB. Le volume perçu baisse donc d'autant, sauf pour les profils avec effets (voir plus bas).
- **Chaîne de traitement** : égalisation → compresseur → saturation → gain de sortie. Les deux derniers étages ne concernent que certains profils.
- **Valeurs** : les courbes sont des points de départ raisonnables, pas une norme (il n'existe pas de standard officiel). Elles sont définies dans [profiles.go](profiles.go).
- **Utilitaires** : `Profiles()` liste les profils, `ParseProfile("rock")` en retrouve un par son nom (insensible à la casse), `p.String()` donne son nom, et `p.Bands()` renvoie une copie de ses bandes d'égalisation pour partir d'elles avec `Equalizer`.

#### Effets dynamiques par profil

| Profil       | Compresseur (seuil, ratio, attaque / relâchement, compensation) | Saturation (drive / gain de sortie) | Pourquoi |
|--------------|------------------------------------------------------------------|-------------------------------------|----------|
| `Rock`       | −20 dBFS, 3:1, 10 / 120 ms, 0 dB                                 | +6 dB / −1 dB                       | densité et grain |
| `Pop`        | −20 dBFS, 2,5:1, 15 / 150 ms, +2,5 dB                            | —                                   | genre très compressé, mais propre |
| `HipHop`     | −20 dBFS, 3:1, 20 / 100 ms, +1,5 dB                              | +3 dB / 0 dB                        | attaque lente pour garder le punch, chaleur des basses |
| `Electronic` | −20 dBFS, 4:1, 5 / 80 ms, +1,5 dB                                | +3 dB / 0 dB                        | son dense et serré |
| `Vocal`      | −22 dBFS, 3:1, 5 / 150 ms, +3 dB                                 | —                                   | régularité et intelligibilité de la voix |
| `Jazz`       | —                                                                | +2 dB / 0 dB                        | chaleur légère sans écraser la dynamique |

`Flat`, `Classical` et `Acoustic` gardent la dynamique d'origine. Tous les compresseurs ont un genou de 6 dB.

La compensation du compresseur et la saturation relèvent le niveau pour contrebalancer la baisse du gain automatique : à −18 dBFS et 1 kHz, chaque profil avec effets rend le niveau d'entrée à moins de 0,4 dB près. La saturation (tangente hyperbolique normalisée) arrondit les pics et ajoute des harmoniques impaires. Son effet est surtout audible dans les basses, car le gain automatique compense le drive là où l'égalisation est sous son pic. Les réglages sont dans `profileCompression` et `profileSaturation` ([profiles.go](profiles.go)).

### Égaliseur paramétrique

```go
func Equalizer(src io.Reader, sampleRate, channels int, bands []Band) io.Reader

type Band struct {
	Type   BandType // LowShelf, Peaking, HighShelf ou Gain
	FreqHz float64  // fréquence de coupure (shelf) ou centrale (peaking)
	GainDB float64  // gain en dB, ramené dans [-24, +24]
	Q      float64  // facteur de qualité ; <= 0 → 1/√2
}
```

Exemple, une courbe « sourire » pour du rock :

```go
rock := []audioprofile.Band{
	{Type: audioprofile.LowShelf, FreqHz: 100, GainDB: 5},
	{Type: audioprofile.Peaking, FreqHz: 400, GainDB: -4, Q: 1},  // creux anti-« boue »
	{Type: audioprofile.Peaking, FreqHz: 3000, GainDB: 4, Q: 1},  // présence
	{Type: audioprofile.HighShelf, FreqHz: 8000, GainDB: 3},
}
io.Copy(out, audioprofile.Equalizer(in, 44100, 2, rock))
```

- **Types** : `LowShelf` et `HighShelf` agissent sous / au-dessus de `FreqHz` ; `Peaking` est une cloche centrée sur `FreqHz` ; `Gain` est un gain global sur tout le spectre (`FreqHz` et `Q` sont ignorés).
- **Q** : plus il est grand, plus la bande est étroite. Pour une cloche, le gain demandé est atteint exactement à la fréquence centrale quel que soit Q.
- Les bandes sont appliquées dans l'ordre, en série, en une seule passe sur les échantillons.
- Sont ignorées les bandes sans effet : gain nul ou NaN, ou (hors `Gain`) fréquence <= 0 ou >= Nyquist. S'il n'en reste aucune, le flux est renvoyé tel quel.
- Filtres biquad en forme directe II transposée (formules du *RBJ Audio EQ Cookbook*).

### Compresseur

```go
func Compressor(src io.Reader, sampleRate, channels int, c CompressorSettings) io.Reader

type CompressorSettings struct {
	ThresholdDB float64 // seuil en dBFS (ex. -20)
	Ratio       float64 // rapport (3 = 3:1) ; <= 1 = pas de compression
	AttackMs    float64 // attaque en ms ; <= 0 → 10
	ReleaseMs   float64 // relâchement en ms ; <= 0 → 100
	KneeDB      float64 // largeur du genou (0 = coude franc, 6 = doux)
	MakeupDB    float64 // gain de compensation après compression
}
```

```go
io.Copy(out, audioprofile.Compressor(in, 44100, 2, audioprofile.CompressorSettings{
	ThresholdDB: -20, Ratio: 3, AttackMs: 10, ReleaseMs: 120, KneeDB: 6, MakeupDB: 2,
}))
```

- Le niveau est détecté sur la **crête du canal le plus fort** de chaque trame, et le **même gain** est appliqué à tous les canaux (détection liée) : l'image stéréo est conservée.
- Le gain est calculé en dB (genou quadratique), puis lissé : attaque quand la réduction augmente, relâchement quand elle diminue.
- Sans effet (flux renvoyé tel quel) si `Ratio <= 1` et `MakeupDB = 0`.

### Préréglages à une bande

```go
func BassBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
func MidBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
func TrebleBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
```

| Fonction      | Filtre                                          |
|---------------|-------------------------------------------------|
| `BassBoost`   | low-shelf à **120 Hz**                          |
| `MidBoost`    | cloche (peaking) à **1 kHz**, Q = 0,7 (large, environ 2 octaves) |
| `TrebleBoost` | high-shelf à **4 kHz**                          |

`intensity` va de `-1.0` (bande −15 dB) à `+1.0` (bande +15 dB) ; `0` = signal inchangé, et les valeurs hors intervalle sont ramenées dans `[-1, 1]`. Pour `TrebleBoost`, le flux est renvoyé tel quel si la coupure dépasse Nyquist (`sampleRate < 8000`).

## Ligne de commande

L'outil ([cmd/audioprofile](cmd/audioprofile/main.go)) lit du PCM s16le sur l'entrée standard et écrit le résultat sur la sortie standard, donc il se place directement dans un pipe avec ffmpeg.

```sh
go build -o audioprofile ./cmd/audioprofile    # ou : go run ./cmd/audioprofile ...
```

```sh
# Écouter un morceau avec le profil rock
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

# Produire un fichier WAV, avec un profil à moitié dosé
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile pop -intensity 0.5 \
  | ffmpeg -f s16le -ar 44100 -ac 2 -i - musique_pop.wav

# Exagérer un profil pour mieux l'entendre (jusqu'à 3)
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile rock -intensity 2 \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

# Comparer sans pipe, avec des fichiers bruts
./audioprofile -profile jazz < musique.raw > musique_jazz.raw
```

| Option        | Défaut  | Description                                                       |
|---------------|---------|-------------------------------------------------------------------|
| `-profile`    | —       | Profil à appliquer (obligatoire, insensible à la casse)           |
| `-intensity`  | `1`     | `0` = aucun effet, `1` = profil complet, jusqu'à `3` pour l'exagérer |
| `-rate`       | `44100` | Fréquence d'échantillonnage en Hz                                 |
| `-channels`   | `2`     | Nombre de canaux                                                  |
| `-list`       | —       | Affiche les profils disponibles et quitte                         |

**`-rate` et `-channels` doivent correspondre aux options `-ar` et `-ac` de ffmpeg** : le flux brut n'a pas d'en-tête, l'outil ne peut pas les deviner. Une valeur incorrecte ne provoque pas d'erreur mais donne un son faux (basses et aigus mal placés, canaux mélangés).

L'outil refuse de lire sur un terminal sans pipe ni redirection, et d'écrire du PCM brut sur un terminal. Les erreurs (profil inconnu, option invalide) sortent avec le code 2.

## Tests

```sh
go test ./...
```

Les tests s'appuient sur des signaux synthétiques (sinus) et mesurent le gain réel obtenu à chaque fréquence :

| Fichier | Ce qui est vérifié |
|---------|--------------------|
| `bassboost_test.go`, `midboost_test.go`, `trebleboost_test.go` | gains et atténuations des préréglages, signal intact à intensité nulle ou au-delà de Nyquist |
| `equalizer_test.go` | courbe multibande (gains à moins de 0,7 dB), effet de Q, bandes ignorées, limitation à ±24 dB, lecture fragmentée identique à une lecture en un bloc |
| `dynamics_test.go` | courbe statique du compresseur, genou doux, attaque / relâchement progressifs, détection liée stéréo, coude franc sans NaN, compensation seule, mise à l'échelle par l'intensité |
| `saturation_test.go` | forme de la saturation (pleine échelle conservée, monotone, impaire), harmoniques impaires uniquement, croissance avec l'intensité, niveau de Rock, pas d'écrêtage |
| `profiles_test.go` | noms et `ParseProfile`, identité de `Flat` et de l'intensité nulle, pic à 0 dB, forme des courbes, niveau des profils avec effets, aucun écrêtage à −3 dBFS sur tout le spectre, y compris à intensité exagérée (2 et 3), effet croissant et plafonné au-delà de 1 |
| `cmd/audioprofile/main_test.go` | liste, traitement, identité de `flat`, mono et fréquence, erreurs, entrée vide |

## Limites connues

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- Au-delà de l'intensité 1, le volume baisse et le niveau n'est plus calibré sur l'entrée ; un signal à pleine échelle (0 dBFS) peut écrêter plus largement qu'à l'intensité 1 (Pop, Vocal).
- Les réglages des profils n'ont pas été validés à l'oreille : ils sont vérifiés par mesure (forme des courbes, niveaux) mais restent des points de départ à affiner.
- Le compresseur n'a pas d'anticipation (look-ahead) et il n'y a pas de limiteur : un pic très bref passe pendant le temps d'attaque. Un signal à pleine échelle (0 dBFS) dès son premier échantillon peut ainsi écrêter brièvement (Pop, Vocal) ; à −3 dBFS, aucun profil n'écrête. En dehors des profils, un fort gain d'égalisation sur un signal déjà proche de la saturation est écrêté (distorsion).
- La saturation n'est pas suréchantillonnée : à fort drive sur des aigus très forts, un peu de repliement de spectre (aliasing) est possible.
- Le dosage de la saturation de Rock dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
