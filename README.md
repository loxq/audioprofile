# audioprofile

Bibliothèque Go (et petit outil en ligne de commande) pour donner une couleur à un flux audio PCM : égaliseur, profils de genre (Rock, Pop, Jazz…), compresseur, saturation de type tube, traitement de diffusion « radio » (multibande, élargissement stéréo, limiteur). Tout se fait **au fil de l'eau** sur des `io.Reader`, sans charger le fichier en mémoire.

Ce que le paquet `audioprofile` propose :

- `ApplyProfile` : applique un **profil prédéfini** (Rock, Pop, Classical, Jazz, HipHop, Electronic, Acoustic, Vocal, Tube, et les profils **radio** `radio-hits`, `radio-rock`, `radio-urban`, `radio-club`, `radio-talk`, `radio-gold`, `radio-natural`) avec une intensité réglable ;
- `Equalizer` : **égaliseur paramétrique** à bandes libres (type, fréquence, gain en dB, Q), avec des **filtres passe-haut et passe-bas** de Butterworth (ordres 1 à 8, `HighPassFilter` et `LowPassFilter`) ;
- `Compressor` : **compresseur de dynamique** (seuil, ratio, attaque, relâchement, genou, compensation) ;
- `MultibandCompressor` : **compresseur multibande** (séparations de Linkwitz-Riley, un compresseur par bande) ;
- `Limiter` : **limiteur de crête à anticipation** (plafond garanti, sortie recalée sur l'entrée) ;
- `StereoWidth` : **élargisseur stéréo** mid/side qui garde les basses au centre et la compatibilité mono ;
- `TubeAmp` : **ampli à tubes simulé** (étages de saturation asymétriques en cascade, correcteur de timbre, baffle optionnel), avec les préréglages `AmpClean`, `AmpCrunch` et `AmpLead` ;
- `BassBoost`, `MidBoost`, `TrebleBoost` : **préréglages à une bande** (basses, médiums, aigus) réglés par une simple intensité ;
- `MeasureLoudness`, `LoudnessMeter` : **mesure de sonie** EBU R128 (intégrée en LUFS, plage en LU, crête), au fil de l'eau ;
- `NormalizeLoudness` : **normalisation de sonie** (amène un morceau à −14 LUFS, par exemple, avec un limiteur sur les crêtes) ;
- `MatchGains`, `ApplyGain`, `Alternate`, `SegmentFrames` : **comparaison A/B à sonie égale** (alterne l'original et le traité) ;
- `audioprofile` (commande) : applique un profil ou un préréglage d'ampli sur un flux lu sur l'entrée standard, pour se brancher sur un pipe `ffmpeg`, , compare à l'oreille avec `-ab` , mesure la sonie avec `-loudness` et la règle avec `-target-lufs`.

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

# Ou un traitement de diffusion « radio » : plus fort, plus large, crêtes limitées à −1 dBFS
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile radio-hits \
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
| `RadioHits`  | `radio-hits`         | Hits actuels : fort, brillant, large               |
| `RadioRock`  | `radio-rock`         | Médiums agressifs, bas serrés, saturation légère   |
| `RadioUrban` | `radio-urban`        | Hip-hop, R&B : basses profondes, aigus nets        |
| `RadioClub`  | `radio-club`         | Dance : basses puissantes, limitation franche, très large |
| `RadioTalk`  | `radio-talk`         | Voix parlée : intelligibilité, niveau régulier, image centrée |
| `RadioGold`  | `radio-gold`         | Classiques : chaleur et présence, caractère d'époque préservé |
| `RadioNatural` | `radio-natural`    | Diffusion transparente : dynamique naturelle, contrôle léger |

- **Intensité** : `0` = aucun effet, `1` = profil complet. Elle met à l'échelle les gains, le ratio du compresseur et le drive de la saturation.
- **Exagérer un profil** : une intensité **au-delà de 1** (jusqu'à `MaxIntensity` = 3) amplifie l'effet, utile pour juger d'un profil à l'oreille ou pour un rendu plus marqué. Le gain automatique grandit avec l'effet, donc le volume baisse (de 1 à 7 dB à l'intensité 3 selon le profil, mesuré à −18 dBFS et 1 kHz) ; `Tube` fait exception : sa saturation relève les signaux faibles (+8 dB à 3). Les profils `radio-*` n'ont pas de gain automatique : leur niveau dépend de l'AGC et de la multibande, et leurs crêtes restent sous −1 dBFS à toute intensité (voir [Profils radio](#profils-radio)). La compensation du compresseur, le gain de sortie positif et l'asymétrie de la saturation ne croissent pas au-delà de leur valeur d'intensité 1 : les deux premiers pour éviter l'écrêtage, le dernier parce qu'à fort drive l'asymétrie rendrait le niveau quasi nul. Les valeurs hors de [0, `MaxIntensity`] sont ramenées dedans.
- **Gain automatique** : pour éviter l'écrêtage, un gain global négatif est ajouté afin que la fréquence la plus renforcée reste à 0 dB. Le volume perçu baisse donc d'autant, sauf pour les profils avec effets (voir plus bas) et pour les profils `radio-*`, qui n'ont pas de gain automatique : c'est leur limiteur qui borne les crêtes.
- **Chaîne de traitement** : égalisation → compresseur → [multibande] → saturation → gain de sortie → [élargissement stéréo] → [limiteur]. Seuls certains profils ont les étages suivant l'égalisation ; l'élargissement et le limiteur (entre crochets) ne concernent que les profils radio.
- **Valeurs** : les courbes sont des points de départ raisonnables, pas une norme (il n'existe pas de standard officiel). Elles sont définies dans [profiles.go](profiles.go).
- **Utilitaires** : `Profiles()` liste les profils, `ParseProfile("rock")` en retrouve un par son nom (insensible à la casse), `p.String()` donne son nom, et `p.Bands()` renvoie une copie de ses bandes d'égalisation pour partir d'elles avec `Equalizer` (pour un profil `radio-*`, ce n'est que l'égalisation : la multibande, l'élargissement et le limiteur n'y sont pas ; `radio-natural` n'a aucune bande).

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

La compensation du compresseur et la saturation relèvent le niveau pour contrebalancer la baisse du gain automatique : à −18 dBFS et 1 kHz, chaque profil avec effets de ce tableau rend le niveau d'entrée à moins de 0,4 dB près. Les profils `radio-*` ne suivent pas cette règle (voir [Profils radio](#profils-radio)). Les réglages sont dans `profileCompression` et `profileSaturation` ([profiles.go](profiles.go)).

#### Profils radio

Les profils `radio-*` appliquent une chaîne de diffusion complète, dont la structure s'inspire de celle des processeurs de diffusion (nettoyage, niveau, mise en forme par bandes, limitation) :

```
égalisation → AGC → multibande (4 bandes) → saturation (certains) → élargissement stéréo → limiteur −1 dBFS
```

- **AGC** : un compresseur large bande très lent (attaque 100–200 ms, relâchement 0,8–1,5 s) qui nivelle les écarts de volume d'un morceau à l'autre, avec une compensation de 3,5 à 7,5 dB.
- **Multibande** : quatre bandes séparées à 120 Hz, 1,2 kHz et 6 kHz (sub-basses, médiums, présence, air), seuil de −20 à −26 dBFS, ratios de 1,5:1 (`radio-natural`) à 3,5:1 (`radio-club`).
- **Élargissement** : `radio-hits` ×1,25, `radio-rock` ×1,2, `radio-urban` ×1,15, `radio-club` ×1,4, `radio-gold` ×1,15 ; `radio-talk` et `radio-natural` gardent l'image d'origine.
- **Limiteur** : plafond à −1 dBFS (1 dB de marge pour les crêtes inter-échantillons), anticipation 5 ms, relâchement de 50 ms (`radio-club`) à 150 ms (`radio-natural`). C'est le dernier étage et **il remplace le gain automatique** des autres profils : la garantie vient du limiteur, pas d'un gain négatif.
- Saturation : `radio-rock` (+4 dB), `radio-club` (+3 dB) et `radio-gold` (+3 dB, asymétrie 0,3).

Tous les réglages sont dans [profiles.go](profiles.go) et [profiles_radio.go](profiles_radio.go).

**Leur sonie converge vers une valeur commune et leur plage de sonie est plus faible : un morceau calme ou moyen sort plus fort, un master déjà très fort sort plus faible, et les crêtes ne dépassent jamais −1 dBFS.** Sur les trois morceaux de `testdata/` (valeurs mesurées sur le flux de sortie, intensité 1) :

| Profil | Sonie intégrée¹ (calme / moyen / très fort) | Plage de sonie¹ (LRA) | Crête | Côté/milieu² |
|--------|----------------------------------------------|-----------------------|-------|--------------|
| `radio-hits`    | +5,5 / +2,7 / −2,9 LU | −1,7 / −4,8 / −4,2 LU | ≤ −1,0 dBFS | +0,8 / +2,0 / +4,8 dB |
| `radio-rock`    | +7,0 / +4,8 / −0,5 LU | −2,0 / −4,7 / −4,6 LU | ≤ −1,0 dBFS | +0,5 / +1,8 / +4,6 dB |
| `radio-urban`   | +4,1 / +1,8 / −3,1 LU | −1,6 / −3,9 / −4,2 LU | ≤ −1,0 dBFS | +0,4 / +1,0 / +3,7 dB |
| `radio-club`    | +8,3 / +5,3 / −0,3 LU | −2,0 / −5,0 / −4,7 LU | ≤ −1,0 dBFS | +1,1 / +2,8 / +5,6 dB |
| `radio-talk`    | +2,8 / +0,8 / −4,5 LU | −2,1 / −5,4 / −3,9 LU | ≤ −1,0 dBFS | −0,2 / +1,0 / +4,2 dB |
| `radio-gold`    | +4,1 / +2,0 / −2,2 LU | −1,4 / −3,7 / −4,0 LU | ≤ −1,0 dBFS | +0,5 / +0,9 / +2,8 dB |
| `radio-natural` | +2,3 / +0,8 / −3,1 LU | −0,9 / −2,8 / −3,3 LU | ≤ −1,0 dBFS | −0,1 / −0,2 / +1,5 dB |

Pour mémoire, les trois morceaux d'origine mesurent −19,2 / −13,3 / −7,7 LUFS (sonie intégrée) et 2,7 / 8,7 / 6,7 LU (plage de sonie).

¹ Mesurées avec `-loudness` (UIT-R BS.1770 / EBU R128, voir [Mesure de sonie](#mesure-de-sonie)), en différence avec l'original ; les mêmes valeurs, pour l'original et quelques sorties, ont été recoupées avec le filtre `ebur128` de ffmpeg à 0,1 LU près, l'arrondi de l'affichage (par exemple −13,302 LUFS et 8,71 LU pour le morceau moyen, contre −13,3 et 8,7 ici). ² Rapport d'énergie côté/milieu, en dB. L'élargisseur seul apporte `20·log10(largeur)` (+1,9 dB pour ×1,25) ; le reste vient de la compression des basses, très centrées, qui augmente la part relative du côté.

À lire dans ce tableau :

- **La sonie converge** vers une valeur commune : un morceau calme est relevé de 2 à 8 LU, un master très fort est **abaissé** de 0,3 à 4,5 LU (l'AGC et la multibande le compressent avant le limiteur). Ce n'est pas un volume maximal garanti.
- **La plage de sonie est réduite de 0,9 à 5,4 LU.** Si ces profils donnent l'impression d'un son plus « vivant », ce serait plutôt par le relâchement lent des bandes graves (le punch reste), la présence relevée des médiums et des aigus et l'élargissement stéréo : c'est une hypothèse, pas une mesure. Un renforçateur de transitoires ou une expansion, qui augmenteraient réellement la dynamique, ne sont pas implémentés.
- **Intensité** : `0` laisse le signal intact (octet pour octet) ; en dessous de 1, ratios, élargissement et égalisation sont réduits proportionnellement ; au-delà de 1, ratios et élargissement croissent (largeur ×1,75 pour `radio-hits` à l'intensité 3) tandis que les compensations de niveau restent à leur valeur d'intensité 1. Le plafond de −1 dBFS tient à toute intensité.
- Sur un sinus à −18 dBFS, ces profils relèvent le niveau de +3,6 dB (`radio-natural`) à +10,4 dB (`radio-club`) : un signal faible est remonté par la compensation sans que les bandes ne le compressent. C'est ce que fait un AGC avec des passages calmes ; `radio-natural` et `radio-gold` sont les moins marqués.
- Avec `sampleRate` bas (séparations au-delà de Nyquist, par exemple 8 kHz), la multibande est ignorée ; le reste de la chaîne et le limiteur continuent d'agir.

#### Saturation de type tube

La saturation ([saturation.go](saturation.go)) applique, sur le signal normalisé, `f(x) = tanh(g·x + β) − tanh(β)` puis normalise pour que la pleine échelle reste à la pleine échelle (`g` = drive, `β` = asymétrie). Elle arrondit les pics au lieu de les écrêter.

- **Asymétrie** : à `β = 0` (Rock, HipHop, Electronic), la courbe est symétrique et ne produit que des **harmoniques impaires** (3e, 5e…), plutôt dures. Avec `β > 0` (Jazz, Tube, RadioGold), elle est asymétrique comme celle d'un tube et ajoute des **harmoniques paires** (2e, 4e…), perçues comme plus chaudes. À 200 Hz, 2e harmonique de 1,9 % (Jazz) et 3,7 % (Tube) à −18 dBFS, 8,8 % et 9,6 % à −3 dBFS, contre 0 pour les profils sans asymétrie. Un filtre passe-haut (7 Hz) retire la composante continue qu'introduit l'asymétrie, et le silence reste un silence exact.
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
	Type   BandType // LowShelf, Peaking, HighShelf, Gain, HighPass ou LowPass
	FreqHz float64  // fréquence de coupure (shelf, passe-haut, passe-bas) ou centrale (peaking)
	GainDB float64  // gain en dB, ramené dans [-24, +24] (ignoré par HighPass et LowPass)
	Q      float64  // facteur de qualité ; <= 0 → 1/√2
	Order  int      // HighPass et LowPass : ordre du filtre, de 1 à 8 ; <= 0 → 2
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

- **Types** : `LowShelf` et `HighShelf` agissent sous / au-dessus de `FreqHz` ; `Peaking` est une cloche centrée sur `FreqHz` ; `Gain` est un gain global sur tout le spectre (`FreqHz` et `Q` sont ignorés) ; `HighPass` et `LowPass` sont des filtres de coupure (voir ci-dessous).
- **Q** : plus il est grand, plus la bande est étroite. Pour une cloche, le gain demandé est atteint exactement à la fréquence centrale quel que soit Q.
- Les bandes sont appliquées dans l'ordre, en série, en une seule passe sur les échantillons.
- Sont ignorées les bandes sans effet : gain nul ou NaN (sauf pour `HighPass` et `LowPass`, qui n'ont pas de gain), ou (hors `Gain`) fréquence <= 0 ou >= Nyquist. S'il n'en reste aucune, le flux est renvoyé tel quel.
- Filtres biquad en forme directe II transposée (formules du *RBJ Audio EQ Cookbook*).

#### Filtres passe-haut et passe-bas

```go
func HighPassFilter(src io.Reader, sampleRate, channels int, cutoffHz float64, order int) io.Reader
func LowPassFilter(src io.Reader, sampleRate, channels int, cutoffHz float64, order int) io.Reader
```

Filtres de **Butterworth** (réponse la plus plate possible en bande passante) : **−3 dB à la fréquence de coupure**, puis **6 dB par octave et par ordre**, de l'ordre 1 (6 dB/octave) à l'ordre 8 (48 dB/octave) ; `order <= 0` donne l'ordre 2 (12 dB/octave), et un ordre hors de [1, 8] est ramené dedans. Le passe-haut retire les grondements et le bruit de manipulation (par exemple 40–80 Hz), le passe-bas adoucit les aigus ou retire un sifflement.

- Ils existent aussi comme types de bande (`HighPass`, `LowPass`) dans [`Equalizer`](#égaliseur-paramétrique) : `Band{Type: audioprofile.HighPass, FreqHz: 80, Order: 4}`. Le gain d'une telle bande est ignoré (y compris à 0, qui désactive les autres types).
- À l'ordre 2, `Q` règle le filtre comme pour les autres bandes (`<= 0` → 1/√2, Butterworth) ; aux autres ordres, chaque section a le Q de Butterworth qui lui correspond (`1 / (2·sin((2j−1)π / 2n))`), et un ordre impair ajoute une section du premier ordre.
- Une fréquence <= 0, NaN ou >= Nyquist laisse le flux tel quel.
- À partir de l'ordre 8, l'atténuation à deux octaves dépasse 90 dB : elle tombe sous le plancher de quantification d'un flux 16 bits.

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

### Mesure de sonie

```go
type Loudness struct {
	IntegratedLUFS float64 // sonie intégrée (valable si Valid)
	RangeLU        float64 // plage de sonie, LRA (valable si HasRange)
	PeakDBFS       float64 // crête d'échantillon
	Valid, HasRange bool
}

func MeasureLoudness(src io.Reader, sampleRate, channels int) (Loudness, error)
func NewLoudnessMeter(sampleRate, channels int) *LoudnessMeter // io.Writer : Write, puis Loudness()
```

Mesure selon l'**UIT-R BS.1770-4** et l'**EBU R128**, la référence de la diffusion et du streaming :

- **Sonie intégrée (LUFS)** : le signal passe par la pondération K (un shelf aigu de +4 dB vers 1,7 kHz qui modélise la tête, puis un passe-haut à 38 Hz), puis l'énergie est moyennée par blocs de 400 ms (recouvrement de 75 %) avec une **double porte** : −70 LUFS absolu, puis −10 LU sous la moyenne des blocs restants. Les passages calmes et le silence ne tirent donc pas la mesure vers le bas. Les portes se calculent sur la moyenne des énergies, jamais sur une moyenne de valeurs en LUFS.
- **Plage de sonie (LRA, EBU Tech 3342)** : écart entre les centiles 10 % et 95 % des niveaux à court terme (3 s), après les portes −70 LUFS et −20 LU. Un signal stable donne 0 ; deux niveaux séparés de 10 dB donnent environ 10 LU. C'est la mesure de la **dynamique** à l'échelle d'un morceau.
- **Crête d'échantillon (dBFS)** : pas la crête inter-échantillons (*true peak*).
- Étalonnage : un sinus de 1 kHz à X dBFS sur les deux canaux donne X LUFS (−23 dBFS → −23,0 LUFS, cas d'essai de l'EBU Tech 3341) ; sur un seul canal, 3,01 dB de moins.
- **Au fil de l'eau** : `LoudnessMeter` reçoit les octets par `Write`, par morceaux de n'importe quelle taille (même octet par octet : le résultat est identique), et ne garde que l'énergie de chaque pas de 100 ms (environ 10 valeurs par seconde).
- **Résultat « non valable »** (`Valid` ou `HasRange` faux, jamais `-Inf` ni `NaN`) : silence, flux vide, moins de 400 ms de signal audible, et pour la plage, moins de 3 s.
- Tous les canaux ont le même poids : exact en mono et en stéréo, approché au-delà (la recommandation pondère les canaux d'ambiance de 5.1 et ignore le canal des basses).
- **Recoupé avec ffmpeg** : sur deux signaux synthétiques à 48 kHz, l'écart avec le filtre `ebur128` de ffmpeg est de 0,007 LU sur la sonie intégrée et de 0,001 LU sur la plage ; sur les trois morceaux de `testdata/` et une sortie traitée, il est d'au plus 0,1 LU (sonie intégrée et plage), à l'arrondi de l'affichage près. Les coefficients du filtre à 48 kHz sont ceux de la table de la recommandation, à 10⁻⁹ près.

### Normalisation de sonie

```go
type NormalizeOptions struct {
	TargetLUFS float64 // sonie intégrée visée, dans [-70, 0]
	CeilingDB  float64 // plafond des crêtes d'échantillon, dans [-40, 0] (0 = pleine échelle)
	MaxBoostDB float64 // gain maximal accordé ; <= 0 → 24
}

func NormalizeLoudness(pcm []byte, sampleRate, channels int, o NormalizeOptions) ([]byte, NormalizeResult, error)
```

Amène la **sonie intégrée** d'un morceau à une cible, par exemple **−14 LUFS** (niveau de référence de nombreux services de streaming) ou **−23 LUFS** (diffusion EBU R128). Le signal reçoit un gain, puis le [limiteur](#limiteur) à `CeilingDB` (−1 dBFS en général) : les crêtes ne dépassent jamais le plafond. Le gain et le limiteur sont dans la même chaîne, en nombres flottants : un gain qui dépasse la pleine échelle n'est pas écrêté avant que le limiteur ne le voie.

- **Itération** : le limiteur baisse un peu la sonie obtenue, donc le gain est corrigé et la sonie mesurée de nouveau, jusqu'à tomber à 0,1 LU de la cible (au plus 8 passes). Chaque passe repart du signal d'origine. Mesuré avec ffmpeg sur les trois morceaux de `testdata/`, à −14 et −23 LUFS : de −14,039 à −14,005 et de −23,007 à −23,005 LUFS.
- **Baisser** n'a pas besoin du limiteur (si les crêtes restent sous le plafond) : le résultat est alors exactement le signal multiplié par un gain.
- **Cible inatteignable** : le résultat le dit (`Reached`, `Reason`) et rend ce qui a été obtenu de plus proche, sans dépasser le plafond. Deux cas : un morceau très faible qui demanderait plus de `MaxBoostDB` de gain, et un plafond (ou des crêtes) qui empêche d'aller plus fort (le limiteur écraserait le morceau : on s'arrête quand le gain n'apporte plus de sonie).
- `NormalizeResult` donne aussi la sonie `Before` et `After`, le gain appliqué, et si le limiteur est intervenu (`Limited`).
- Silence, flux vide ou moins de 400 ms de signal : renvoyé tel quel, sans erreur (`Reason` explique). Une cible ou un plafond invalide (NaN, hors bornes) donne une erreur et le signal tel quel. L'entrée n'est jamais modifiée.
- Le plafond porte sur les crêtes d'échantillon, **pas sur les crêtes inter-échantillons** (*true peak*) : visez −1 dBFS ou moins pour de la marge.
- Travaille sur un tampon en mémoire.

### Comparaison A/B à sonie égale

```go
func MatchGains(original, processed []byte, sampleRate, channels int) (originalDB, processedDB float64)
func ApplyGain(pcm []byte, gainDB float64) []byte
func Alternate(a, b []byte, sampleRate, channels int, segmentSeconds float64) []byte
func SegmentFrames(sampleRate int, segmentSeconds float64) int
```

Comparer un son traité à l'original quand il est plus fort est trompeur : le plus fort paraît presque toujours « meilleur ». Ces trois fonctions travaillent sur des tampons en mémoire (pas sur des flux) :

- **`MatchGains`** mesure la **sonie** des deux signaux ([LUFS intégrés](#mesure-de-sonie), qui suivent mieux ce qu'on entend que le niveau efficace : à niveau efficace égal, un signal grave paraît moins fort qu'un signal à 3 kHz) et renvoie les gains à appliquer pour les égaliser. Sur un extrait de moins de 400 ms, qui n'a pas de sonie intégrée, il retombe sur le niveau efficace. **Le plus fort est atténué, l'autre n'est jamais relevé** : aucun risque d'écrêtage. Un signal silencieux ne reçoit aucun gain.
- **`ApplyGain`** renvoie une copie multipliée par un gain en dB, arrondie et écrêtée à 16 bits (0 dB = copie identique).
- **`SegmentFrames`** donne la longueur d'un segment en trames : c'est ce qui permet à la commande d'annoncer les changements de source.
- **`Alternate`** alterne `a` et `b` toutes les `segmentSeconds` secondes, en commençant par `a`. C'est un vrai A/B : les deux flux sont lus à la même position, la musique ne s'interrompt pas, seule la source change. Chaque changement est un **fondu enchaîné linéaire de 15 ms** (à gain constant : `a` et `b` étant la même musique, un fondu à puissance constante ajouterait 3 dB). Deux flux identiques ressortent identiques, bit à bit. La sortie a la longueur du plus court des deux.

La commande les assemble avec l'option `-ab` ([voir plus bas](#comparer-à-loreille--ab)).

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

# Traitement de diffusion : profil radio, à moitié dosé
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile radio-hits -intensity 0.5 \
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
| `-profile`    | —       | Profil à appliquer, y compris `radio-*` (`-profile` ou `-amp` est obligatoire, sauf avec `-fx`) |
| `-amp`        | —       | Préréglage d'ampli à tubes : `clean`, `crunch` ou `lead` (exclusif avec `-profile`) |
| `-cabinet`    | `false` | Avec `-amp` : ajoute la simulation de baffle (son étouffé sur un morceau complet) |
| `-intensity`  | `1`     | Profil ou ampli : `0` = aucun effet, `1` = réglage complet, jusqu'à `3` pour l'exagérer |
| `-target-lufs` | —      | Amène la sonie intégrée de la sortie à cette valeur (de −70 à 0 LUFS) ; lit tout le flux |
| `-ceiling`    | `-1`    | Avec `-target-lufs` : plafond des crêtes d'échantillon, en dBFS (de −40 à 0) |
| `-fx`         | —       | Ajoute un effet après le profil ou l'ampli (répétable ; voir [`-fx`](#ajouter-des-effets--fx)) |
| `-list-fx`    | —       | Affiche les effets de `-fx` et leurs paramètres et quitte |
| `-loudness`   | `false` | Affiche sur la sortie d'erreur la sonie (intégrée, plage, crête) de l'original et du traité |
| `-ab`         | `false` | Alterne l'original (A) et le traité (B), à sonie égale |
| `-ab-seconds` | `5`     | Avec `-ab` : durée de chaque segment, en secondes |
| `-ab-delay`   | `0`     | Avec `-ab` : retarde l'affichage de A / B de ce nombre de secondes (tampon du lecteur) |
| `-rate`       | `44100` | Fréquence d'échantillonnage en Hz                                 |
| `-channels`   | `2`     | Nombre de canaux                                                  |
| `-list`       | —       | Affiche les profils disponibles et quitte                         |
| `-list-amps`  | —       | Affiche les préréglages d'ampli disponibles et quitte             |

### Ajouter des effets (`-fx`)

```sh
# Un passe-haut à 80 Hz d'ordre 4 (24 dB/octave) seul, sans profil
./audioprofile -fx hpf:freq=80,order=4 < musique.raw > sortie.raw

# Après un profil, plusieurs effets, dans l'ordre : passe-haut puis passe-bas
./audioprofile -profile rock -fx hpf:freq=60 -fx lpf:freq=14000,order=4 < musique.raw > sortie.raw
```

`-fx nom` ou `-fx nom:cle=valeur,cle=valeur` ajoute un effet **après le profil ou l'ampli**, dans l'ordre où les `-fx` apparaissent. L'option est répétable, et **`-profile` devient facultatif dès qu'un `-fx` est donné**. `./audioprofile -list-fx` affiche les effets, leurs paramètres et leurs valeurs par défaut.

| Effet | Paramètres | Rôle |
|-------|------------|------|
| `hpf` | `freq` (Hz, obligatoire), `order` (1 à 8, défaut 2) | filtre passe-haut de Butterworth |
| `lpf` | `freq` (Hz, obligatoire), `order` (1 à 8, défaut 2) | filtre passe-bas de Butterworth |

L'analyse est stricte : un effet ou un paramètre inconnu (le message liste les valides), un paramètre obligatoire absent ou donné deux fois, une valeur qui n'est pas un nombre fini, ou une fréquence hors de `]0, rate/2[` sont des erreurs. Les effets sont dans la chaîne de traitement : `-ab`, `-loudness` et `-target-lufs` les entendent (avec `-ab`, le côté B est nommé d'après la chaîne : `traité (flat + lpf:freq=3000)`).

### Comparer à l'oreille (`-ab`)

```sh
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile radio-hits -ab \
  | ffplay -nostats -loglevel error -f s16le -ar 44100 -ac 2 -nodisp -
```

(`-nostats` évite que la ligne d'état de ffplay ne se mélange aux annonces ci-dessous.)

La sortie alterne l'**original (A)** pendant 5 s, puis le **traité (B)** pendant 5 s, et ainsi de suite, sur la même musique (on change de source, pas de position). `-ab-seconds` règle la durée des segments (`-ab-seconds 2` pour comparer plus vite). Avant de jouer, la commande affiche sur la sortie d'erreur la convention (A = original, B = traité), la durée des segments et **le gain appliqué** : le côté le plus fort est atténué pour que les deux aient la même sonie (LUFS, voir [`MatchGains`](#comparaison-ab-à-sonie-égale)).

- **La source en cours est annoncée sur la console**, avec l'instant, à chaque changement (en couleur si la sortie d'erreur est un terminal, sauf si `NO_COLOR` est défini) :

  ```
  A/B : traitement du morceau en cours, la lecture commencera dans quelques secondes…
  A/B : A = original, B = traité (radio-hits) ; segments de 5 s : A de 0 à 5 s, B de 5 à 10 s, etc.
  A/B : le traité est plus fort que l'original : gain de -2.7 dB appliqué à B pour égaliser la sonie (l'original n'est pas modifié).
  [0:00.0] A  original
  [0:05.0] B  traité (radio-hits)
  [0:10.0] A  original
  ```

  Chaque annonce part juste avant l'écriture de son segment, et les segments sont écrits un par un : quand la sortie est un pipe, l'écriture se bloque dès que le pipe est plein, ce qui cale l'annonce sur la lecture. **Elle reste en avance d'environ la mémoire tampon du lecteur** : avec un lecteur parfaitement cadencé et un pipe de 64 Ko, j'ai mesuré 0,3 s d'avance (le pipe seul) ; ffplay, qui lit environ 1 s d'avance, devrait en ajouter autant (non mesuré avec un vrai lecteur audio). `-ab-delay 1` retarde l'affichage de 1 s pour compenser ; ajustez-le à l'oreille. Redirigez la sortie d'erreur (`2> /dev/null`) pour ne pas les afficher.
- **Le flux entier est lu et traité avant la première note** : il y a quelques secondes d'attente, et la mémoire utilisée est proportionnelle à la durée du morceau (mesuré : 232 Mo pour un morceau de 3 min 51 s, soit environ 60 Mo par minute de stéréo à 44,1 kHz).
- Fonctionne avec `-profile` comme avec `-amp`. Avec un profil sans effet (`flat`, ou intensité 0), la sortie est identique à l'entrée, bit à bit.
- `-ab-seconds` et `-ab-delay` sans `-ab` sont une erreur.

### Mesurer la sonie (`-loudness`)

```sh
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile radio-hits -loudness \
  | ffplay -f s16le -ar 44100 -ac 2 -nodisp -
```

```
Sonie (EBU R128)               original     traité
  intégrée (LUFS)                 -13.3      -10.6
  plage (LU)                        8.7        3.9
  crête d'échantillon (dBFS)       -0.2       -1.0
```

Le tableau s'affiche sur la sortie d'erreur à la fin du flux ; la sortie audio n'est pas modifiée (octet pour octet). La mesure se fait au fil de l'eau, sans garder le morceau en mémoire. Pour ne garder que la mesure, envoyez la sortie dans `/dev/null` : `… | ./audioprofile -profile radio-hits -loudness > /dev/null`. Avec `-ab`, les deux flux entiers sont mesurés (pas l'alternance). Un « — » signale une mesure impossible (silence, moins de 400 ms de signal, moins de 3 s pour la plage).

### Régler la sonie (`-target-lufs`)

```sh
# Amener un morceau à −14 LUFS (streaming), crêtes limitées à −1 dBFS
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile flat -target-lufs -14 \
  | ffmpeg -f s16le -ar 44100 -ac 2 -i - musique_14lufs.wav

# Un profil radio, puis la sonie réglée et affichée avant / après
./audioprofile -profile radio-hits -target-lufs -16 -loudness < musique.raw > sortie.raw
```

Sortie d'erreur sur le morceau moyen de `testdata/` (`sample02`), pour la première commande puis pour la seconde (`radio-hits`, `-target-lufs -16 -loudness`) :

```
Normalisation : sonie intégrée -13.3 → -14.0 LUFS (cible -14.0), gain de -0.7 dB avant le limiteur à -1.0 dBFS, limiteur intervenu.
```

```
Normalisation : sonie intégrée -10.6 → -16.0 LUFS (cible -16.0), gain de -5.4 dB avant le limiteur à -1.0 dBFS.
Sonie (EBU R128)               original     traité
  intégrée (LUFS)                 -13.3      -16.0
  plage (LU)                        8.7        3.9
  crête d'échantillon (dBFS)       -0.2       -6.4
```

Le « limiteur intervenu » de la première sortie vient des crêtes de ce morceau (−0,2 dBFS), plus hautes que le plafond de −1 dBFS : même en baissant la sonie de 0,7 dB, le limiteur retouche les pointes.

La normalisation vient **après** le profil ou l'ampli : la sonie « avant » du résumé est celle du signal déjà traité. Avec `-profile flat`, c'est une simple normalisation. `-ceiling` règle le plafond des crêtes (−1 dBFS par défaut). Si la cible n'est pas atteignable, la sortie est la meilleure approche possible et le résumé l'explique : `Normalisation : cible de -3.0 LUFS non atteinte : -7.9 LUFS obtenus, à partir de -19.2 (le limiteur à -1 dBFS empêche d'aller plus fort).` (mesuré sur le morceau calme de `testdata/`).

- **Le flux entier est lu avant d'écrire**, comme avec `-ab` : mémoire proportionnelle à la durée (mesuré : 247 Mo pour un morceau de 3 min 51 s), et seule la partie gain + limiteur est refaite à chaque passe de normalisation (le profil ou l'ampli ne tourne qu'une fois). `-loudness` seul, lui, reste au fil de l'eau (11 Mo).
- `-target-lufs` et `-ab` sont exclusifs (`-ab` égalise déjà les sonies) ; `-ceiling` sans `-target-lufs` est une erreur.
- Le plafond porte sur les crêtes d'échantillon, pas sur les crêtes inter-échantillons (*true peak*).

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
| `saturation_test.go` | forme de la saturation (pleine échelle conservée, monotone, impaire, asymétrique avec biais), harmoniques impaires seulement à biais nul et paires avec biais, composante continue retirée, silence exact, réduction du repliement (≥ 20 dB), plafond doux (jamais ±32767 après arrondi), états par canal sans diaphonie, seuls les profils asymétriques (Jazz, Tube, RadioGold) produisent des harmoniques paires, gain des petits signaux qui croît avec le drive malgré le biais (régression), croissance avec l'intensité, niveau de Rock |
| `tubeamp_presets_test.go` | noms et `ParseTubeAmpPreset`, `Settings()` copie, `Scaled` (0, 1, 2, plafonds, gain de sortie positif), intensité nulle, niveau proche de l'entrée, distorsion croissante de Clean à Lead, aucun écrêtage jusqu'à l'intensité 3 avec ou sans baffle, silence et lecture fragmentée |
| `tubeamp_test.go` | identité sans réglage, polarité conservée de 1 à 4 étages, harmoniques paires sans composante continue, correcteur de timbre (±6 dB), baffle (aigus, très graves, présence, résonance), pic des filtres finaux à 0 dB, basse fréquence d'échantillonnage, gain de sortie, silence exact, états par canal et lecture fragmentée, aucun écrêtage même à fond |
| `limiter_test.go` | signal sous le plafond bit à bit identique (mono et stéréo), alignement d'une impulsion et longueur (entrées de 1 à 1000 trames, vide), aucune crête au-dessus du plafond (sinus à pleine échelle dès le début, salve soudaine, bruit blanc, de 0 à −20 dB), anticipation (réduction avant la salve, retour au niveau après relâchement), rampe progressive et non une marche (échelon continu — un test de mutation sans la rampe le fait échouer), détection liée, lecture octet par octet / données + EOF / octets en trop, silence exact, réglages invalides, pas de dérive sur 3 × 65 536 trames |
| `multiband_test.go` | somme des bandes plate à ±0,1 dB (4 bandes, de 20 Hz à 18 kHz, séparations comprises — un test de mutation sans la compensation passe-tout le fait échouer), bandes indépendantes (aigus intacts quand seules les basses sont compressées, contre −20 dB en large bande), compensation par bande, réglages invalides = identité, détection liée et canal silencieux exactement silencieux, longueur conservée, lecture fragmentée identique, mise à l'échelle par l'intensité |
| `profiles_radio_test.go` | les 7 profils radio existent, noms et `ParseProfile`, constantes existantes inchangées, niveau à −18 dBFS dans la fourchette mesurée de chacun, aucune crête au-dessus du plafond de −1 dBFS (7 profils × 4 intensités × 7 fréquences, mono et stéréo, signal à −0,1 dBFS), identité à intensité 0 / négative / NaN, longueur conservée, profil sans égalisation (`radio-natural`) qui agit quand même, élargissement du côté/milieu à la largeur du profil à 3 kHz et pas à 60 Hz, image de `radio-talk` inchangée, dynamique non écrasée (écart faible/fort conservé), basse fréquence d'échantillonnage |
| `loudness_test.go` | coefficients de la pondération K à 48 kHz égaux à la table de l'UIT-R BS.1770-4 (10⁻⁹), gain de +0,69 dB à 1 kHz (44,1 / 48 / 96 kHz), étalonnage (X dBFS stéréo → X LUFS, mono −3,01 dB, à 44,1 / 48 / 96 kHz), accord avec ffmpeg `ebur128` sur deux signaux (±0,1 LU et ±1 LU), doubles portes (calme, silence numérique, niveau à −8 LU retenu), plage de sonie (stable ≈ 0, deux niveaux ≈ 10 LU), cas non valables (silence, vide, 200 ms, paramètres) sans `-Inf` ni `NaN`, morceaux de toute taille (octet par octet, 3 octets, 4097 octets, octet isolé) identiques, plusieurs canaux |
| `filters_test.go` | −3 dB à la coupure à tous les ordres (1 à 8, trois fréquences d'échantillonnage), pente de 6·n dB par octave contre la courbe de Butterworth, bande passante plate, Q respecté à l'ordre 2 et ordres hors bornes ramenés, gain ignoré (même à 0 ou NaN), fréquences invalides ignorées, mesure de bout en bout sur un flux, combinaison avec d'autres bandes et lecture fragmentée |
| `normalize_test.go` | cible atteinte (monter, descendre, cas streaming ; ± 0,15 LU) avec plafond respecté et longueur conservée, baisse = simple gain (bit à bit), montée avec limiteur actif, cibles inatteignables (plafond trop bas, gain maximal, `MaxBoostDB`) avec raison et meilleur résultat sous le plafond, gains de plus de 24 dB répartis sur plusieurs bandes, idempotence, équilibre gauche/droite, silence / vide / 100 ms intacts, cibles et plafonds invalides (NaN, hors bornes) en erreur, entrée non modifiée |
| `ab_test.go` | niveau efficace (sinus, silence numérique ignoré, paramètres invalides), gains qui égalisent les sonies sans jamais relever (traité plus fort, original plus fort, égaux, silence), sonie et non niveau efficace (basse contre 3 kHz), repli sur le niveau efficace sous 400 ms, `ApplyGain` (identité à 0 dB, −6 dB, écrêtage, copie, octet isolé), segments purs hors fondu, fondu linéaire monotone dans les deux sens, deux flux identiques bit à bit, entrelacement stéréo, longueur et paramètres invalides |
| `stereo_test.go` | côté multiplié par la largeur et milieu intact, basses (60 Hz) laissées au centre, `G + D` conservé (à 1 LSB), largeur 0 = mono au-dessus de 200 Hz, identité (largeur 1, NaN, mono, 3 canaux, fréquence nulle), plafonnement à `MaxWidth`, aucun écrêtage net, lecture fragmentée identique |
| `profiles_test.go` | noms et `ParseProfile`, identité de `Flat` et de l'intensité nulle, pic à 0 dB, forme des courbes, niveau des profils avec effets, aucun écrêtage à −3 dBFS sur tout le spectre, y compris à intensité exagérée (2 et 3), effet croissant et plafonné au-delà de 1 |
| `cmd/audioprofile/fx_test.go` | `-list-fx`, `-fx` seul (sans profil) et combiné à un profil, filtres passe-haut / passe-bas / passe-bande mesurés, casse et espaces, mono, ordre des effets, toutes les erreurs d'analyse (effet ou paramètre inconnu, doublon, NaN, hors bornes, Nyquist), noms en `-ab`, avec `-loudness` et `-target-lufs` |
| `cmd/audioprofile/main_test.go` | liste des profils (dont `radio-*`) et des amplis, traitement, `-ab` (profil sans effet bit à bit, alternance et convention annoncée, annonce de la source en cours à l'instant et juste avant son segment, annonces retardées dans l'ordre, pas de couleur hors terminal, erreurs d'options, ampli et mono), `-loudness` (sortie inchangée, tableau, mesure impossible sans `NaN`, avec `-ab`), sortie vers `/dev/null`, `-target-lufs` (−14 / −23 / −30 mesurés sur la sortie, après un profil, avec `-loudness`, cible inatteignable, silence, toutes les erreurs d'options), profil radio (longueur, plafond à −1 dBFS à l'intensité 3, intensité 0), identité de `flat`, `-amp` (trois préréglages, casse, intensité 0 et 2, `-cabinet`), mono et fréquence, erreurs (ampli inconnu, `-profile` + `-amp`, `-cabinet` seul…), entrée vide |

## Limites connues

- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- `-ab`, `-target-lufs` et `NormalizeLoudness` travaillent en mémoire : environ 60 Mo par minute de stéréo à 44,1 kHz (mesuré : 232 à 247 Mo pour un morceau de près de 4 minutes). Seuls les profils, `-loudness` et `MeasureLoudness` restent au fil de l'eau.
- La mesure de sonie pèse tous les canaux de la même façon (exact en mono et en stéréo, approché pour du 5.1), et le plafond des crêtes porte sur les valeurs d'échantillons, pas sur les crêtes inter-échantillons (*true peak*).
- Les paramètres des profils (dont `radio-*`) n'ont pas été recalibrés sur la sonie : leurs réglages restent ceux d'une calibration sur le niveau d'un sinus et sur le niveau efficace. La sonie « avant / après » se mesure avec `-loudness`, et `-target-lufs` ou `-ab` permettent de comparer à sonie égale.
- Les profils `radio-*` n'ont pas été validés à l'oreille non plus. Leurs réglages sont des points de départ, calés par mesure sur trois morceaux : niveau, crête, plage dynamique et côté/milieu.
- Les filtres de séparation du compresseur multibande sont à phase minimale, pas à phase linéaire. Le limiteur borne les crêtes d'échantillons (pas les crêtes inter-échantillons) et n'apporte pas de saturation ou d'écrêtage doux en plus.
- Au-delà de l'intensité 1, le volume baisse et le niveau n'est plus calibré sur l'entrée ; un signal à pleine échelle (0 dBFS) peut écrêter plus largement qu'à l'intensité 1 (Pop, Vocal).
- Les réglages des profils n'ont pas été validés à l'oreille : ils sont vérifiés par mesure (forme des courbes, niveaux) mais restent des points de départ à affiner.
- Le compresseur n'a pas d'anticipation : un pic très bref passe pendant le temps d'attaque. Seul le [`Limiter`](#limiteur) garantit un plafond. Un signal à pleine échelle (0 dBFS) dès son premier échantillon peut donc écrêter brièvement avec un profil sans limiteur (Pop, Vocal) ; à −3 dBFS, aucun profil n'écrête. En dehors des profils, un fort gain d'égalisation sur un signal déjà proche de la saturation est écrêté (distorsion).
- Les profils et préréglages avec saturation sont calibrés à −18 dBFS : sur un master très fort, la saturation compresse les passages forts et le volume baisse (jusqu'à −6 dB mesuré sur un morceau à RMS −9,7 dBFS).
- Le suréchantillonnage de la saturation réduit fortement le repliement de spectre mais ne l'élimine pas (−25 dB mesuré sur un cas poussé), et ses filtres arrondissent un peu les aigus extrêmes des profils saturés.
- Le dosage de la saturation dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
