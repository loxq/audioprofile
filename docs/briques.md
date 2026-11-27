# Les briques de traitement

[← Retour au README](../README.md)

Chaque brique prend un `io.Reader` PCM s16le et en renvoie un autre : elles s'enchaînent. Voir [le format d'entrée et de sortie](../README.md#format-dentrée--sortie).

## Égaliseur paramétrique

```go
func Equalizer(src io.Reader, sampleRate, channels int, bands []Band) io.Reader

type Band struct {
	Type   BandType // LowShelf, Peaking, HighShelf, Gain, HighPass, LowPass ou Notch
	FreqHz float64  // fréquence de coupure (shelf, passe-haut, passe-bas) ou centrale (peaking)
	GainDB float64  // gain en dB, ramené dans [-24, +24] (ignoré par HighPass, LowPass et Notch)
	Q      float64  // facteur de qualité ; <= 0 → 1/√2 (10 pour Notch)
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

- **Types** : `LowShelf` et `HighShelf` agissent sous / au-dessus de `FreqHz` ; `Peaking` est une cloche centrée sur `FreqHz` ; `Gain` est un gain global sur tout le spectre (`FreqHz` et `Q` sont ignorés) ; `HighPass` et `LowPass` sont des filtres de coupure et `Notch` un coupe-bande étroit (voir ci-dessous).
- **Q** : plus il est grand, plus la bande est étroite. Pour une cloche, le gain demandé est atteint exactement à la fréquence centrale quel que soit Q.
- Les bandes sont appliquées dans l'ordre, en série, en une seule passe sur les échantillons.
- Sont ignorées les bandes sans effet : gain nul ou NaN (sauf pour `HighPass`, `LowPass` et `Notch`, qui n'ont pas de gain), ou (hors `Gain`) fréquence <= 0 ou >= Nyquist. S'il n'en reste aucune, le flux est renvoyé tel quel.
- Filtres biquad en forme directe II transposée (formules du *RBJ Audio EQ Cookbook*).

### Filtres passe-haut et passe-bas

```go
func HighPassFilter(src io.Reader, sampleRate, channels int, cutoffHz float64, order int) io.Reader
func LowPassFilter(src io.Reader, sampleRate, channels int, cutoffHz float64, order int) io.Reader
```

Filtres de **Butterworth** (réponse la plus plate possible en bande passante) : **−3 dB à la fréquence de coupure**, puis **6 dB par octave et par ordre**, de l'ordre 1 (6 dB/octave) à l'ordre 8 (48 dB/octave) ; `order <= 0` donne l'ordre 2 (12 dB/octave), et un ordre hors de [1, 8] est ramené dedans. Le passe-haut retire les grondements et le bruit de manipulation (par exemple 40–80 Hz), le passe-bas adoucit les aigus ou retire un sifflement.

- Ils existent aussi comme types de bande (`HighPass`, `LowPass`) dans [`Equalizer`](#égaliseur-paramétrique) : `Band{Type: audioprofile.HighPass, FreqHz: 80, Order: 4}`. Le gain d'une telle bande est ignoré (y compris à 0, qui désactive les autres types).
- À l'ordre 2, `Q` règle le filtre comme pour les autres bandes (`<= 0` → 1/√2, Butterworth) ; aux autres ordres, chaque section a le Q de Butterworth qui lui correspond (`1 / (2·sin((2j−1)π / 2n))`), et un ordre impair ajoute une section du premier ordre.
- Une fréquence <= 0, NaN ou >= Nyquist laisse le flux tel quel.
- À partir de l'ordre 8, l'atténuation à deux octaves dépasse 90 dB : elle tombe sous le plancher de quantification d'un flux 16 bits.

### Coupe-bande (notch)

```go
func NotchFilter(src io.Reader, sampleRate, channels int, freqHz, q float64) io.Reader
```

Filtre coupe-bande du second ordre : le gain est **nul à `freqHz`** (un vrai zéro : −100 dB et moins) et la bande supprimée mesure `freqHz / q` de large à −3 dB. `q <= 0` donne 10 (à 50 Hz, environ 5 Hz de large), bien plus étroit que le 1/√2 des autres bandes. Plus `q` est grand, plus la bande est étroite. L'usage typique est de retirer un **ronflement du secteur** (50 ou 60 Hz), puis ses harmoniques (100, 150 Hz…) avec d'autres coupe-bandes : sur un test avec un ronflement à −30 dBFS, il tombe de plus de 40 dB alors que la musique à 330 Hz et 1 kHz reste à ±0,1 dB.

- Existe aussi comme type de bande (`Band{Type: audioprofile.Notch, FreqHz: 50, Q: 12}`) ; le gain d'une telle bande est ignoré.
- La largeur à −3 dB suit `freqHz / q` à 0,4 dB près jusqu'à 1 kHz (mesuré pour trois `q` et trois fréquences d'échantillonnage) ; vers 8 kHz, la déformation de la transformation bilinéaire élargit un peu la bande (−2,1 dB aux bords calculés). Une octave de part et d'autre, le signal passe à ±0,1 dB (pour `q >= 10`).
- Une fréquence <= 0, NaN ou >= Nyquist laisse le flux tel quel. Un `q` très grand donne un filtre très sélectif mais à la réponse transitoire longue (de l'ordre de `q / (π × freqHz)` secondes).


## Compresseur

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


## Limiteur

```go
type LimiterSettings struct {
	CeilingDB   float64 // plafond des crêtes en dBFS, dans [-40, 0] (0 = pleine échelle)
	LookaheadMs float64 // anticipation en ms, dans [0,1 ; 50] ; <= 0 → 5
	ReleaseMs   float64 // relâchement en ms ; <= 0 → 100
}

func Limiter(src io.Reader, sampleRate, channels int, s LimiterSettings) io.Reader
```

Limiteur de crête **à anticipation** : aucune crête d'échantillon ne dépasse `CeilingDB` (à une demi-valeur d'échantillon près : le signal est arrondi à l'entier). Le gain nécessaire pour chaque trame est calculé `LookaheadMs` plus tôt et la réduction est étalée en rampe sur cette durée, de sorte qu'elle est déjà complète quand le pic arrive. Le compresseur, lui, laisse passer un pic bref pendant son temps d'attaque ; le limiteur ne le laisse jamais passer, et sans la distorsion d'un écrêtage.

- **Aucune latence visible** : le signal est retardé de l'anticipation en interne, mais la sortie est recalée : même longueur et même alignement que l'entrée (le tampon est vidé en fin de flux).
- **Transparent sous le plafond** : tant qu'aucune crête ne dépasse, le signal ressort **bit à bit identique**.
- Détection liée : le même gain pour tous les canaux.
- Le plafond porte sur les valeurs d'échantillons, pas sur les crêtes inter-échantillons (*true peak*) : un convertisseur en aval peut en reconstruire qui dépassent de 1 à 2 dB. Prenez `-1` dBFS pour avoir de la marge.
- Dans une chaîne de profil, le limiteur remplace le plafond doux : il est le dernier étage.


## Gate / expandeur

```go
type GateSettings struct {
	ThresholdDB float64 // seuil en dBFS : en dessous, le signal est réduit
	Ratio       float64 // 2 = expandeur doux, 100 = gate franc ; <= 1 : sans effet
	RangeDB     float64 // réduction maximale en dB (positive) ; <= 0 → 40
	AttackMs    float64 // ouverture, en ms ; <= 0 → 2
	ReleaseMs   float64 // fermeture, en ms ; <= 0 → 100
}

func Gate(src io.Reader, sampleRate, channels int, s GateSettings) io.Reader
```

Réduit les **passages faibles** : le bruit de fond entre les phrases, le souffle entre les notes. C'est l'inverse d'un compresseur : au-dessus du seuil rien ne change, en dessous le niveau chute `Ratio` fois plus vite qu'il ne baisse à l'entrée. Avec `Ratio = 2`, un passage 10 dB sous le seuil sort 10 dB plus bas (expandeur doux) ; avec un rapport élevé, il est réduit de `RangeDB` (gate).

- **Courbe** : droite de pente `Ratio − 1` sous le seuil, raccordée sans saut par un genou quadratique de 6 dB, et réduction plafonnée à `RangeDB` (testée point par point, avec continuité sur tout l'axe).
- **Détection** : crête du canal le plus fort, avec un détecteur qui monte en 0,2 ms et retombe en 30 ms (pour ne pas hacher les basses fréquences) ; le même gain s'applique à tous les canaux. La réduction est lissée : l'ouverture suit `AttackMs`, la fermeture `ReleaseMs` (la réduction atteint 86 % de la plage après deux temps de relâchement ; mesuré avec 80 ms de relâchement et une plage de 40 dB : −34 dB 0,25 s après la fin d'une salve).
- **Mesuré** (sur des signaux sous le plafond doux commun à tous les `-fx`, qui arrondit les pics au-dessus d'environ −1,9 dBFS) : un sinus de 40 Hz à 8 kHz au-dessus du seuil sort à ±0,1 dB (y compris les basses fréquences, où l'enveloppe ondule) ; 15 dB sous le seuil avec `Ratio = 2`, −15 dB ±2 ; un gate franc réduit de `RangeDB` ±3. Sur des salves à −20 dBFS séparées par un bruit de fond à −60 dBFS, le bruit est réduit de plus de 25 dB entre les salves et les salves passent à ±0,5 dB ; une ouverture de 0,5 ms laisse passer 36 dB de plus des 4 premières millisecondes d'une salve qu'une ouverture de 40 ms.
- **Pas d'anticipation** : le tout début d'un son qui sort du silence peut être atténué pendant `AttackMs`. Le silence reste exactement du silence.
- **Sur les morceaux de `testdata/`, il ne fait presque rien** (`-fx gate` aux défauts, −50 dBFS : sonie et plage de sonie inchangées, crête de 0,0 à 0,4 dB plus bas à cause du plafond doux des `-fx`) : ce sont des musiques continues, sans rien sous −40 dBFS. Il est pensé pour de la voix et des prises avec du bruit de fond, que je n'ai pas essayées.
- **Coût** : 231 s de stéréo traitées en 0,7 s.
- Sans effet (flux identique) si `Ratio <= 1` ou NaN, si le seuil est NaN, ou si `sampleRate <= 0`. Rapport et plage hors bornes sont ramenés dedans.


## Compression parallèle

```go
type ParallelCompressionSettings struct {
	Compressor CompressorSettings // en général très marqué : seuil bas, rapport élevé, compensation forte
	Mix        float64            // 0 (sec, sans effet) à 1 (entièrement comprimé)
}

func ParallelCompressor(src io.Reader, sampleRate, channels int, s ParallelCompressionSettings) io.Reader
```

Compression « parallèle » ou « New York » : le signal passe aussi par un compresseur très marqué, dont la compensation relève fortement les passages faibles, puis les deux sont mélangés : `sortie = (1 − Mix) × sec + Mix × comprimé`. Les passages faibles, remontés par le comprimé, gagnent en **densité et en détail** ; les passages forts, aplatis dans le comprimé, restent dominés par le sec et gardent leurs transitoires. Le sec et le comprimé étant la même musique à un gain près, le mélange est **linéaire**.

- C'est le compresseur de [`Compressor`](#compresseur) (détection liée sur le canal le plus fort, même gain pour tous les canaux).
- **Mesuré** avec un compresseur à −40 dBFS, 10:1, compensation de 12 dB et `Mix = 0,5` : un sinus à −55 dBFS est relevé de +7,9 dB, un sinus à −10 dBFS est abaissé de 4,5 dB ; sur un son qui s'éteint, la queue gagne +3,5 dB quand l'attaque perd 1,3 dB.
- `Mix = 1` donne exactement le compresseur seul ; `Mix = 0,5` la moyenne du sec et du comprimé, à 1 LSB près (testé sur un signal assez faible pour que le plafond doux commun à tous les `-fx` n'intervienne pas : au-dessus d'environ −1,9 dBFS, les pics sont arrondis).
- Sans effet (flux identique) si `Mix` est nul ou NaN, si le compresseur est sans effet (rapport <= 1 et compensation nulle) ou si `sampleRate <= 0`. Un plafond doux (genou à 0,8) arrondit les pics, car la compensation relève le niveau.
- **Mesuré avec `-fx parcomp`** (défauts : −30 dBFS, 8:1, attaque 10 ms, relâchement 120 ms, compensation 16 dB, `Mix = 0,3`) sur les trois morceaux de `testdata/` : sonie intégrée +1,5 / +0,2 / −1,2 LU (calme / moyen / très fort), plage de sonie −1,1 / −3,0 / −1,8 LU, crête de −2,8 à −3,3, de −0,2 à −0,9 et de 0,0 à 0,0 dBFS. **L'effet dépend du niveau** : sur le morceau calme, il y a beaucoup à remonter ; sur le master très fort, dont presque tout dépasse le seuil, le comprimé est lui-même très réduit et le mélange fait baisser la sonie.


## Compresseur multibande

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


## Ampli à tubes

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

- **Étages en cascade** : chaque étage est la [saturation de type tube](profils.md#saturation-de-type-tube) suréchantillonnée. Le drive total est réparti à parts égales entre les étages, la phase est inversée d'un étage à l'autre (comme dans un vrai préampli), puis rétablie en sortie : la polarité d'origine est conservée quel que soit le nombre d'étages. Entre deux étages, un filtre de couplage à 30 Hz retire la composante continue que l'asymétrie fait apparaître. Tout est calculé dans le domaine suréchantillonné ×4, avec un seul jeu de filtres.
- **Timbre** : trois bandes appliquées *après* les étages, comme le correcteur d'un ampli.
- **Baffle** : passe-haut à 80 Hz, résonance de +3 dB à 110 Hz, présence de +3 dB à 2,8 kHz, puis passe-bas d'ordre 4 à 5 kHz. **Sur un morceau complet, il donne un son volontairement étouffé, « de radio »** (10 kHz à −28 dB mesuré sur `sample01.mp3`) : à réserver à un usage créatif, ou à une guitare seule. Les filtres du baffle dont la fréquence dépasse Nyquist sont ignorés.
- **Gain automatique** : les filtres posés après les étages (timbre, baffle) sont suivis d'un gain qui ramène leur réponse maximale à 0 dB ; un renfort de timbre baisse donc le reste d'autant. Aucune autre compensation de niveau n'est faite : la saturation relève les signaux faibles (+3 dB de RMS sur `sample01.mp3` avec les réglages ci-dessus), ajustez `OutputDB`.
- **Plafond doux** : un filtre à 0 dB au maximum peut déphaser les harmoniques d'une onde saturée de façon à additionner leurs pics (jusqu'à 1,36 fois la pleine échelle mesuré avec le baffle, +8 dB de basses et 18 dB de drive). Un plafond doux (genou à 0,8) arrondit ces pics au lieu de les écrêter net ; la sortie ne touche jamais ±32767.
- Sans effet (flux renvoyé tel quel) si `sampleRate <= 0`, ou si tous les réglages sont nuls et `Cabinet` est faux. `Bias` seul, sans drive, n'a pas d'effet.
- Mesuré sur `sample01.mp3` (réglages ci-dessus) : composante continue de 0,14 % de l'original réduite à 0,001 %, aucun échantillon à pleine échelle.

### Préréglages d'ampli

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


## De-esser

```go
type DeEsserSettings struct {
	FreqHz      float64 // séparation ; ce qui est au-dessus est surveillé ; <= 0 → 5000
	ThresholdDB float64 // seuil, en dBFS, mesuré sur la bande haute
	Ratio       float64 // rapport de compression de la bande haute ; <= 1 : sans effet
}

func DeEsser(src io.Reader, sampleRate, channels int, s DeEsserSettings) io.Reader
```

Atténue les **sifflantes** (les « s » et les « ch » d'une voix, vers 5 à 8 kHz) quand elles ressortent trop, sans toucher au reste du son. C'est un [compresseur multibande](#compresseur-multibande) à deux bandes : le spectre est coupé à `FreqHz` par un filtre de Linkwitz-Riley d'ordre 4, la bande basse passe intacte et la bande haute traverse un compresseur à attaque rapide (0,5 ms) et relâchement de 40 ms (genou de 3 dB). Tant que la bande haute reste sous le seuil, elle n'est pas touchée.

- **Mesuré** : un sinus de 7 kHz à −12 dBFS (une « sifflante ») est réduit de 9,2 dB avec `Ratio = 8` et `ThresholdDB = −30`, alors qu'une note de 500 Hz jouée en même temps reste à ±0,3 dB (avant, pendant et après la sifflante) ; un 7 kHz à −45 dBFS, sous le seuil, passe à ±0,3 dB. Un rapport plus fort ou un seuil plus bas réduit davantage.
- Détection liée : le même gain pour tous les canaux.
- **Le seuil est absolu** (en dBFS) : il dépend du niveau de l'enregistrement, et il faut le régler pour chaque source. **Il n'a pas été essayé sur de la voix** : les morceaux de `testdata/` sont de la musique.
- **La bande haute contient tout ce qui dépasse `FreqHz`**, cymbales et charleston compris : sur de la musique, un de-esser réglé trop sensible les atténue aussi. Mesuré avec `-fx deess` (seuil −30 dBFS, rapport 4) sur les trois morceaux de `testdata/`, la sonie au-dessus de 5 kHz baisse de 0,0 / 3,0 / 8,7 dB (calme / moyen / très fort) et la sonie totale de 0,0 / 0,2 / 0,7 LU ; avec un seuil de −40 dBFS et un rapport de 6, de 0,1 / 6,0 / 11,5 dB. Le morceau très fort, aux aigus soutenus, est le plus touché.
- Même sous le seuil, le flux n'est pas rendu à l'identique : la somme des deux bandes a un module plat à ±0,1 dB, mais sa phase tourne. **Et le plafond doux commun à tous les effets** (genou à 0,8, c'est-à-dire autour de −1,9 dBFS) arrondit les pics même quand l'effet ne fait rien : avec `deess:threshold=0`, qui ne se déclenche jamais, la crête du morceau moyen passe de −0,2 à −0,6 dBFS (sonie inchangée, −13,3 LUFS). C'est un petit biais à garder en tête quand on compare des masters très forts avec `-ab`.
- Une sifflante à 7 kHz avec une séparation à 5 kHz est presque entièrement dans la bande haute (94 %) ; plus la séparation est proche de la sifflante, plus la réduction est partielle (une séparation à 6 kHz laisse passer une part notable d'un 7 kHz dans la bande basse). Le flux est renvoyé tel quel si `Ratio <= 1`, `sampleRate <= 0` ou si `FreqHz` dépasse Nyquist.
- **Coût** : 231 s de stéréo traitées en 1,6 s.


## Exciter

```go
type ExciterSettings struct {
	FreqHz  float64 // séparation : seul ce qui est au-dessus est enrichi ; <= 0 → 3000
	Amount  float64 // part d'harmoniques ajoutée, de 0 (sans effet) à 1
	DriveDB float64 // gain poussé dans la saturation, de 3 à 24 dB ; <= 0 → 20
	Warmth  float64 // 0 : harmoniques impaires ; 1 : harmoniques paires en plus
}

func Exciter(src io.Reader, sampleRate, channels int, s ExciterSettings) io.Reader
```

Ajoute des **harmoniques** aux aigus : de la brillance et de la présence qu'une égalisation, qui ne fait que relever ce qui existe, ne donne pas.

```
sortie = entrée + Amount × passe-haut( saturation(passe-haut(entrée)) − même chaîne sans saturation )
```

Le signal passe par un passe-haut de Linkwitz-Riley d'ordre 4 à `FreqHz`, puis par la [saturation de type tube](profils.md#saturation-de-type-tube) de la bibliothèque (suréchantillonnage ×4). On en retire la **même chaîne de suréchantillonnage sans saturation**, qui a exactement le même retard : il ne reste que ce que la non-linéarité a ajouté (harmoniques et petite compression de la fondamentale). Un second passe-haut retire ce qui tombe sous `FreqHz`, et le résultat, multiplié par `Amount`, est ajouté au signal d'origine, qui n'est jamais retardé ni modifié.

- **Pourquoi l'alignement des retards** : ajouter directement la voie saturée, retardée de quelques échantillons par les filtres de suréchantillonnage, au signal sec fait un filtre en peigne. La première version de cet effet creusait de 13 dB à 10 kHz ; un test balaye maintenant la réponse (5 réglages, 15 fréquences, trois niveaux) et garantit l'absence de creux.
- **Transparent à petit signal** : à −55 dBFS, le gain est de 0,00 dB à toutes les fréquences. À −20 dBFS, la fondamentale bouge de ±1,6 dB au plus (la saturation la comprime un peu) ; sur un aigu très fort (−14 dBFS, saturation profonde), de −4,6 à +3,2 dB, sans annulation.
- **Les harmoniques n'apparaissent que si les aigus sont assez forts** pour faire travailler la saturation (le drive est absolu, en dBFS). Avec `DriveDB = 20` et `Amount = 1`, un 5 kHz produit une 3ᵉ harmonique à −45 dBc à −30 dBFS, −28 dBc à −20 dBFS et −22 dBc à −14 dBFS. Augmentez `DriveDB` pour en obtenir sur des aigus plus discrets.
- **`Warmth`** rend la saturation asymétrique : la 2ᵉ harmonique (l'octave, la chaleur) passe de −107 dBc (saturation symétrique : elle n'a que des harmoniques impaires) à −24 dBc avec `Warmth = 0,6` (5 kHz à −14 dBFS).
- **Repliement de spectre** : −38,7 dBc à 17,1 kHz pour un sinus de 9 kHz à −10 dBFS poussé à 24 dB (sa 3ᵉ harmonique, à 27 kHz, se replierait là sans le suréchantillonnage).
- Canaux traités séparément (un canal silencieux reste silencieux), plafond doux (genou à 0,8) pour les pics, valeurs ramenées dans leurs bornes. Sans effet si `Amount` est nul, `sampleRate <= 0` ou `FreqHz` >= Nyquist.
- **Mesuré avec `-fx exciter`** (défauts : `Amount = 0,3`, `DriveDB = 20`, séparation à 3 kHz) sur les trois morceaux de `testdata/` : la sonie au-dessus de 9 kHz bouge de 0,0 / +0,2 / +0,6 dB (calme / moyen / très fort) et la sonie totale d'au plus 0,1 LU ; avec `Amount = 0,6` et `Warmth = 0,4`, de 0,0 / +0,5 / +1,3 dB. **L'effet est discret par construction** : il n'est franc que sur les aigus forts, et nul sur le morceau calme.
- **Coût** : 231 s de stéréo traitées en 3,7 s.


## Lo-fi

```go
type LoFiSettings struct {
	LowCutHz     float64 // passe-haut du 2ᵉ ordre ; 0 : aucun
	HighCutHz    float64 // passe-bas du 4ᵉ ordre ; 0 : aucun
	Bits         int     // quantification, de 1 à 15 bits ; 0 ou >= 16 : aucune
	SampleRateHz float64 // fréquence d'échantillonnage réduite, sans anti-repliement ; 0 : aucune
	Wobble       float64 // wow et flutter, de 0 à 1
	DriveDB      float64 // saturation de bande, de 0 à 24 dB
	Crackle      float64 // craquements de vinyle, de 0 à 1
	Hiss         float64 // souffle de bande, de 0 à 1
}

func LoFi(src io.Reader, sampleRate, channels int, s LoFiSettings) io.Reader
```

Dégrade le son comme un vieux support. **Un champ nul désactive sa partie** : une structure vide n'a aucun effet (flux identique), et `-fx lofi` sans paramètre applique un réglage « lo-fi » prêt à l'emploi. Les étages, dans l'ordre :

1. **`DriveDB`** : saturation douce de bande magnétique (la [saturation de type tube](profils.md#saturation-de-type-tube), suréchantillonnée), calibrée pour ne rien changer aux signaux faibles (±0,1 dB à −50 dBFS).
2. **`Wobble`** : instabilité de vitesse, la **hauteur oscille**. Un *wow* lent (0,6 Hz, ±0,4 % à `Wobble = 1`) et un *flutter* rapide (7,3 Hz, ±0,12 %) passent par la ligne à retard à interpolation de Hermite du [chorus](#chorus-et-flanger) ; les deux canaux dérivent ensemble. Mesuré sur un 1 kHz par les passages à zéro : écart de **4,01 Hz** pour le wow (4,00 attendu, vitesse 0,60 Hz), **1,20 Hz** pour le flutter (1,20 attendu, 7,30 Hz) et **5,20 Hz** à `Wobble = 1` (2,62 Hz à 0,5), avec une fréquence moyenne inchangée (1 000,008 Hz).
3. **`SampleRateHz`** : fréquence d'échantillonnage réduite par échantillonneur-bloqueur, **sans filtre anti-repliement** : les aigus se replient dans le spectre audible, c'est le son des vieux échantillonneurs. Un rapport non entier (44 100 → 8 000, soit 5,51) passe par un accumulateur de phase : 8 000 valeurs nouvelles par seconde à 2 % près ; un 9 kHz à 11 025 Hz se replie à 2 025 Hz, un 5 kHz à 8 000 Hz à 3 000 Hz.
4. **`Bits`** : quantification de chaque échantillon sur ce nombre de bits, sans lissage (*dither*) : une distorsion de quantification de plus en plus audible aux faibles niveaux. Mesuré sur un sinus à −6 dBFS : tous les échantillons sont des multiples du pas, et le rapport signal / bruit vaut 20,4 / 44,1 / 68,0 dB à 4 / 8 / 12 bits (théorie : 19,8 / 43,9 / 68,0 dB).
5. **`LowCutHz` et `HighCutHz`** : bande passante réduite (passe-haut d'ordre 2 : −24 dB à deux octaves sous la coupure ; passe-bas d'ordre 4 : −24 dB à une octave au-dessus), comme un petit haut-parleur ou un téléphone.
6. **`Crackle` et `Hiss`** : craquements de vinyle (des impulsions de signe et d'amplitude aléatoires qui s'éteignent en 0,5 ms, **partagées par les canaux** comme sur un sillon, environ `2 + 18 × Crackle` par seconde, jusqu'à −18 dBFS) et souffle de bande (un bruit blanc filtré à 6 kHz, **indépendant sur chaque canal**, de −60 dBFS efficaces pour presque rien à **−40 dBFS** à `Hiss = 1` ; testé : −40 et −50 dBFS à `Hiss = 1` et `0,5`, à 1,5 dB près, avec une corrélation entre canaux inférieure à 0,05).

- **Avec du bruit, le silence n'est plus du silence** : l'effet en ajoute. Sans `Crackle` ni `Hiss`, un silence reste exactement du silence.
- **Reproductible** : le bruit vient d'un générateur à graine fixe dont l'état est dans l'étage. Deux passages sur le même flux, ou une lecture octet par octet, donnent exactement le même résultat. Densité des craquements : la moyenne sur 30 graines est à 5 % de `2 + 18 × Crackle` (testé) ; avec la graine fixe, 22,9 et 8,5 craquements par seconde mesurés pour 20 et 7,4 attendus (le hasard d'un processus de Poisson sur 20 s).
- **Alignement conservé** : la ligne à retard du wow et du flutter a un retard moyen de 4 ms, **rattrapé** par le lecteur : le flux garde sa longueur et son alignement sur l'entrée, ce qui compte pour la comparaison A/B (`-ab`). Testé : une impulsion ressort à la trame d'entrée à ±55 trames près (la dérive de hauteur elle-même, au plus 1,2 ms), et à ±15 trames en moyenne. Ce mécanisme est général : tout effet qui annonce une latence (`latency()`) est rattrapé comme le limiteur, y compris quand plusieurs s'additionnent ou qu'un limiteur suit (testé avec un effet de retard factice, à la trame près, y compris pour une entrée plus courte que la latence).
- Rien n'est compensé en niveau : réduire la bande passante baisse la sonie. Un plafond doux (genou à 0,8) arrondit les pics. Valeurs ramenées dans leurs bornes (NaN compte pour 0).
- **Mesuré sur les trois morceaux de `testdata/`**, aux défauts de `-fx lofi` (100 Hz–5 kHz, 12 bits, wobble 0,4, saturation 4 dB, craquements 0,3, souffle 0,2) : sonie intégrée −0,9 / −1,4 / −5,4 LU (calme / moyen / très fort), plage de sonie −0,4 / −1,3 / −2,7 LU, sonie au-dessus de 6 kHz **+0,6 / −9,1 / −15,6 dB**. Le morceau calme n'a presque rien au-dessus de 6 kHz : le souffle et la quantification en ajoutent. Avec un réglage plus dur (`bits=8,samplerate=12000,wobble=0.8,crackle=0.6,hiss=0.4,highcut=4000,lowcut=200`) : −2,9 / −2,8 / −7,3 LU, et au-dessus de 6 kHz +4,0 / −15,4 / −21,4 dB.
- **Coût** : 231 s de stéréo traitées en 2,8 s.


## Chorus et flanger

```go
type ChorusSettings struct {
	DelayMs float64 // retard moyen de chaque voix, de 1 à 50 ms ; <= 0 → 18
	DepthMs float64 // amplitude de la modulation du retard ; <= 0 → 3 (au plus DelayMs − 0,5)
	RateHz  float64 // vitesse, de 0,05 à 10 Hz ; <= 0 → 0,8
	Voices  int     // de 1 à 6 ; <= 0 → 3
	Mix     float64 // niveau des voix, de 0 (sans effet) à 1
}

type FlangerSettings struct {
	DelayMs  float64 // retard moyen, de 0,3 à 10 ms ; <= 0 → 2
	DepthMs  float64 // amplitude de la modulation ; <= 0 → 1,5 (au plus DelayMs − 0,2)
	RateHz   float64 // vitesse, de 0,05 à 10 Hz ; <= 0 → 0,25
	Feedback float64 // rétroaction, de −0,95 à 0,95
	Mix      float64 // niveau du signal retardé, de 0 (sans effet) à 1
}

func Chorus(src io.Reader, sampleRate, channels int, s ChorusSettings) io.Reader
func Flanger(src io.Reader, sampleRate, channels int, s FlangerSettings) io.Reader
```

Deux effets de **modulation** : le signal est mélangé à des copies retardées dont le retard oscille lentement, `sortie = sec + Mix × copies`. Le sec n'est ni retardé ni modifié.

- **Chorus** : plusieurs voix (de 1 à 6), chacune à `DelayMs ± DepthMs` (sinusoïde de vitesse `RateHz`, décalée d'un tour divisé par le nombre de voix). L'effet d'un ensemble d'instruments jamais tout à fait accordés. **Les voix sont moyennées** : en ajouter ne rend pas l'effet plus fort. En stéréo, la modulation du canal droit est décalée d'un quart de tour : l'image s'élargit (deux canaux identiques en entrée ne le sont plus en sortie).
- **Flanger** : une seule copie, très courte (0,3 à 10 ms), réinjectée avec `Feedback`. C'est un filtre en peigne qui balaie le spectre. Un retard de τ crée des creux à `(k + ½)/τ` : à `Mix = 1`, sans modulation ni rétroaction, un son à `1/(2τ)` est supprimé de plus de 30 dB et un son à `1/τ` est doublé (+6,02 dB, mesuré). La rétroaction approfondit le peigne et ajoute de la résonance ; son signe décale les creux et les pics. La réponse à une impulsion décroît jusqu'à zéro exact pour `Feedback = ±0,95`.
- **Interpolation de Hermite à quatre points** (Catmull-Rom). Un retard qui varie de moins d'un échantillon fait changer la fraction de lecture à chaque trame ; une interpolation linéaire ferait osciller le niveau des aigus (jusqu'à 1,5 dB à 8 kHz, un « gargouillis »). Mesuré sur toutes les fractions : écart de niveau de 0,02 dB à 4 kHz (0,36 dB en linéaire) et de 0,31 dB à 8 kHz (1,49 dB en linéaire).
- **La hauteur oscille** : le retard qui varie fait varier la fréquence de `2π × RateHz × DepthMs` en valeur relative (**±1,5 % pour les défauts du chorus, 0,8 Hz et 3 ms**, soit environ 26 centièmes de ton : réduisez `depth` si le résultat donne le mal de mer). Mesuré sur un 1 kHz (par les passages à zéro) avec 1 ms de profondeur à 2 Hz : écart de 12,59 Hz pour 12,57 Hz attendus, fréquence moyenne 1 000,08 Hz (la hauteur moyenne ne bouge pas), et écart nul à profondeur nulle.
- Un plafond doux (genou à 0,8) arrondit les pics. Les valeurs sont ramenées dans leurs bornes (NaN compte pour la valeur par défaut). Le retard minimal est de 2 échantillons même à basse fréquence d'échantillonnage. Sans effet si `Mix` est nul ou `sampleRate <= 0`.
- **Mesuré avec `-fx chorus`** (défauts : 18 ms, ±3 ms, 0,8 Hz, 3 voix, mélange 0,5) sur les trois morceaux de `testdata/` : sonie intégrée +0,6 / +0,4 / +0,1 LU (calme / moyen / très fort), plage de sonie −0,1 / −0,4 / −0,2 LU ; coût : 231 s de stéréo en 1,7 s. **Avec `-fx flanger`** (défauts : 2 ms, ±1,5 ms, 0,25 Hz, rétroaction 0,5, mélange 0,5) : sonie +0,6 / +1,0 / +1,4 LU, plage de sonie −1,1 / +0,6 / +0,3 LU ; coût : 0,95 s. Le peigne du flanger et sa rétroaction ajoutent de l'énergie.


## Delay (écho)

```go
type DelaySettings struct {
	TimeMs    float64 // entre deux échos, de 1 à 2000 ms
	Feedback  float64 // part réinjectée à chaque écho, de 0 (un seul écho) à 0,95
	Mix       float64 // niveau des échos par rapport au sec, de 0 (sans effet) à 1
	DampingHz float64 // passe-bas dans la boucle : chaque écho est plus sombre ; <= 0 : aucun
	PingPong  bool    // stéréo : les échos rebondissent entre gauche et droite
}

func Delay(src io.Reader, sampleRate, channels int, s DelaySettings) io.Reader
```

Ajoute des **échos** au signal : `sortie = sec + Mix × échos`. Le sec n'est ni retardé ni atténué.

- **Échos exacts** : l'écho k arrive à `k × TimeMs` (à la trame près) avec l'amplitude `Mix × Feedback^(k−1)` (testé impulsion par impulsion jusqu'au 6ᵉ écho, et rien entre deux échos). `Feedback = 0` donne un seul écho. La queue atteint −60 dB après environ `TimeMs × 6,9 / −ln(Feedback)`.
- **Chaque canal a sa ligne à retard** : la voix de gauche garde ses échos à gauche, celle de droite à droite ; un canal silencieux reste exactement silencieux.
- **`DampingHz`** place un passe-bas du premier ordre dans la boucle : à chaque tour, les échos perdent le gain du passe-bas en plus de `Feedback`, comme dans un écho à bande (mesuré sur un aigu de 6 kHz avec 2 kHz : l'écho 2 perd le gain du filtre une fois, l'écho 3 deux fois, à 1 dB près).
- **`PingPong`** (stéréo seulement) : l'entrée est la moyenne des deux canaux ; le premier écho sort à gauche, le deuxième à droite, le troisième à gauche, etc. (testé aux trames exactes, avec `Feedback^(k−1)`). L'image stéréo d'origine n'est conservée que par le sec. Ignoré en mono et à plus de deux canaux.
- **La queue est coupée à la fin du flux** : la sortie a la longueur de l'entrée, les derniers échos s'arrêtent net. Pour les laisser mourir, ajoutez du silence à la fin (`ffmpeg … -af apad=pad_dur=4`).
- Les états minuscules sont ramenés à zéro : après un long silence, l'écho résiduel est exactement nul (pas de nombres dénormalisés).
- Un plafond doux (genou à 0,8) arrondit les pics qui dépasseraient la pleine échelle. Valeurs ramenées dans leurs bornes (NaN compte pour 0). Sans effet si `Mix` est nul ou `sampleRate <= 0`.
- **Mesuré avec `-fx delay`** (défauts : 350 ms, rétroaction 0,4, mélange 0,3) sur les trois morceaux de `testdata/` : sonie intégrée +0,1 / +0,4 / +0,4 LU (calme / moyen / très fort), plage de sonie +0,1 / +0,1 / −0,1 LU ; avec `time=300,feedback=0.5,mix=0.4,damping=3000,pingpong=1` : +0,5 / +0,4 / +0,3 LU. Les échos s'ajoutent au sec : la sonie monte un peu (contrairement à la réverbération, qui mélange à puissance constante).
- **Coût** : 231 s de stéréo traitées en 0,37 s.


## Réverbération

```go
type ReverbSettings struct {
	RoomSize   float64 // 0 (petite pièce, queue courte) à 1 (grande, queue longue)
	Damping    float64 // 0 (pièce claire) à 1 (pièce feutrée : les aigus s'éteignent vite)
	Width      float64 // 0 (mono) à 1 (décorrélée)
	PreDelayMs float64 // 0 à 200 ms
	Mix        float64 // 0 (sec, sans effet) à 1 (réverbération seule)
}

func Reverb(src io.Reader, sampleRate, channels int, s ReverbSettings) io.Reader
```

Ajoute de l'espace à un son : la réverbération d'une pièce, de la petite salle à la grande nef. C'est l'effet de **spatialisation** par excellence, à ne pas confondre avec [`StereoWidth`](#élargisseur-stéréo), qui élargit l'image sans ajouter de pièce.

- **Algorithme** : celui de « Freeverb » (Jezar at Dreampoint, domaine public), de Schroeder et Moorer : par canal, **huit filtres en peigne** avec amortissement des aigus dans la boucle, en parallèle, suivis de **quatre filtres passe-tout** en série. Les longueurs, choisies pour 44,1 kHz, sont mises à l'échelle des autres fréquences d'échantillonnage (la durée de la queue est la même à 44,1, 48 et 96 kHz : 1,27 s pour `RoomSize = 0,6`). Chaque canal a des longueurs décalées de 23 échantillons : la réverbération est décorrélée entre gauche et droite. L'entrée est la moyenne des canaux, retardée de `PreDelayMs`.
- **Durée de la queue** (RT60 mesuré sur la réponse impulsionnelle, amortissement 0,3) : 0,6 s à `RoomSize = 0,1`, 0,9 s à 0,4, 1,6 s à 0,7 et 5,3 s à 0,95.
- **Amortissement** : à `RoomSize = 0,8`, le rapport aigus / graves de la queue (de 0,6 à 1,6 s) passe de +12,5 dB sans amortissement à −23 dB avec `Damping = 1`.
- **Niveau calibré** : la partie réverbérée est mise à l'échelle sur l'énergie de la réponse impulsionnelle, calculée au démarrage : un bruit blanc ressort de la réverbération avec la même puissance qu'à l'entrée (±1,5 dB mesuré pour cinq combinaisons de taille, d'amortissement et de retard). `Mix` mélange le sec et le mouillé **à puissance constante** (`cos(Mix·π/2)` pour le sec, `sin(Mix·π/2)` pour le mouillé) : le volume ne bouge presque pas quand on monte le mélange (un fondu linéaire le ferait baisser de 3 dB à 0,5). `Mix = 0,25` met 15 % de la puissance en réverbération, `0,5` la moitié.
- **`Width`** mélange les deux canaux de la partie réverbérée (stéréo seulement) : `0` les rend identiques, `1` les laisse décorrélés. Ignoré en mono et au-delà de deux canaux.
- **La queue est coupée à la fin du flux.** La sortie a exactement la longueur de l'entrée, comme tous les effets : la réverbération d'un morceau s'arrête net avec le dernier échantillon. Pour la laisser mourir, ajoutez du silence à la fin de l'entrée (`ffmpeg … -af apad=pad_dur=4`). Après la fin du signal, la queue d'une petite pièce atteint exactement zéro, et les états minuscules sont ramenés à zéro (pas de nombres dénormalisés, qui ralentiraient le calcul après un long silence).
- Les valeurs sont ramenées dans leurs bornes (NaN compte pour 0). Si `Mix` est nul, ou si `sampleRate <= 0`, le flux est renvoyé tel quel. Un plafond doux (genou à 0,8) arrondit les pics qui dépasseraient la pleine échelle.
- **Coût** : 231 s de stéréo (`sample03`) traitées en 1,9 s (environ 120 fois le temps réel), 11 Mo de mémoire.
- **Mesuré sur les trois morceaux de `testdata/`** (`-fx reverb`, réglages par défaut : `RoomSize = 0,5`, `Damping = 0,5`, `Mix = 0,25`) : la sonie intégrée bouge de −0,2 à 0 LU (−19,4 / −13,3 / −7,9 LUFS) et la crête de 0 à 0,6 dB vers le bas ; la plage de sonie (LRA) baisse de 0,2 à 0,3 LU : une réverbération remplit les creux entre les notes.


## Renforçateur de transitoires

```go
type TransientSettings struct {
	Attack  float64 // de -1 (attaque aplatie) à +1 (attaque marquée)
	Sustain float64 // de -1 (queue raccourcie) à +1 (queue allongée)
}

func TransientShaper(src io.Reader, sampleRate, channels int, s TransientSettings) io.Reader
```

Accentue ou adoucit **l'attaque** des sons (l'impact d'une grosse caisse, d'une caisse claire, d'un médiator) et **leur queue** (la résonance qui suit), **sans seuil** : l'effet suit la forme des sons, pas leur niveau. C'est le seul effet de la bibliothèque qui augmente (ou diminue) réellement la dynamique à l'échelle de la note, là où le compresseur et le limiteur ne savent que la réduire.

- **Principe** : deux enveloppes du niveau crête sont comparées, une rapide (monte en 1 ms, retombe en 50 ms) et une lente (monte en 25 ms, retombe en 350 ms). Au début d'un son, la rapide devance la lente : c'est l'attaque. Dans la queue d'un son qui s'éteint, la lente reste au-dessus : c'est la tenue. Le gain en dB est proportionnel à l'écart : `Attack = +1` double, en dB, l'avance de l'attaque sur le corps du son ; `Attack = −1` la supprime ; `Sustain` fait de même avec la queue (il agit surtout sur la fin de la résonance, de 100 à 300 ms après l'attaque pour un son qui décroît en 90 ms : il change la pente de la décroissance).
- **Indépendant du niveau** : le même coup de caisse claire est traité de la même façon à −40, −30 et −20 dBFS (gain de +10,7, +11,2 et +11,4 dB sur l'attaque à `Attack = 1`).
- **Un son stable n'est pas touché** : un sinus de 40 Hz à 8 kHz sort à ±0,2 dB avec moins de −45 dB de distorsion, même aux réglages extrêmes. Sous 2 dB d'écart entre les enveloppes, aucun gain n'est appliqué (« zone morte ») : elle absorbe l'ondulation de l'enveloppe des très basses fréquences, qui sinon se transformerait en modulation.
- Gain limité à ±12 dB et lissé sur 0,5 ms. **Rien n'est touché sous −70 dBFS** (enveloppe lente) : pas de pompage du bruit de fond, et le silence reste exactement du silence.
- **Détection liée** (canal le plus fort, même gain pour tous) : l'image stéréo ne bouge pas.
- **Pas d'anticipation** : le gain ne peut pas précéder le tout premier millième de seconde d'un son. Aplatir une attaque (`Attack < 0`) garde donc les toutes premières périodes presque intactes.
- Renforcer l'attaque fait grimper les pics, sans compensation : un **plafond doux** (genou à 0,8) arrondit ceux qui dépasseraient la pleine échelle. **Sur un master très fort, dont les pics sont déjà à 0 dBFS, il reste presque rien à gagner sur l'attaque.**
- Sans effet (flux identique) si `Attack` et `Sustain` sont nuls ou NaN, ou si `sampleRate <= 0`. Les valeurs sont ramenées dans [−1, 1].

**Mesuré sur les trois morceaux de `testdata/`** (`-fx transient:attack=0.7 -loudness`, rapport crête / sonie intégrée, appelé PLR, avant → après) :

| Morceau (crête d'origine) | `attack=0.7` | `attack=-0.7` | `sustain=0.7` | `sustain=-0.7` |
|---------------------------|--------------|---------------|---------------|----------------|
| calme (−2,8 dBFS) | PLR 16,4 → 17,8 (+1,4) | 16,4 → 14,6 (−1,8) | −0,1 | 0,0 |
| moyen (−0,2 dBFS) | 13,1 → 12,8 (−0,3) | 13,1 → 12,6 (−0,5) | −0,3 | −0,3 |
| très fort (0,0 dBFS) | 7,7 → 7,5 (−0,2) | 7,7 → 7,5 (−0,2) | −0,1 | −0,3 |

L'effet n'est net que sur le morceau calme : sur les deux autres, dont les crêtes sont déjà au plafond, la crête ne peut plus monter et le gain d'attaque est absorbé par le plafond doux (la crête reste à −0,1 / 0,0 dBFS). La plage de sonie (LRA), qui mesure des fenêtres de 3 s, ne bouge presque pas (de −0,9 à +0,6 LU) : ce n'est pas la bonne mesure pour une dynamique à l'échelle de la note, d'où le PLR et le facteur de crête (testé sur une boucle de batterie synthétique : 14,3 dB → 20,3 dB à `Attack = 0,8` et 13,4 dB à `−0,8`).


## Crossfeed pour casque

```go
type CrossfeedSettings struct {
	Strength float64 // 0 (sans effet) à 1 (graves presque ramenés au centre)
	CutoffHz float64 // en dessous, les canaux se mélangent ; <= 0 → 700
	DelayMs  float64 // retard du signal croisé, de 0 à 1 ms ; 0 par défaut
}

func Crossfeed(src io.Reader, sampleRate, channels int, s CrossfeedSettings) io.Reader
```

Adapte un mixage stéréo à l'écoute **au casque** : il mélange un peu de chaque canal dans l'autre, comme le fait l'air entre deux enceintes, ce qui réduit la fatigue d'écoute d'un mixage qui isole trop les deux oreilles (une guitare à gauche, une basse à droite). C'est l'inverse de [`StereoWidth`](#élargisseur-stéréo), qui élargit l'image pour les enceintes.

```
G' = G + k × (PB(D) − PB(G))      D' = D + k × (PB(G) − PB(D))      avec k = Strength / 2
```

`PB` est un passe-bas du premier ordre à `CutoffHz`. En termes milieu / côté :

- **le milieu (`G + D`) n'est jamais touché** : le résultat reste compatible mono, et un son au centre ressort **à l'identique, bit à bit** (testé à −12 dBFS) tant que ses pics restent sous le plafond doux commun à tous les `-fx` (voir [`-fx`](cli.md#ajouter-des-effets--fx)) : sur `sample03` copié en `G = D`, 3 % des échantillons diffèrent, tous des pics à plus de −1,9 dBFS (écart maximal 1 563 sur 32 768) ;
- **seul le côté (`G − D`) est réduit sous la coupure**, d'un facteur voisin de `1 − Strength` aux basses fréquences : `Strength = 0,6` réduit le côté de 7,5 dB à 100 Hz, `Strength = 1` de 17 dB (le déphasage du passe-bas empêche l'annulation exacte) ; les aigus ne bougent presque pas (côté à 2 kHz : −0,5 dB, à 8 kHz : −0,03 dB, à `Strength = 1`).
- Un son entièrement à gauche fuit à droite aux graves seulement : −10,5 dB à 100 Hz et moins de −30 dB à 8 kHz pour `Strength = 0,6`.
- **`DelayMs`** retarde le signal croisé (0,25 ms environ imite la différence de temps entre les deux oreilles) : plus de profondeur, mais les sons du centre sont colorés (2,4 dB au plus entre 300 Hz et 1 kHz à `Strength = 0,6`), et le milieu n'est alors plus rigoureusement préservé. 0 par défaut.
- Seuls les flux stéréo (`channels = 2`) sont traités ; mono et plus de deux canaux sont renvoyés tels quels, de même que `Strength` nul ou NaN, `sampleRate <= 0` ou `CutoffHz >= Nyquist`. Les valeurs sont ramenées dans leurs bornes.
- **Mesuré avec `-fx crossfeed`** (`Strength = 0,6`) sur les trois morceaux de `testdata/` (sonie du milieu et du côté, calculée sur `(G+D)/2` et `(G−D)/2`) : le milieu ne bouge pas (−24,3 / −16,8 / −11,1 LUFS avant et après) ; le côté baisse de 5,2 / 1,2 / 0,5 dB (calme / moyen / très fort). Le morceau calme est celui dont les graves sont les plus latéraux ; sur les deux autres, les graves sont déjà presque au centre et il y a peu à ramener.


## Élargisseur stéréo

```go
func StereoWidth(src io.Reader, sampleRate, channels int, width float64) io.Reader
```

Traitement mid/side : `width` multiplie le signal « côté » (`(G − D) / 2`) au-dessus de **200 Hz**. `1` = inchangé, `1,5` à `2` = nettement plus large, `0` = mono au-dessus de 200 Hz ; la valeur est ramenée dans `[0, MaxWidth]` (`MaxWidth = 3`).

- Le milieu (`(G + D) / 2` : voix, grosse caisse, basse) n'est jamais modifié, donc **`G + D` est conservé** : le résultat reste compatible mono (à 1 LSB d'arrondi près).
- Sous 200 Hz, le côté garde son niveau (il est seulement déphasé par le filtre de séparation de Linkwitz-Riley d'ordre 4) : les basses restent au centre.
- Seuls les flux stéréo (`channels = 2`) sont traités ; mono, plus de deux canaux, `width = 1` ou `NaN` renvoient le flux tel quel.
- Élargir peut faire dépasser la pleine échelle à un signal déjà fort : les pics sont arrondis par un plafond doux (genou à 0,8), pas écrêtés net. Il n'y a pas de gain de compensation : le niveau du centre reste le même.

C'est un effet de **spatialisation** : il ne touche pas à la dynamique.


## Préréglages à une bande

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
