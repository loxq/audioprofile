# poc-audio-profiles

Preuve de concept en Go d'un **égaliseur trois bandes (basses / médiums / aigus)** appliqué à un flux audio PCM, au fil de l'eau.

Le paquet `bassboost` expose trois fonctions qui enveloppent un `io.Reader` PCM et renvoient un autre `io.Reader` :

- `BassBoost` : renforce ou atténue les **basses** ;
- `MidBoost` : renforce ou atténue les **fréquences intermédiaires** (médiums) ;
- `TrebleBoost` : renforce ou atténue les **aigus**.

- `Compressor` : **compresseur de dynamique** (seuil, ratio, attaque, relâchement, genou, compensation) ;
- `ApplyProfile` : applique un **profil prédéfini** (Rock, Pop, Classique, Jazz…) avec une intensité réglable ;
- `Equalizer` : **égaliseur paramétrique** à bandes libres (type, fréquence, gain en dB, Q), appliquées en une seule passe.

`BassBoost`, `MidBoost` et `TrebleBoost` sont des préréglages à une bande, réglés par une simple intensité. Toutes ces fonctions sont chaînables : `TrebleBoost(MidBoost(BassBoost(src, ...), ...), ...)`. Pour plusieurs bandes, `Equalizer` est plus efficace (une seule boucle sur les échantillons).

## Fonctionnement

- Filtres biquad (formules du *RBJ Audio EQ Cookbook*, pente S = 1), forme directe II transposée :
  - `BassBoost` : **low-shelf** à **120 Hz** ;
  - `MidBoost` : filtre en **cloche** (peaking) centré à **1 kHz**, Q = 0,7 (large, environ 2 octaves) ;
  - `TrebleBoost` : **high-shelf** à **4 kHz**.
- Gain maximal : **±15 dB** (atteint pour une intensité de ±1).
- Un état de filtre par canal, donc compatible mono, stéréo et multicanal.
- Traitement en flux : lecture par blocs de 4096 trames, sans charger le fichier entier en mémoire.
- Écrêtage des échantillons à l'intervalle 16 bits pour éviter tout débordement d'entier.

## Format d'entrée / sortie

PCM **16 bits signé, little-endian, entrelacé** (s16le). Aucun en-tête n'est géré : ni WAV, ni autre conteneur. Seules les données brutes sont attendues.

Une éventuelle trame incomplète en fin de flux est ignorée.

## Utilisation

```go
package main

import (
	"io"
	"os"

	bassboost "github.com/loxq/audioprofile"
)

func main() {
	in, _ := os.Open("musique.raw") // s16le, 44,1 kHz, stéréo
	defer in.Close()

	out, _ := os.Create("musique_boost.raw")
	defer out.Close()

	// +0.8 : basses fortement renforcées
	io.Copy(out, bassboost.BassBoost(in, 44100, 2, 0.8))
}
```

Le chemin du module est `github.com/loxq/audioprofile` mais le paquet s'appelle `bassboost` : l'alias `bassboost` à l'import rend cela explicite.

### Signatures

```go
func BassBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
func MidBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
func TrebleBoost(src io.Reader, sampleRate, channels int, intensity float64) io.Reader
```

Les paramètres sont identiques pour les trois fonctions.

| Paramètre    | Description                                                                                  |
|--------------|----------------------------------------------------------------------------------------------|
| `src`        | Flux PCM s16le entrelacé                                                                     |
| `sampleRate` | Fréquence d'échantillonnage en Hz (ex. `44100`)                                              |
| `channels`   | Nombre de canaux (1 = mono, 2 = stéréo…). Une valeur < 1 est ramenée à 1                     |
| `intensity`  | De `-1.0` (bande −15 dB) à `+1.0` (bande +15 dB). `0` = signal inchangé. Valeurs hors intervalle ramenées dans `[-1, 1]` |

Si `intensity == 0` ou `sampleRate <= 0`, le flux est renvoyé tel quel (pas de filtrage). C'est aussi le cas quand la fréquence de coupure dépasse Nyquist (pour `TrebleBoost`, `sampleRate < 8000`).

### Égaliseur paramétrique

```go
func Equalizer(src io.Reader, sampleRate, channels int, bands []Band) io.Reader

type Band struct {
	Type   BandType // LowShelf, Peaking ou HighShelf
	FreqHz float64  // fréquence de coupure (shelf) ou centrale (peaking)
	GainDB float64  // gain en dB, ramené dans [-24, +24]
	Q      float64  // facteur de qualité ; <= 0 → 1/√2
}
```

Exemple, une courbe « sourire » pour du rock :

```go
rock := []bassboost.Band{
	{Type: bassboost.LowShelf, FreqHz: 100, GainDB: 5},
	{Type: bassboost.Peaking, FreqHz: 400, GainDB: -4, Q: 1},  // creux anti-« boue »
	{Type: bassboost.Peaking, FreqHz: 3000, GainDB: 4, Q: 1},  // présence
	{Type: bassboost.HighShelf, FreqHz: 8000, GainDB: 3},
}
io.Copy(out, bassboost.Equalizer(in, 44100, 2, rock))
```

- **Q** : plus il est grand, plus la bande est étroite. Pour une cloche (`Peaking`), le gain demandé est atteint exactement à la fréquence centrale quel que soit Q.
- Les bandes sans effet (gain nul ou NaN, fréquence <= 0 ou >= Nyquist) sont ignorées. S'il n'en reste aucune, le flux est renvoyé tel quel.
- Les bandes sont appliquées dans l'ordre, en série.

### Profils prédéfinis

```go
func ApplyProfile(src io.Reader, sampleRate, channels int, p Profile, intensity float64) io.Reader
```

```go
io.Copy(out, bassboost.ApplyProfile(in, 44100, 2, bassboost.Rock, 0.8))
```

| Profil       | Nom (`ParseProfile`) | Caractère                                              |
|--------------|----------------------|--------------------------------------------------------|
| `Flat`       | `flat`               | Aucune correction                                      |
| `Rock`       | `rock`               | Sourire : basses et aigus relevés, médiums creusés     |
| `Pop`        | `pop`                | Voix en avant, basses et brillance modérées            |
| `Classical`  | `classical`          | Naturel, léger renfort des extrêmes                    |
| `Jazz`       | `jazz`               | Chaud, médiums doux, aigus aérés                       |
| `HipHop`     | `hiphop`             | Basses profondes, voix détachée                        |
| `Electronic` | `electronic`         | Sub-basses et aigus marqués, médiums creusés           |
| `Acoustic`   | `acoustic`           | Naturel, présence des cordes et de l'air               |
| `Vocal`      | `vocal`              | Voix : intelligibilité, graves réduits                 |

- **Intensité** : de `0` (aucun effet) à `1` (profil complet) ; elle met les gains à l'échelle. Les valeurs hors de cet intervalle sont ramenées dedans.
- **Gain automatique** : pour éviter l'écrêtage, un gain global négatif est ajouté afin que la fréquence la plus renforcée reste à 0 dB. Le volume perçu baisse donc d'autant, à compenser en aval si besoin.
- **Effets dynamiques** : certains profils ajoutent, après l'égalisation, un compresseur puis une saturation douce avec gain de sortie (voir plus bas). Leur dosage suit l'intensité : le ratio va de 1:1 au ratio du profil, et le drive de 0 dB à sa valeur. Les profils qui ne figurent pas dans le tableau (Flat, Classical, Acoustic) gardent la dynamique d'origine.
- **Valeurs** : les courbes sont des points de départ raisonnables, pas une norme (il n'existe pas de standard officiel). Elles sont définies dans [profiles.go](profiles.go).
- **Utilitaires** : `Profiles()` liste les profils, `ParseProfile("rock")` en retrouve un par son nom (utile pour un flag CLI), et `p.Bands()` renvoie les bandes d'un profil pour partir de lui avec `Equalizer`.

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
io.Copy(out, bassboost.Compressor(in, 44100, 2, bassboost.CompressorSettings{
	ThresholdDB: -20, Ratio: 3, AttackMs: 10, ReleaseMs: 120, KneeDB: 6, MakeupDB: 2,
}))
```

- Le niveau est détecté sur la **crête du canal le plus fort** de chaque trame, et le **même gain** est appliqué à tous les canaux (détection liée) : l'image stéréo est conservée.
- Le gain est calculé en dB (genou quadratique), puis lissé : attaque quand la réduction augmente, relâchement quand elle diminue.
- Sans effet (flux renvoyé tel quel) si `sampleRate <= 0`, ou si `Ratio <= 1` et `MakeupDB = 0`.
- Dans `ApplyProfile`, la chaîne est : égalisation → compresseur → saturation → gain de sortie.

#### Effets dynamiques par profil

| Profil       | Compresseur (seuil, ratio, attaque / relâchement, compensation) | Saturation (drive / gain de sortie) | Pourquoi |
|--------------|------------------------------------------------------------------|-------------------------------------|----------|
| `Rock`       | −20 dBFS, 3:1, 10 / 120 ms, 0 dB                                 | +6 dB / −1 dB                       | densité et grain |
| `Pop`        | −20 dBFS, 2,5:1, 15 / 150 ms, +2,5 dB                            | —                                   | genre très compressé, mais propre |
| `HipHop`     | −20 dBFS, 3:1, 20 / 100 ms, +1,5 dB                              | +3 dB / 0 dB                        | attaque lente pour garder le punch, chaleur des basses |
| `Electronic` | −20 dBFS, 4:1, 5 / 80 ms, +1,5 dB                                | +3 dB / 0 dB                        | son dense et serré |
| `Vocal`      | −22 dBFS, 3:1, 5 / 150 ms, +3 dB                                 | —                                   | régularité et intelligibilité de la voix |
| `Jazz`       | —                                                                | +2 dB / 0 dB                        | chaleur légère sans écraser la dynamique |

Tous ont un genou de 6 dB. La compensation du compresseur et la saturation relèvent le niveau pour contrebalancer la baisse du gain automatique : à −18 dBFS et 1 kHz, chaque profil avec effets rend le niveau d'entrée à moins de 0,4 dB près. La saturation, tangente hyperbolique normalisée, arrondit les pics et ajoute des harmoniques impaires ; son effet est surtout audible dans les basses, car le gain automatique compense le drive là où l'égalisation est sous son pic. Les réglages sont dans `profileCompression` et `profileSaturation` ([profiles.go](profiles.go)).

## Ligne de commande : `audioprofile`

Petit outil ([cmd/audioprofile](cmd/audioprofile/main.go)) pour tester les profils sur un vrai fichier. Il lit du PCM s16le sur l'entrée standard et écrit le résultat sur la sortie standard, donc il se place directement dans un pipe avec ffmpeg.

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

# Comparer sans pipe, avec des fichiers bruts
./audioprofile -profile jazz < musique.raw > musique_jazz.raw
```

| Option        | Défaut  | Description                                                       |
|---------------|---------|-------------------------------------------------------------------|
| `-profile`    | —       | Profil à appliquer (obligatoire, insensible à la casse)           |
| `-intensity`  | `1`     | De `0` (aucun effet) à `1` (profil complet)                       |
| `-rate`       | `44100` | Fréquence d'échantillonnage en Hz                                 |
| `-channels`   | `2`     | Nombre de canaux                                                  |
| `-list`       | —       | Affiche les profils disponibles et quitte                         |

**`-rate` et `-channels` doivent correspondre aux options `-ar` et `-ac` de ffmpeg** : le flux brut n'a pas d'en-tête, l'outil ne peut pas les deviner. Une valeur incorrecte ne provoque pas d'erreur mais donne un son faux (basses et aigus mal placés, canaux mélangés).

L'outil refuse de lire sur un terminal sans pipe ni redirection, et d'écrire du PCM brut sur un terminal. Les erreurs (profil inconnu, option invalide) sortent avec le code 2.

## Tests

```sh
go test -v ./...
```

Le CLI a ses propres tests dans [cmd/audioprofile](cmd/audioprofile/main_test.go) (liste, traitement, identité de `flat`, mono/rate, erreurs, entrée vide).

Les tests vérifient notamment :

- **TestGain** : un sinus à 40 Hz est fortement amplifié, un sinus à 5 kHz reste quasi inchangé.
- **TestCut** : avec une intensité négative, les graves sont atténués, les aigus inchangés.
- **TestZeroIsIdentity** : une intensité de 0 laisse le signal intact.
- **TestStereoOddChunks** : le recollage des trames fonctionne avec des lectures fragmentées en stéréo.
- **TestEqualizerMultiBand** : une courbe à quatre bandes donne les gains attendus à chaque fréquence (à moins de 0,7 dB).
- **TestEqualizerQ**, **TestEqualizerIgnoredBands**, **TestEqualizerGainClamped**, **TestEqualizerStereoOddChunks** : effet de Q, bandes ignorées, limitation du gain à ±24 dB, lecture fragmentée identique à une lecture en un bloc.
- **TestCompressor\*** / **TestScaleCompression** / **TestRockCompressesDynamics** : courbe statique (gains attendus sous, au et au-dessus du seuil), genou doux, attaque et relâchement progressifs, détection liée stéréo, coude franc sans NaN, compensation seule, lecture fragmentée, mise à l'échelle par l'intensité, effet réel sur Rock.
- **TestSaturator\*** / **TestSaturation\*** / **TestRock\*** / **TestSaturatedOutputNeverClips** : forme de la saturation (pleine échelle conservée, monotone, impaire), harmoniques impaires uniquement, croissance avec l'intensité, niveau de Rock proche de l'entrée, pas d'écrêtage sur un signal très fort.
- **TestProfilesLevelNearUnity**, **TestProfilesNoClipping** : niveau des profils avec effets proche de l'entrée à −18 dBFS, aucun écrêtage à −3 dBFS sur tout le spectre.
- **TestProfile\*** : noms et `ParseProfile`, profils identiques au signal d'origine (`Flat`, intensité nulle), pic à 0 dB après compensation, forme de la courbe (Rock en « sourire », Vocal, HipHop), lecture fragmentée.
- **TestMidGain** : `MidBoost` agit à 1 kHz sans toucher 40 Hz ni 15 kHz.
- **TestMidZeroIsIdentity** : intensité nulle = signal intact.
- **TestTrebleGain** : `TrebleBoost` amplifie/atténue un sinus à 12 kHz sans toucher un sinus à 100 Hz.
- **TestTrebleZeroIsIdentity**, **TestTrebleLowSampleRateIsIdentity** : signal intact si l'effet est nul ou si la coupure dépasse Nyquist.

## Limites connues (POC)

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- Les profils réduisent le volume (gain automatique anti-écrêtage) ; les profils avec effets le compensent en partie (voir le tableau). Il n'y a pas de limiteur à anticipation (look-ahead) : pendant le temps d'attaque du compresseur, un pic très bref passe, la saturation de Rock l'arrondit ensuite.
- Sans anticipation, un signal à pleine échelle (0 dBFS) dès son premier échantillon peut écrêter brièvement sur Pop et Vocal, avant que l'attaque du compresseur ne réagisse (quelques échantillons). À −3 dBFS, aucun profil n'écrête.
- La saturation n'est pas suréchantillonnée : à fort drive sur des aigus très forts, un peu de repliement de spectre (aliasing) est possible.
- Le dosage de la saturation de Rock dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
- Pas de limiteur : un fort gain sur un signal déjà proche de la saturation sera écrêté (distorsion).
