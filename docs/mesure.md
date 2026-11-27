# Mesure de sonie, normalisation et comparaison A/B

[← Retour au README](../README.md)

## Mesure de sonie

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


## Normalisation de sonie

```go
type NormalizeOptions struct {
	TargetLUFS float64 // sonie intégrée visée, dans [-70, 0]
	CeilingDB  float64 // plafond des crêtes d'échantillon, dans [-40, 0] (0 = pleine échelle)
	MaxBoostDB float64 // gain maximal accordé ; <= 0 → 24
}

func NormalizeLoudness(pcm []byte, sampleRate, channels int, o NormalizeOptions) ([]byte, NormalizeResult, error)
```

Amène la **sonie intégrée** d'un morceau à une cible, par exemple **−14 LUFS** (niveau de référence de nombreux services de streaming) ou **−23 LUFS** (diffusion EBU R128). Le signal reçoit un gain, puis le [limiteur](briques.md#limiteur) à `CeilingDB` (−1 dBFS en général) : les crêtes ne dépassent jamais le plafond. Le gain et le limiteur sont dans la même chaîne, en nombres flottants : un gain qui dépasse la pleine échelle n'est pas écrêté avant que le limiteur ne le voie.

- **Itération** : le limiteur baisse un peu la sonie obtenue, donc le gain est corrigé et la sonie mesurée de nouveau, jusqu'à tomber à 0,1 LU de la cible (au plus 8 passes). Chaque passe repart du signal d'origine. Mesuré avec ffmpeg sur les trois morceaux de `testdata/`, à −14 et −23 LUFS : de −14,039 à −14,005 et de −23,007 à −23,005 LUFS.
- **Baisser** n'a pas besoin du limiteur (si les crêtes restent sous le plafond) : le résultat est alors exactement le signal multiplié par un gain.
- **Cible inatteignable** : le résultat le dit (`Reached`, `Reason`) et rend ce qui a été obtenu de plus proche, sans dépasser le plafond. Deux cas : un morceau très faible qui demanderait plus de `MaxBoostDB` de gain, et un plafond (ou des crêtes) qui empêche d'aller plus fort (le limiteur écraserait le morceau : on s'arrête quand le gain n'apporte plus de sonie).
- `NormalizeResult` donne aussi la sonie `Before` et `After`, le gain appliqué, et si le limiteur est intervenu (`Limited`).
- Silence, flux vide ou moins de 400 ms de signal : renvoyé tel quel, sans erreur (`Reason` explique). Une cible ou un plafond invalide (NaN, hors bornes) donne une erreur et le signal tel quel. L'entrée n'est jamais modifiée.
- Le plafond porte sur les crêtes d'échantillon, **pas sur les crêtes inter-échantillons** (*true peak*) : visez −1 dBFS ou moins pour de la marge.
- Travaille sur un tampon en mémoire.


## Comparaison A/B à sonie égale

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

La commande les assemble avec l'option `-ab` ([voir la commande](cli.md#comparer-à-loreille--ab)).
