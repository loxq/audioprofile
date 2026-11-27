# Exemple : une webradio Go → Icecast avec traitement du son

[← Retour au README](../../README.md)

Cet exemple montre **où s'intercaler** audioprofile dans une webradio : une playlist planifiée est décodée, traitée par un profil (qui peut changer d'une émission à l'autre), ré-encodée en mp3 puis poussée sur Icecast, avec l'annonce du titre en cours.

```
 votre programme                    audioprofile                      votre programme
┌──────────────────────┐        ┌──────────────────────┐         ┌──────────────────────────┐
│ planning ──▶ décodage │ ─ PCM ▶│ Switcher             │ ─ PCM ─▶│ encodage mp3 ──▶ Icecast │
│  (morceau suivant)    │        │  profil, compresseur,│         │  (ffmpeg dans l'exemple) │
│                       │        │  limiteur, effets…   │         └──────────────────────────┘
└─────────┬─────────────┘        └──────────▲───────────┘                    
          │ au début de chaque morceau :    │ Swap, sans clic
          └─ profil du morceau ─────────────┘     + annonce du titre (admin Icecast)
```

Le traitement s'insère **entre votre décodeur et votre encodeur**, là où le son est du PCM. Vos fichiers sont déjà normalisés (même codec, même débit, même fréquence d'échantillonnage) : il n'y a rien à rééchantillonner, et l'étage de traitement n'est là que pour la couleur et la protection (égalisation, compression, limiteur).

## Essayer

Il faut `ffmpeg` (avec `libmp3lame`) et un serveur Icecast qui tourne, par exemple le paquet `icecast2` de votre distribution avec son mot de passe de source.

```sh
go run ./examples/webradio \
  -playlist playlist.txt \
  -output icecast://source:MOT-DE-PASSE@127.0.0.1:8000/live \
  -admin-password MOT-DE-PASSE-ADMIN \
  -config reglages.yaml
```

Écoutez `http://127.0.0.1:8000/live` : les titres s'affichent à chaque morceau et le profil change avec la liste. Sans Icecast, `-output sortie.mp3` écrit un fichier pour essayer.

### La liste

Un chemin par ligne. `#profile: nom` s'applique aux morceaux suivants (« #profile: » seul revient au profil par défaut), et ce qui suit la première barre verticale est le titre annoncé. Les chemins relatifs sont relatifs à la liste.

```text
# le matin
#profile: radio-talk
journal.mp3 | Le journal
chronique.mp3

#profile: radio-hits
musique/a.mp3 | Artiste - Titre
musique/b.mp3
```

Les profils sont ceux de la bibliothèque (`radio-talk`, `radio-hits`, `tube`…) ou ceux de votre fichier `-config` ([le format](../../docs/reglage.md)). Un profil inconnu est signalé et la chaîne garde le précédent.

### Les options

| Option | Rôle |
|--------|------|
| `-playlist` | la liste de morceaux |
| `-output` | `icecast://source:mdp@hote:8000/montage`, ou un fichier `.mp3` |
| `-bitrate` | débit mp3 (128k par défaut) |
| `-rate`, `-channels` | format du flux traité : 44100 Hz, 2 canaux (doit être celui de vos fichiers) |
| `-config`, `-profile`, `-intensity`, `-fx` | profils personnalisés, profil par défaut (vide : aucun traitement), intensité, effets après le profil |
| `-admin-user`, `-admin-password` | administration d'Icecast : annonce le titre en cours (vide : pas d'annonce) |
| `-panel 127.0.0.1:8080` | ouvre le panneau de réglage sur le flux qui part vers Icecast |
| `-loop` | repasser la liste quand elle est finie (vrai par défaut) |
| `-retry` | pause avant de relancer l'encodeur s'il s'arrête (3 s) |

## Ce que fait l'exemple, et ce que vous reprenez

Trois morceaux de code valent la peine d'être repris, le reste est de la tuyauterie ffmpeg que vous avez déjà :

- **`trackSource`** ([source.go](source.go)) : enchaîne les morceaux décodés en **un seul flux PCM continu**. C'est ce qui permet à la chaîne de vivre d'un morceau à l'autre : l'AGC, le compresseur et le limiteur gardent leur état, et rien n'est vidé à chaque fin de fichier. Il appelle `onTrack` quand un morceau entre dans la chaîne, saute un morceau qui ne s'ouvre pas (une radio ne s'arrête pas pour un fichier cassé), et rend toujours des trames entières.
- **`applyProfile`** ([radio.go](radio.go)) : au début de chaque morceau, passe la chaîne au profil demandé avec `Switcher.Swap` (`live.Pipeline.Build`), sans clic, et ne fait rien si le profil est déjà en place. Avec le panneau ouvert, le changement passe par `live.Tuner` pour que le panneau reste cohérent.
- **L'annonce du titre** ([icecast.go](icecast.go)) : `/admin/metadata?mode=updinfo` d'Icecast, hors du chemin du son. Au démarrage, le premier titre est annoncé avant que l'encodeur soit connecté : Icecast le refuse, donc l'annonce est retentée un moment.

Les deux endroits où votre programme se branche sont de petites interfaces :

<!-- doccheck:skip -->
```go
type scheduler interface{ Next(ctx context.Context) (Track, bool) }                 // le morceau suivant
type opener func(ctx context.Context, path string) (io.ReadCloser, error)           // son PCM s16le du morceau
```

Remplacez la liste par votre planificateur et le décodeur ffmpeg par votre décodeur Go : `options.hooks` prend les deux. L'encodeur et la poussée vers Icecast sont ici assurés par ffmpeg (`-f mp3 icecast://…`), un seul processus qui reçoit le PCM traité ; si vous avez déjà votre propre encodeur et client Icecast, branchez-les sur la sortie du `Switcher` (`io.Copy(votreEncodeur, sw)`).

Le programme relance l'encodeur s'il s'arrête (Icecast redémarré, réseau coupé) et reprend là où en est la liste, comme une radio en direct : le son de la coupure est perdu.

## À savoir

- **Le niveau.** Vos fichiers sont normalisés, mais les profils `radio-*` ramènent le niveau vers une valeur commune (voir [Profils prédéfinis](../../docs/profils.md)) : le traitement change la sonie. Comparez avant et après sur vos morceaux (`MeasureLoudness`, ou le mode `-ab` de la commande) avant de choisir un profil de production.
- **Le titre annoncé** arrive quand le morceau entre dans la chaîne ; les auditeurs l'entendent un peu plus tard, de la taille des tampons de l'encodeur et d'Icecast.
- **L'enchaînement** n'ouvre pas le morceau suivant à l'avance : le démarrage du décodeur (quelques dizaines de millisecondes) est absorbé par les tampons. Pour un enchaînement sans aucun accroc, ouvrez le suivant pendant que le courant joue.
- **Pas de fondu enchaîné** entre deux morceaux : ils sont mis bout à bout.
- Le panneau n'a pas de mot de passe : il n'est écouté que sur une adresse de bouclage. Avec lui, le planning l'emporte sur vos réglages à chaque changement de profil.

## Tests

```sh
go test ./examples/webradio
```

Les tests de bout en bout (qui demandent `ffmpeg` avec `libmp3lame`, et se sautent sinon) lancent la vraie chaîne contre un faux Icecast qui parle assez de protocole pour qu'ffmpeg s'y connecte, à 10 fois le temps réel. Ils vérifient : l'authentification et les en-têtes de la source, **un profil différent par morceau mesuré dans le mp3 reçu** (+9 dB entre `quiet` et `loud`), l'annonce des titres dans l'ordre (la première est retentée), un fichier cassé ou absent sauté sans être annoncé, un profil inconnu qui laisse le précédent, l'encodeur relancé quand la connexion est coupée, et un réglage du panneau qui change le son envoyé. Les unités couvrent la liste, l'enchaînement des morceaux, la cadence en temps réel et l'annonce.

Je l'ai aussi essayé contre un vrai Icecast 2.4.4 en local : le flux est reçu et écoutable, les titres s'affichent, et le niveau change entre les deux morceaux comme prévu.
