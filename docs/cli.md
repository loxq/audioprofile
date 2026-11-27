# La commande `audioprofile`

[← Retour au README](../README.md)


L'outil ([cmd/audioprofile](../cmd/audioprofile/main.go)) lit du PCM s16le sur l'entrée standard et écrit le résultat sur la sortie standard, donc il se place directement dans un pipe avec ffmpeg.

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

## Ajouter des effets (`-fx`)

```sh
# Un passe-haut à 80 Hz d'ordre 4 (24 dB/octave) seul, sans profil
./audioprofile -fx hpf:freq=80,order=4 < musique.raw > sortie.raw

# Après un profil, plusieurs effets, dans l'ordre : passe-haut puis passe-bas
./audioprofile -profile rock -fx hpf:freq=60 -fx lpf:freq=14000,order=4 < musique.raw > sortie.raw
```

`-fx nom` ou `-fx nom:cle=valeur,cle=valeur` ajoute un effet **après le profil ou l'ampli**, dans l'ordre où les `-fx` apparaissent. L'option est répétable, et **`-profile` devient facultatif dès qu'un `-fx` est donné**. `./audioprofile -list-fx` affiche les effets, leurs paramètres et leurs valeurs par défaut.

| Effet | Paramètres | Rôle |
|-------|------------|------|
| `crossfeed` | `strength` (0,6), `cutoff` (Hz, 700), `delay` (ms, 0) | crossfeed pour casque (stéréo seulement) |
| `notch` | `freq` (Hz, obligatoire), `q` (0,1 à 1000, défaut 10) | filtre coupe-bande étroit (ronflement du secteur) |
| `gate` | `threshold` (−50), `ratio` (4), `range` (dB, 30), `attack` (ms, 2), `release` (ms, 120) | gate / expandeur descendant |
| `parcomp` | `threshold` (−30), `ratio` (8), `attack` (ms, 10), `release` (ms, 120), `makeup` (dB, 16), `mix` (0,3) | compression parallèle |
| `deess` | `freq` (5000), `threshold` (dBFS, −30), `ratio` (4) | de-esser : atténue les sifflantes au-dessus du seuil |
| `exciter` | `freq` (3000), `amount` (0,3), `drive` (dB, 20), `warmth` (0) | harmoniques ajoutées aux aigus forts |
| `lofi` | `lowcut` (Hz, 100), `highcut` (Hz, 5000), `bits` (12), `samplerate` (Hz, 0), `wobble` (0,4), `drive` (dB, 4), `crackle` (0,3), `hiss` (0,2) ; 0 désactive une partie | lo-fi : vieux support (bande, bande magnétique, vinyle) |
| `chorus` | `delay` (ms, 18), `depth` (ms, 3), `rate` (Hz, 0,8), `voices` (3), `mix` (0,5) | chorus : voix retardées modulées |
| `flanger` | `delay` (ms, 2), `depth` (ms, 1,5), `rate` (Hz, 0,25), `feedback` (0,5), `mix` (0,5) | flanger : peigne qui balaie, avec rétroaction |
| `delay` | `time` (ms, 350), `feedback` (0,4), `mix` (0,3), `damping` (Hz, 0 = aucun), `pingpong` (0 ou 1) | échos, avec ping-pong stéréo |
| `reverb` | `room` (0,5), `damping` (0,5), `width` (1), `predelay` (ms, 0), `mix` (0,25) | réverbération (Freeverb), queue coupée à la fin du flux |
| `transient` | `attack`, `sustain` (de −1 à 1, défaut 0) | renforçateur de transitoires |
| `hpf` | `freq` (Hz, obligatoire), `order` (1 à 8, défaut 2) | filtre passe-haut de Butterworth |
| `lpf` | `freq` (Hz, obligatoire), `order` (1 à 8, défaut 2) | filtre passe-bas de Butterworth |

**Tout effet de `-fx` active un plafond doux** (genou à 0,8 : les pics au-dessus d'environ −1,9 dBFS sont arrondis) : les effets qui ajoutent de l'énergie (attaque, harmoniques, réverbération) pourraient sinon faire écrêter, et la mesure ne distingue pas un effet qui travaille d'un effet inactif. Sur un master dont les crêtes sont déjà à 0 dBFS, cela retouche donc un peu les pics même avec un réglage neutre.

L'analyse est stricte : un effet ou un paramètre inconnu (le message liste les valides), un paramètre obligatoire absent ou donné deux fois, une valeur qui n'est pas un nombre fini, ou une fréquence hors de `]0, rate/2[` sont des erreurs. Les effets sont dans la chaîne de traitement : `-ab`, `-loudness` et `-target-lufs` les entendent (avec `-ab`, le côté B est nommé d'après la chaîne : `traité (flat + lpf:freq=3000)`).


## Comparer à l'oreille (`-ab`)

```sh
ffmpeg -loglevel error -i musique.mp3 -f s16le -ar 44100 -ac 2 - \
  | ./audioprofile -profile radio-hits -ab \
  | ffplay -nostats -loglevel error -f s16le -ar 44100 -ac 2 -nodisp -
```

(`-nostats` évite que la ligne d'état de ffplay ne se mélange aux annonces ci-dessous.)

La sortie alterne l'**original (A)** pendant 5 s, puis le **traité (B)** pendant 5 s, et ainsi de suite, sur la même musique (on change de source, pas de position). `-ab-seconds` règle la durée des segments (`-ab-seconds 2` pour comparer plus vite). Avant de jouer, la commande affiche sur la sortie d'erreur la convention (A = original, B = traité), la durée des segments et **le gain appliqué** : le côté le plus fort est atténué pour que les deux aient la même sonie (LUFS, voir [`MatchGains`](mesure.md#comparaison-ab-à-sonie-égale)).

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


## Mesurer la sonie (`-loudness`)

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


## Régler la sonie (`-target-lufs`)

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
