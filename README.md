# poc-audio-profiles

Preuve de concept en Go d'un **égaliseur trois bandes (basses / médiums / aigus)** appliqué à un flux audio PCM, au fil de l'eau.

Le paquet `bassboost` expose trois fonctions qui enveloppent un `io.Reader` PCM et renvoient un autre `io.Reader` :

- `BassBoost` : renforce ou atténue les **basses** ;
- `MidBoost` : renforce ou atténue les **fréquences intermédiaires** (médiums) ;
- `TrebleBoost` : renforce ou atténue les **aigus**.

Elles sont chaînables : `TrebleBoost(MidBoost(BassBoost(src, ...), ...), ...)`.

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
- **TestMidGain** : `MidBoost` agit à 1 kHz sans toucher 40 Hz ni 15 kHz.
- **TestMidZeroIsIdentity** : intensité nulle = signal intact.
- **TestTrebleGain** : `TrebleBoost` amplifie/atténue un sinus à 12 kHz sans toucher un sinus à 100 Hz.
- **TestTrebleZeroIsIdentity**, **TestTrebleLowSampleRateIsIdentity** : signal intact si l'effet est nul ou si la coupure dépasse Nyquist.

## Limites connues (POC)

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Fréquence de coupure et gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`).
- Pas de limiteur : un fort gain sur un signal déjà proche de la saturation sera écrêté (distorsion).
