# Profils prédéfinis

[← Retour au README](../README.md)

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
- **Valeurs** : les courbes sont des points de départ raisonnables, pas une norme (il n'existe pas de standard officiel). Elles sont définies dans [profiles.go](../profiles.go).
- **Utilitaires** : `Profiles()` liste les profils, `ParseProfile("rock")` en retrouve un par son nom (insensible à la casse), `p.String()` donne son nom, et `p.Bands()` renvoie une copie de ses bandes d'égalisation pour partir d'elles avec `Equalizer` (pour un profil `radio-*`, ce n'est que l'égalisation : la multibande, l'élargissement et le limiteur n'y sont pas ; `radio-natural` n'a aucune bande).

### Effets dynamiques par profil

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

La compensation du compresseur et la saturation relèvent le niveau pour contrebalancer la baisse du gain automatique : à −18 dBFS et 1 kHz, chaque profil avec effets de ce tableau rend le niveau d'entrée à moins de 0,4 dB près. Les profils `radio-*` ne suivent pas cette règle (voir [Profils radio](#profils-radio)). Les réglages sont dans `profileCompression` et `profileSaturation` ([profiles.go](../profiles.go)).

### Profils radio

Les profils `radio-*` appliquent une chaîne de diffusion complète, dont la structure s'inspire de celle des processeurs de diffusion (nettoyage, niveau, mise en forme par bandes, limitation) :

```
égalisation → AGC → multibande (4 bandes) → saturation (certains) → élargissement stéréo → limiteur −1 dBFS
```

- **AGC** : un compresseur large bande très lent (attaque 100–200 ms, relâchement 0,8–1,5 s) qui nivelle les écarts de volume d'un morceau à l'autre, avec une compensation de 3,5 à 7,5 dB.
- **Multibande** : quatre bandes séparées à 120 Hz, 1,2 kHz et 6 kHz (sub-basses, médiums, présence, air), seuil de −20 à −26 dBFS, ratios de 1,5:1 (`radio-natural`) à 3,5:1 (`radio-club`).
- **Élargissement** : `radio-hits` ×1,25, `radio-rock` ×1,2, `radio-urban` ×1,15, `radio-club` ×1,4, `radio-gold` ×1,15 ; `radio-talk` et `radio-natural` gardent l'image d'origine.
- **Limiteur** : plafond à −1 dBFS (1 dB de marge pour les crêtes inter-échantillons), anticipation 5 ms, relâchement de 50 ms (`radio-club`) à 150 ms (`radio-natural`). C'est le dernier étage et **il remplace le gain automatique** des autres profils : la garantie vient du limiteur, pas d'un gain négatif.
- Saturation : `radio-rock` (+4 dB), `radio-club` (+3 dB) et `radio-gold` (+3 dB, asymétrie 0,3).

Tous les réglages sont dans [profiles.go](../profiles.go) et [profiles_radio.go](../profiles_radio.go).

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

¹ Mesurées avec `-loudness` (UIT-R BS.1770 / EBU R128, voir [Mesure de sonie](mesure.md#mesure-de-sonie)), en différence avec l'original ; les mêmes valeurs, pour l'original et quelques sorties, ont été recoupées avec le filtre `ebur128` de ffmpeg à 0,1 LU près, l'arrondi de l'affichage (par exemple −13,302 LUFS et 8,71 LU pour le morceau moyen, contre −13,3 et 8,7 ici). ² Rapport d'énergie côté/milieu, en dB. L'élargisseur seul apporte `20·log10(largeur)` (+1,9 dB pour ×1,25) ; le reste vient de la compression des basses, très centrées, qui augmente la part relative du côté.

À lire dans ce tableau :

- **La sonie converge** vers une valeur commune : un morceau calme est relevé de 2 à 8 LU, un master très fort est **abaissé** de 0,3 à 4,5 LU (l'AGC et la multibande le compressent avant le limiteur). Ce n'est pas un volume maximal garanti.
- **La plage de sonie est réduite de 0,9 à 5,4 LU.** Si ces profils donnent l'impression d'un son plus « vivant », ce serait plutôt par le relâchement lent des bandes graves (le punch reste), la présence relevée des médiums et des aigus et l'élargissement stéréo : c'est une hypothèse, pas une mesure. Un renforçateur de transitoires ou une expansion, qui augmenteraient réellement la dynamique, ne sont pas implémentés.
- **Intensité** : `0` laisse le signal intact (octet pour octet) ; en dessous de 1, ratios, élargissement et égalisation sont réduits proportionnellement ; au-delà de 1, ratios et élargissement croissent (largeur ×1,75 pour `radio-hits` à l'intensité 3) tandis que les compensations de niveau restent à leur valeur d'intensité 1. Le plafond de −1 dBFS tient à toute intensité.
- Sur un sinus à −18 dBFS, ces profils relèvent le niveau de +3,6 dB (`radio-natural`) à +10,4 dB (`radio-club`) : un signal faible est remonté par la compensation sans que les bandes ne le compressent. C'est ce que fait un AGC avec des passages calmes ; `radio-natural` et `radio-gold` sont les moins marqués.
- Avec `sampleRate` bas (séparations au-delà de Nyquist, par exemple 8 kHz), la multibande est ignorée ; le reste de la chaîne et le limiteur continuent d'agir.

### Saturation de type tube

La saturation ([saturation.go](../saturation.go)) applique, sur le signal normalisé, `f(x) = tanh(g·x + β) − tanh(β)` puis normalise pour que la pleine échelle reste à la pleine échelle (`g` = drive, `β` = asymétrie). Elle arrondit les pics au lieu de les écrêter.

- **Asymétrie** : à `β = 0` (Rock, HipHop, Electronic), la courbe est symétrique et ne produit que des **harmoniques impaires** (3e, 5e…), plutôt dures. Avec `β > 0` (Jazz, Tube, RadioGold), elle est asymétrique comme celle d'un tube et ajoute des **harmoniques paires** (2e, 4e…), perçues comme plus chaudes. À 200 Hz, 2e harmonique de 1,9 % (Jazz) et 3,7 % (Tube) à −18 dBFS, 8,8 % et 9,6 % à −3 dBFS, contre 0 pour les profils sans asymétrie. Un filtre passe-haut (7 Hz) retire la composante continue qu'introduit l'asymétrie, et le silence reste un silence exact.
- **`β` est exprimé dans l'argument de `tanh`, donc indépendant du drive** : le gain des petits signaux vaut `g·sech²(β)` (au facteur de normalisation près) et grandit avec le drive. Une asymétrie exprimée dans le domaine d'entrée, `tanh(g·(x+b))`, repousse au contraire le point de fonctionnement dans la zone saturée à mesure que `g` grandit : mise à l'échelle avec l'intensité, elle rendait `Jazz` quasi muet à −18 dB et `Tube` à −49 dB à l'intensité 3. Un test de régression garde ce comportement.
- **Suréchantillonnage ×4** : la non-linéarité s'exécute à 4 fois la fréquence d'échantillonnage, entre deux filtres passe-bas d'ordre 8 (coupure 0,43 × fs). Les harmoniques qui dépasseraient Nyquist sont atténuées avant de se replier dans le spectre audible : sur un sinus de 15 kHz poussé de 6 dB, le repliement à 900 Hz tombe de 6,4 % à 0,35 % (−25 dB). Ces filtres arrondissent légèrement les aigus extrêmes des profils saturés (mesuré à 44,1 kHz : −0,2 dB à 15 kHz, −3 dB à 18 kHz, −10 dB à 20 kHz).
- **Plusieurs étages** : l'étage peut être mis en cascade (voir [Ampli à tubes](briques.md#ampli-à-tubes)), avec inversion de phase et couplage entre étages.
- **Plafond doux** : une onde saturée, une fois limitée en bande, peut dépasser la pleine échelle (la fondamentale d'une onde carrée vaut 1,27 fois son sommet). Un plafond doux arrondit ces pics au lieu de les laisser écrêter net : la saturation ne produit jamais la pleine échelle.
- **Un état par canal** : les filtres et le filtre anti-continu sont propres à chaque canal ; un canal silencieux reste silencieux (pas de diaphonie).
- **Le grain s'entend surtout dans les basses** : le gain automatique compense le drive là où l'égalisation est sous son pic.
