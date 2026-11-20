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

	"poc-audio-profiles"
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

L'import réel dépend du chemin du module (voir `go.mod`) : le paquet s'appelle `bassboost`.

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
- **Compresseur (Rock)** : juste après l'égalisation, Rock applique un compresseur (seuil −20 dBFS, ratio 3:1, attaque 10 ms, relâchement 120 ms, genou 6 dB). Le ratio suit l'intensité (de 1:1 à 3:1). Sur un sinus à 1 kHz, l'écart de gain entre un signal faible (−40 dBFS) et un fort (−6 dBFS) passe de 0,6 dB à 5,3 dB.
- **Saturation douce (Rock)** : après le compresseur, le profil Rock pousse le signal dans une saturation en tangente hyperbolique (+6 dB de drive), puis applique un gain de sortie de −1 dB. Les pics sont arrondis au lieu d'être écrêtés, ce qui ajoute des harmoniques impaires (grain, chaleur). Cela relève aussi les signaux faibles, ce qui compense la baisse de volume du gain automatique : à −18 dBFS et 1 kHz, Rock rend à peu près le niveau d'entrée (mesuré à +0,04 dB). Drive et gain de sortie suivent l'intensité. Les autres profils n'ont pas de saturation. Voir [saturation.go](saturation.go).
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

### Tester avec ffmpeg

```sh
# Convertir un MP3 en PCM brut
ffmpeg -i musique.mp3 -f s16le -ar 44100 -ac 2 musique.raw

# Après traitement, réécouter le résultat
ffplay -f s16le -ar 44100 -ac 2 musique_boost.raw
```

## Tests

```sh
go test -v ./...
```

Les tests vérifient notamment :

- **TestGain** : un sinus à 40 Hz est fortement amplifié, un sinus à 5 kHz reste quasi inchangé.
- **TestCut** : avec une intensité négative, les graves sont atténués, les aigus inchangés.
- **TestZeroIsIdentity** : une intensité de 0 laisse le signal intact.
- **TestStereoOddChunks** : le recollage des trames fonctionne avec des lectures fragmentées en stéréo.
- **TestEqualizerMultiBand** : une courbe à quatre bandes donne les gains attendus à chaque fréquence (à moins de 0,7 dB).
- **TestEqualizerQ**, **TestEqualizerIgnoredBands**, **TestEqualizerGainClamped**, **TestEqualizerStereoOddChunks** : effet de Q, bandes ignorées, limitation du gain à ±24 dB, lecture fragmentée identique à une lecture en un bloc.
- **TestCompressor\*** / **TestScaleCompression** / **TestRockCompressesDynamics** : courbe statique (gains attendus sous, au et au-dessus du seuil), genou doux, attaque et relâchement progressifs, détection liée stéréo, coude franc sans NaN, compensation seule, lecture fragmentée, mise à l'échelle par l'intensité, effet réel sur Rock.
- **TestSaturator\*** / **TestSaturation\*** / **TestRock\*** / **TestSaturatedOutputNeverClips** : forme de la saturation (pleine échelle conservée, monotone, impaire), harmoniques impaires uniquement, croissance avec l'intensité, niveau de Rock proche de l'entrée, pas d'écrêtage sur un signal très fort.
- **TestProfile\*** : noms et `ParseProfile`, profils identiques au signal d'origine (`Flat`, intensité nulle), pic à 0 dB après compensation, forme de la courbe (Rock en « sourire », Vocal, HipHop), lecture fragmentée.
- **TestMidGain** : `MidBoost` agit à 1 kHz sans toucher 40 Hz ni 15 kHz.
- **TestMidZeroIsIdentity** : intensité nulle = signal intact.
- **TestTrebleGain** : `TrebleBoost` amplifie/atténue un sinus à 12 kHz sans toucher un sinus à 100 Hz.
- **TestTrebleZeroIsIdentity**, **TestTrebleLowSampleRateIsIdentity** : signal intact si l'effet est nul ou si la coupure dépasse Nyquist.

## Limites connues (POC)

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- Les profils réduisent le volume (gain automatique anti-écrêtage) ; Rock le compense en partie par sa saturation. Il n'y a pas de limiteur à anticipation (look-ahead) : pendant le temps d'attaque du compresseur, un pic très bref passe, la saturation de Rock l'arrondit ensuite.
- La saturation n'est pas suréchantillonnée : à fort drive sur des aigus très forts, un peu de repliement de spectre (aliasing) est possible.
- Le dosage de la saturation de Rock dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
- Pas de limiteur : un fort gain sur un signal déjà proche de la saturation sera écrêté (distorsion).
