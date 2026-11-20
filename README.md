# audioprofile

Bibliothèque Go (et petit outil en ligne de commande) pour donner une couleur à un flux audio PCM : égaliseur, profils de genre (Rock, Pop, Jazz…), compresseur et saturation de type tube. Tout se fait **au fil de l'eau** sur des `io.Reader`, sans charger le fichier en mémoire.

Ce que le paquet `audioprofile` propose :

- `ApplyProfile` : applique un **profil prédéfini** (Rock, Pop, Classical, Jazz, HipHop, Electronic, Acoustic, Vocal, Tube) avec une intensité réglable ;
- `Equalizer` : **égaliseur paramétrique** à bandes libres (type, fréquence, gain en dB, Q) ;
- `Compressor` : **compresseur de dynamique** (seuil, ratio, attaque, relâchement, genou, compensation) ;
- `TubeAmp` : **ampli à tubes simulé** (étages de saturation asymétriques en cascade, correcteur de timbre, baffle optionnel), avec les préréglages `AmpClean`, `AmpCrunch` et `AmpLead` ;
- `BassBoost`, `MidBoost`, `TrebleBoost` : **préréglages à une bande** (basses, médiums, aigus) réglés par une simple intensité ;
- `audioprofile` (commande) : applique un profil ou un préréglage d'ampli sur un flux lu sur l'entrée standard, pour se brancher sur un pipe `ffmpeg`.

Toutes ces fonctions prennent un `io.Reader` et en renvoient un autre : elles s'enchaînent, par exemple `TrebleBoost(MidBoost(BassBoost(src, …), …), …)`.

## Démarrage rapide

```sh
git clone https://github.com/loxq/audioprofile && cd audioprofile
go build -o audioprofile ./cmd/audioprofile

# Écouter un morceau avec le profil rock
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile rock \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

# Ou la chaleur d'un ampli à tubes (intensité 2 : plus audible sur un morceau calme)
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -amp crunch -intensity 2 \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
```

Les profils disponibles s'affichent avec `./audioprofile -list`, les préréglages d'ampli avec `./audioprofile -list-amps`. Détails dans [Ligne de commande](#ligne-de-commande).

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
| `Tube`       | `tube`               | Chaleur d'ampli à tubes sur un morceau complet     |

- **Intensité** : `0` = aucun effet, `1` = profil complet. Elle met à l'échelle les gains, le ratio du compresseur et le drive de la saturation.
- **Exagérer un profil** : une intensité **au-delà de 1** (jusqu'à `MaxIntensity` = 3) amplifie l'effet, utile pour juger d'un profil à l'oreille ou pour un rendu plus marqué. Le gain automatique grandit avec l'effet, donc le volume baisse (de 1 à 7 dB à l'intensité 3 selon le profil, mesuré à −18 dBFS et 1 kHz) ; `Tube` fait exception : sa saturation relève les signaux faibles (+8 dB à 3). La compensation du compresseur, le gain de sortie positif et l'asymétrie de la saturation ne croissent pas au-delà de leur valeur d'intensité 1 : les deux premiers pour éviter l'écrêtage, le dernier parce qu'à fort drive l'asymétrie rendrait le niveau quasi nul. Les valeurs hors de [0, `MaxIntensity`] sont ramenées dedans.
- **Gain automatique** : pour éviter l'écrêtage, un gain global négatif est ajouté afin que la fréquence la plus renforcée reste à 0 dB. Le volume perçu baisse donc d'autant, sauf pour les profils avec effets (voir plus bas).
- **Chaîne de traitement** : égalisation → compresseur → saturation → gain de sortie. Les deux derniers étages ne concernent que certains profils.
- **Valeurs** : les courbes sont des points de départ raisonnables, pas une norme (il n'existe pas de standard officiel). Elles sont définies dans [profiles.go](profiles.go).
- **Utilitaires** : `Profiles()` liste les profils, `ParseProfile("rock")` en retrouve un par son nom (insensible à la casse), `p.String()` donne son nom, et `p.Bands()` renvoie une copie de ses bandes d'égalisation pour partir d'elles avec `Equalizer`.

#### Effets dynamiques par profil

| Profil       | Compresseur (seuil, ratio, attaque / relâchement, compensation) | Saturation (drive / asymétrie / gain de sortie) | Pourquoi |
|--------------|------------------------------------------------------------------|-------------------------------------|----------|
| `Rock`       | −20 dBFS, 3:1, 10 / 120 ms, 0 dB                                 | +6 dB / — / −1 dB                   | densité et grain |
| `Pop`        | −20 dBFS, 2,5:1, 15 / 150 ms, +2,5 dB                            | —                                   | genre très compressé, mais propre |
| `HipHop`     | −20 dBFS, 3:1, 20 / 100 ms, +1,5 dB                              | +3 dB / — / 0 dB                    | attaque lente pour garder le punch, chaleur des basses |
| `Electronic` | −20 dBFS, 4:1, 5 / 80 ms, +1,5 dB                                | +3 dB / — / 0 dB                    | son dense et serré |
| `Vocal`      | −22 dBFS, 3:1, 5 / 150 ms, +3 dB                                 | —                                   | régularité et intelligibilité de la voix |
| `Tube`       | −20 dBFS, 1,8:1, 15 / 150 ms, 0 dB                               | +6 dB / 0,4 / 0 dB                  | léger affaissement (« sag ») et harmoniques paires ; courbe douce : basses +2 dB, aigus −2 dB |
| `Jazz`       | —                                                                | +3 dB / 0,3 / +2 dB                 | chaleur de tube (harmoniques paires) sans écraser la dynamique |

`Flat`, `Classical` et `Acoustic` gardent la dynamique d'origine. Tous les compresseurs ont un genou de 6 dB.

La compensation du compresseur et la saturation relèvent le niveau pour contrebalancer la baisse du gain automatique : à −18 dBFS et 1 kHz, chaque profil avec effets rend le niveau d'entrée à moins de 0,4 dB près. Les réglages sont dans `profileCompression` et `profileSaturation` ([profiles.go](profiles.go)).

#### Saturation de type tube

La saturation ([saturation.go](saturation.go)) applique, sur le signal normalisé, `f(x) = tanh(g·x + β) − tanh(β)` puis normalise pour que la pleine échelle reste à la pleine échelle (`g` = drive, `β` = asymétrie). Elle arrondit les pics au lieu de les écrêter.

- **Asymétrie** : à `β = 0` (Rock, HipHop, Electronic), la courbe est symétrique et ne produit que des **harmoniques impaires** (3e, 5e…), plutôt dures. Avec `β > 0` (Jazz, Tube), elle est asymétrique comme celle d'un tube et ajoute des **harmoniques paires** (2e, 4e…), perçues comme plus chaudes. À 200 Hz, 2e harmonique de 1,9 % (Jazz) et 3,7 % (Tube) à −18 dBFS, 8,8 % et 9,6 % à −3 dBFS, contre 0 pour les autres profils. Un filtre passe-haut (7 Hz) retire la composante continue qu'introduit l'asymétrie, et le silence reste un silence exact.
- **`β` est exprimé dans l'argument de `tanh`, donc indépendant du drive** : le gain des petits signaux vaut `g·sech²(β)` (au facteur de normalisation près) et grandit avec le drive. Une asymétrie exprimée dans le domaine d'entrée, `tanh(g·(x+b))`, repousse au contraire le point de fonctionnement dans la zone saturée à mesure que `g` grandit : mise à l'échelle avec l'intensité, elle rendait `Jazz` quasi muet à −18 dB et `Tube` à −49 dB à l'intensité 3. Un test de régression garde ce comportement.
- **Suréchantillonnage ×4** : la non-linéarité s'exécute à 4 fois la fréquence d'échantillonnage, entre deux filtres passe-bas d'ordre 8 (coupure 0,43 × fs). Les harmoniques qui dépasseraient Nyquist sont atténuées avant de se replier dans le spectre audible : sur un sinus de 15 kHz poussé de 6 dB, le repliement à 900 Hz tombe de 6,4 % à 0,35 % (−25 dB). Ces filtres arrondissent légèrement les aigus extrêmes des profils saturés (mesuré à 44,1 kHz : −0,2 dB à 15 kHz, −3 dB à 18 kHz, −10 dB à 20 kHz).
- **Plusieurs étages** : l'étage peut être mis en cascade (voir [Ampli à tubes](#ampli-à-tubes)), avec inversion de phase et couplage entre étages.
- **Plafond doux** : une onde saturée, une fois limitée en bande, peut dépasser la pleine échelle (la fondamentale d'une onde carrée vaut 1,27 fois son sommet). Un plafond doux arrondit ces pics au lieu de les laisser écrêter net : la saturation ne produit jamais la pleine échelle.
- **Un état par canal** : les filtres et le filtre anti-continu sont propres à chaque canal ; un canal silencieux reste silencieux (pas de diaphonie).
- **Le grain s'entend surtout dans les basses** : le gain automatique compense le drive là où l'égalisation est sous son pic.

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

### Limiteur

```go
type LimiterSettings struct {
	CeilingDB   float64 // plafond des crêtes en dBFS, dans [-40, 0] (0 = pleine échelle)
	LookaheadMs float64 // anticipation en ms, dans [0,1 ; 50] ; <= 0 → 5
	ReleaseMs   float64 // relâchement en ms ; <= 0 → 100
}

func Limiter(src io.Reader, sampleRate, channels int, s LimiterSettings) io.Reader
```

Limiteur de crête **à anticipation** : aucune crête d'échantillon ne dépasse `CeilingDB`. Le gain nécessaire pour chaque trame est calculé `LookaheadMs` plus tôt et la réduction est étalée en rampe sur cette durée, de sorte qu'elle est déjà complète quand le pic arrive. Le compresseur, lui, laisse passer un pic bref pendant son temps d'attaque ; le limiteur ne le laisse jamais passer, et sans la distorsion d'un écrêtage.

- **Aucune latence visible** : le signal est retardé de l'anticipation en interne, mais la sortie est recalée : même longueur et même alignement que l'entrée (le tampon est vidé en fin de flux).
- **Transparent sous le plafond** : tant qu'aucune crête ne dépasse, le signal ressort **bit à bit identique**.
- Détection liée : le même gain pour tous les canaux.
- Le plafond porte sur les valeurs d'échantillons, pas sur les crêtes inter-échantillons (*true peak*) : un convertisseur en aval peut en reconstruire qui dépassent de 1 à 2 dB. Prenez `-1` dBFS pour avoir de la marge.
- Dans une chaîne de profil, le limiteur remplace le plafond doux : il est le dernier étage.

### Compresseur multibande

```go
type MultibandSettings struct {
	Crossovers []float64            // séparations en Hz, strictement croissantes (n séparations = n+1 bandes)
	Bands      []CompressorSettings // un compresseur par bande, du grave à l'aigu : len(Crossovers)+1
}

func MultibandCompressor(src io.Reader, sampleRate, channels int, s MultibandSettings) io.Reader
```

Chaque bande de fréquences est compressée séparément, avec les mêmes paramètres que [`Compressor`](#compresseur) (seuil, rapport, attaque, relâchement, genou, compensation). C'est ce qui permet de serrer les basses sans faire « pomper » les aigus : sur un test avec une basse forte et un aigu faible, l'aigu reste à 0,0 dB avec un compresseur multibande contre −20 dB avec un compresseur large bande aux mêmes réglages.

```go
s := audioprofile.MultibandSettings{
	Crossovers: []float64{120, 1200, 6000}, // sub-basses | médiums | présence | air
	Bands: []audioprofile.CompressorSettings{
		{ThresholdDB: -24, Ratio: 3, AttackMs: 30, ReleaseMs: 200, KneeDB: 6},
		{ThresholdDB: -22, Ratio: 2.5, AttackMs: 15, ReleaseMs: 150, KneeDB: 6},
		{ThresholdDB: -22, Ratio: 2, AttackMs: 8, ReleaseMs: 100, KneeDB: 6},
		{Ratio: 1}, // aigus laissés tels quels
	},
}
out := audioprofile.MultibandCompressor(in, 44100, 2, s)
```

- **Séparation** : filtres de Linkwitz-Riley d'ordre 4 (−6 dB à la fréquence de séparation). À partir de trois séparations, les bandes graves traversent aussi un passe-tout aux séparations plus hautes : sans compression, la réponse en module est plate à ±0,1 dB de 20 Hz à 18 kHz (seule la phase tourne). Les filtres sont à **phase minimale** ; la phase linéaire demanderait un traitement par blocs avec une latence de plusieurs dizaines de millisecondes, hors de portée d'un traitement en flux de ce type.
- **Détection liée** par bande : le même gain est appliqué aux deux canaux, ce qui préserve l'image stéréo.
- Le flux est renvoyé tel quel si les réglages sont invalides (nombre de bandes différent de séparations + 1, séparations non croissantes ou au-delà de Nyquist) ou si aucune bande n'a d'effet.

### Ampli à tubes

```go
func TubeAmp(src io.Reader, sampleRate, channels int, s TubeAmpSettings) io.Reader

type TubeAmpSettings struct {
	DriveDB  float64 // gain total poussé dans les étages, réparti entre eux (0 = pas de saturation)
	Stages   int     // étages à tube en cascade, de 1 à 4 ; <= 0 → 2
	Bias     float64 // asymétrie β des tubes, de 0 à 1 ; ajoute des harmoniques paires

	BassDB   float64 // correcteur de timbre : shelf à 100 Hz
	MidDB    float64 //                       cloche à 650 Hz (Q = 0,8)
	TrebleDB float64 //                       shelf à 4 kHz

	Cabinet  bool    // simulation de baffle et de haut-parleur
	OutputDB float64 // gain de sortie final
}
```

```go
io.Copy(out, audioprofile.TubeAmp(in, 44100, 2, audioprofile.TubeAmpSettings{
	DriveDB: 9, Stages: 2, Bias: 0.15, BassDB: 2, TrebleDB: -1,
}))
```

La chaîne est : étages de saturation → correcteur de timbre → baffle (si demandé) → plafond doux.

- **Étages en cascade** : chaque étage est la [saturation de type tube](#saturation-de-type-tube) suréchantillonnée. Le drive total est réparti à parts égales entre les étages, la phase est inversée d'un étage à l'autre (comme dans un vrai préampli), puis rétablie en sortie : la polarité d'origine est conservée quel que soit le nombre d'étages. Entre deux étages, un filtre de couplage à 30 Hz retire la composante continue que l'asymétrie fait apparaître. Tout est calculé dans le domaine suréchantillonné ×4, avec un seul jeu de filtres.
- **Timbre** : trois bandes appliquées *après* les étages, comme le correcteur d'un ampli.
- **Baffle** : passe-haut à 80 Hz, résonance de +3 dB à 110 Hz, présence de +3 dB à 2,8 kHz, puis passe-bas d'ordre 4 à 5 kHz. **Sur un morceau complet, il donne un son volontairement étouffé, « de radio »** (10 kHz à −28 dB mesuré sur `sample01.mp3`) : à réserver à un usage créatif, ou à une guitare seule. Les filtres du baffle dont la fréquence dépasse Nyquist sont ignorés.
- **Gain automatique** : les filtres posés après les étages (timbre, baffle) sont suivis d'un gain qui ramène leur réponse maximale à 0 dB ; un renfort de timbre baisse donc le reste d'autant. Aucune autre compensation de niveau n'est faite : la saturation relève les signaux faibles (+3 dB de RMS sur `sample01.mp3` avec les réglages ci-dessus), ajustez `OutputDB`.
- **Plafond doux** : un filtre à 0 dB au maximum peut déphaser les harmoniques d'une onde saturée de façon à additionner leurs pics (jusqu'à 1,36 fois la pleine échelle mesuré avec le baffle, +8 dB de basses et 18 dB de drive). Un plafond doux (genou à 0,8) arrondit ces pics au lieu de les écrêter net ; la sortie ne touche jamais ±32767.
- Sans effet (flux renvoyé tel quel) si `sampleRate <= 0`, ou si tous les réglages sont nuls et `Cabinet` est faux. `Bias` seul, sans drive, n'a pas d'effet.
- Mesuré sur `sample01.mp3` (réglages ci-dessus) : composante continue de 0,14 % de l'original réduite à 0,001 %, aucun échantillon à pleine échelle.

#### Préréglages d'ampli

```go
func TubeAmpPresets() []TubeAmpPreset                 // AmpClean, AmpCrunch, AmpLead
func ParseTubeAmpPreset(name string) (TubeAmpPreset, bool)
func (p TubeAmpPreset) Settings() TubeAmpSettings      // une copie, modifiable
func (s TubeAmpSettings) Scaled(intensity float64) TubeAmpSettings
```

```go
s := audioprofile.AmpCrunch.Settings().Scaled(1.5) // 1,5 = plus marqué
s.Cabinet = false                                  // le baffle est désactivé par défaut
io.Copy(out, audioprofile.TubeAmp(in, 44100, 2, s))
```

Réglés pour un **morceau complet** : le baffle est désactivé, la saturation reste plus mesurée que sur une guitare seule, et le gain de sortie est calibré pour que le niveau à −18 dBFS et 1 kHz reste à moins de 0,3 dB de l'entrée.

| Préréglage | Drive | Étages | Asymétrie | Timbre (basses / médiums / aigus) | Sortie | Distorsion à 200 Hz, −12 dBFS |
|------------|-------|--------|-----------|-----------------------------------|--------|-------------------------------|
| `AmpClean`  | 4 dB  | 2 | 0,15 | +1 / 0 / 0     | −3,5 dB | 2,1 % : chaleur discrète |
| `AmpCrunch` | 9 dB  | 2 | 0,25 | +2 / −1 / +1   | −2,5 dB | 3,7 % : saturation franche |
| `AmpLead`   | 14 dB | 3 | 0,35 | +1 / +2 / +1   | −5 dB   | 9,9 % : forte saturation |

**Étages et harmoniques paires** : la phase est inversée d'un étage à l'autre, donc avec un nombre pair d'étages les harmoniques paires s'annulent en partie (à 200 Hz et −12 dBFS, la part des harmoniques paires dans la distorsion est de 0,8 % pour `AmpClean`, 1,7 % pour `AmpCrunch`, et monte à 7,7 % pour `AmpLead` à 3 étages). C'est pourquoi l'asymétrie de `AmpClean` et `AmpCrunch` pèse moins que la colonne ne le laisse penser ; le profil `Tube`, à un seul étage, est plus « pair ».

`Scaled(intensity)` met le drive et le timbre à l'échelle (0 = aucun effet, 1 = inchangé, jusqu'à `MaxIntensity` = 3). Un gain de sortie négatif suit l'intensité. L'asymétrie et un gain de sortie positif ne croissent pas au-delà de 1 : l'asymétrie, à fort drive et sur plusieurs étages, ferait s'effondrer le niveau (`AmpLead` à −29 dB à l'intensité 3), et un gain positif dépasserait la marge. Le nombre d'étages et `Cabinet` ne changent pas.

Mesures sur trois morceaux de `testdata/` (via le CLI, intensité 1, sans baffle) :

- **Aucun échantillon à pleine échelle**, et une composante continue négligeable : au plus 0,08 %, contre 0,24 % pour Rock sur le premier morceau, dont l'original a déjà 0,14 % que son shelf de basses renforce.
- **Sur un morceau calme** (RMS −21 dBFS), le niveau reste à moins de 1,5 dB de l'original. **Sur un master très fort** (RMS −9,7 dBFS, crête 0 dBFS), la saturation compresse les passages forts : le volume baisse de 2 à 6 dB.
- **`AmpClean` et `AmpCrunch` modifient peu un morceau calme** (leur drive y sature à peine : écart avec l'original de −14 dB sur le premier morceau). À l'intensité 2, `AmpCrunch` s'en écarte nettement plus (−7 dB). `AmpLead` est audible dès l'intensité 1 (−8 dB). Sur un master très fort, l'écart est plus faible (la saturation est déjà engagée).

### Élargisseur stéréo

```go
func StereoWidth(src io.Reader, sampleRate, channels int, width float64) io.Reader
```

Traitement mid/side : `width` multiplie le signal « côté » (`(G − D) / 2`) au-dessus de **200 Hz**. `1` = inchangé, `1,5` à `2` = nettement plus large, `0` = mono au-dessus de 200 Hz ; la valeur est ramenée dans `[0, MaxWidth]` (`MaxWidth = 3`).

- Le milieu (`(G + D) / 2` : voix, grosse caisse, basse) n'est jamais modifié, donc **`G + D` est conservé** : le résultat reste compatible mono (à 1 LSB d'arrondi près).
- Sous 200 Hz, le côté garde son niveau (il est seulement déphasé par le filtre de séparation de Linkwitz-Riley d'ordre 4) : les basses restent au centre.
- Seuls les flux stéréo (`channels = 2`) sont traités ; mono, plus de deux canaux, `width = 1` ou `NaN` renvoient le flux tel quel.
- Élargir peut faire dépasser la pleine échelle à un signal déjà fort : les pics sont arrondis par un plafond doux (genou à 0,8), pas écrêtés net. Il n'y a pas de gain de compensation : le niveau du centre reste le même.

C'est un effet de **spatialisation** : il ne touche pas à la dynamique.

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

# Ampli à tubes : préréglage crunch, plus marqué (intensité 2)
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -amp crunch -intensity 2 \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -

# Comparer sans pipe, avec des fichiers bruts
./audioprofile -profile jazz < musique.raw > musique_jazz.raw
```

| Option        | Défaut  | Description                                                       |
|---------------|---------|-------------------------------------------------------------------|
| `-profile`    | —       | Profil d'égaliseur à appliquer (`-profile` ou `-amp` est obligatoire) |
| `-amp`        | —       | Préréglage d'ampli à tubes : `clean`, `crunch` ou `lead` (exclusif avec `-profile`) |
| `-cabinet`    | `false` | Avec `-amp` : ajoute la simulation de baffle (son étouffé sur un morceau complet) |
| `-intensity`  | `1`     | Profil ou ampli : `0` = aucun effet, `1` = réglage complet, jusqu'à `3` pour l'exagérer |
| `-rate`       | `44100` | Fréquence d'échantillonnage en Hz                                 |
| `-channels`   | `2`     | Nombre de canaux                                                  |
| `-list`       | —       | Affiche les profils disponibles et quitte                         |
| `-list-amps`  | —       | Affiche les préréglages d'ampli disponibles et quitte             |

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
| `saturation_test.go` | forme de la saturation (pleine échelle conservée, monotone, impaire, asymétrique avec biais), harmoniques impaires seulement à biais nul et paires avec biais, composante continue retirée, silence exact, réduction du repliement (≥ 20 dB), plafond doux (jamais ±32767 après arrondi), états par canal sans diaphonie, seuls les profils asymétriques (Jazz, Tube) produisent des harmoniques paires, gain des petits signaux qui croît avec le drive malgré le biais (régression), croissance avec l'intensité, niveau de Rock |
| `tubeamp_presets_test.go` | noms et `ParseTubeAmpPreset`, `Settings()` copie, `Scaled` (0, 1, 2, plafonds, gain de sortie positif), intensité nulle, niveau proche de l'entrée, distorsion croissante de Clean à Lead, aucun écrêtage jusqu'à l'intensité 3 avec ou sans baffle, silence et lecture fragmentée |
| `tubeamp_test.go` | identité sans réglage, polarité conservée de 1 à 4 étages, harmoniques paires sans composante continue, correcteur de timbre (±6 dB), baffle (aigus, très graves, présence, résonance), pic des filtres finaux à 0 dB, basse fréquence d'échantillonnage, gain de sortie, silence exact, états par canal et lecture fragmentée, aucun écrêtage même à fond |
| `limiter_test.go` | signal sous le plafond bit à bit identique (mono et stéréo), alignement d'une impulsion et longueur (entrées de 1 à 1000 trames, vide), aucune crête au-dessus du plafond (sinus à pleine échelle dès le début, salve soudaine, bruit blanc, de 0 à −20 dB), anticipation (réduction avant la salve, retour au niveau après relâchement), rampe progressive et non une marche (échelon continu — un test de mutation sans la rampe le fait échouer), détection liée, lecture octet par octet / données + EOF / octets en trop, silence exact, réglages invalides, pas de dérive sur 3 × 65 536 trames |
| `multiband_test.go` | somme des bandes plate à ±0,1 dB (4 bandes, de 20 Hz à 18 kHz, séparations comprises — un test de mutation sans la compensation passe-tout le fait échouer), bandes indépendantes (aigus intacts quand seules les basses sont compressées, contre −20 dB en large bande), compensation par bande, réglages invalides = identité, détection liée et canal silencieux exactement silencieux, longueur conservée, lecture fragmentée identique, mise à l'échelle par l'intensité |
| `stereo_test.go` | côté multiplié par la largeur et milieu intact, basses (60 Hz) laissées au centre, `G + D` conservé (à 1 LSB), largeur 0 = mono au-dessus de 200 Hz, identité (largeur 1, NaN, mono, 3 canaux, fréquence nulle), plafonnement à `MaxWidth`, aucun écrêtage net, lecture fragmentée identique |
| `profiles_test.go` | noms et `ParseProfile`, identité de `Flat` et de l'intensité nulle, pic à 0 dB, forme des courbes, niveau des profils avec effets, aucun écrêtage à −3 dBFS sur tout le spectre, y compris à intensité exagérée (2 et 3), effet croissant et plafonné au-delà de 1 |
| `cmd/audioprofile/main_test.go` | liste des profils et des amplis, traitement, identité de `flat`, `-amp` (trois préréglages, casse, intensité 0 et 2, `-cabinet`), mono et fréquence, erreurs (ampli inconnu, `-profile` + `-amp`, `-cabinet` seul…), entrée vide |

## Limites connues

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- Au-delà de l'intensité 1, le volume baisse et le niveau n'est plus calibré sur l'entrée ; un signal à pleine échelle (0 dBFS) peut écrêter plus largement qu'à l'intensité 1 (Pop, Vocal).
- Les réglages des profils n'ont pas été validés à l'oreille : ils sont vérifiés par mesure (forme des courbes, niveaux) mais restent des points de départ à affiner.
- Le compresseur n'a pas d'anticipation : un pic très bref passe pendant le temps d'attaque. Seul le [`Limiter`](#limiteur) garantit un plafond. Un signal à pleine échelle (0 dBFS) dès son premier échantillon peut donc écrêter brièvement avec un profil sans limiteur (Pop, Vocal) ; à −3 dBFS, aucun profil n'écrête. En dehors des profils, un fort gain d'égalisation sur un signal déjà proche de la saturation est écrêté (distorsion).
- Les profils et préréglages avec saturation sont calibrés à −18 dBFS : sur un master très fort, la saturation compresse les passages forts et le volume baisse (jusqu'à −6 dB mesuré sur un morceau à RMS −9,7 dBFS).
- Le suréchantillonnage de la saturation réduit fortement le repliement de spectre mais ne l'élimine pas (−25 dB mesuré sur un cas poussé), et ses filtres arrondissent un peu les aigus extrêmes des profils saturés.
- Le dosage de la saturation dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
