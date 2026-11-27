# Dans un programme de streaming

[← Retour au README](../README.md)

audioprofile est faite pour être **importée** : le traitement est une suite de fonctions `io.Reader → io.Reader`, sans état global ni goroutine cachée. La commande `audioprofile` n'est qu'un outil de test posé dessus, et l'outillage de réglage (fichier YAML, rechargement à chaud, panneau web) est fourni en paquets séparés pour que votre programme puisse l'embarquer en développement et s'en passer en production.

## Les paquets

```
.                  paquet audioprofile : les traitements (égaliseur, dynamique, effets, ampli, profils), le
                   Switcher (changement de chaîne à chaud) et la mesure de sonie. Aucune dépendance externe.
fx/                registre des effets par nom : fx.Parse("delay:time=300"), paramètres, bornes, validation
config/            fichier de réglages YAML : charger, valider, fusionner avec les valeurs par défaut, écrire
panel/             panneau de réglage web : serveur HTTP, page embarquée, types et schéma des curseurs
live/              assemblage pour régler en direct : Pipeline (la chaîne demandée), Watch (rechargement du
                   fichier), Tuner (panneau branché sur un Switcher)
cmd/audioprofile/  la commande de test : options, comparaison A/B, mesure de sonie, -serve, -watch
```

Les dépendances vont dans un seul sens : `cmd` → `live` → (`panel`, `config`, `fx`) → `audioprofile`.

- `panel` ne connaît que la bibliothèque : il ne sait pas ce que veut dire un réglage. C'est `live.Tuner` (qui implémente `panel.Backend`) qui vérifie les états reçus de la page et change la chaîne.
- `config` et `fx` ne dépendent que de la bibliothèque (`config` utilise `fx` pour les valeurs par défaut des effets).
- La seule dépendance externe est `gopkg.in/yaml.v3`, tirée par `config` (donc par `live` et `cmd`) : importer `audioprofile`, `fx` ou `panel` n'ajoute rien à votre binaire.

## Brancher le traitement

Le flux traité est un `io.Reader` : on lit `sw`, on écrit où l'on veut. Le panneau règle la chaîne pendant que le flux coule.

```go
cfg, _ := config.Load("reglages.yaml") // "" ou nil : les valeurs par défaut
pl := live.Pipeline{Profile: "rock", Intensity: 1, Rate: 44100, Channels: 2, FX: []string{"delay:time=300"}}
build, _ := pl.Build(cfg)

sw := audioprofile.NewSwitcher(src, pl.Rate, pl.Channels, build) // le flux traité : lire sw
tuner, _ := live.NewTuner(cfg, pl, sw)
url, stop, _ := panel.Serve("127.0.0.1:8080", tuner) // le panneau, à garder en développement
defer stop()
// ou : stop, _ := live.Watch("reglages.yaml", sw, pl, live.WatchOptions{Logf: log.Printf})
```

- `live.Pipeline` décrit la chaîne (un profil **ou** un ampli, puis des effets au format de `fx.Parse`). `Build(cfg)` la vérifie et renvoie la fonction que `NewSwitcher` et `Swap` attendent.
- `live.Watch` recharge le fichier quand il change ; `panel.Serve` ouvre le panneau. Les deux passent par `Switcher.Swap`. Ils sont exclusifs dans la commande (le panneau et le fichier se corrigeraient mutuellement), pas dans la bibliothèque : à vous de décider.
- Le serveur du panneau n'a pas de mot de passe. Ne l'écoutez que sur une adresse de bouclage (`127.0.0.1`) ; voir [Régler en direct](reglage.md#panneau-de-réglage--serve) pour ce qu'il refuse.

## Dans une webradio (Go → Icecast)

Si votre programme décode déjà des fichiers et ré-encode vers Icecast, l'étage de traitement s'insère **entre le décodeur et l'encodeur**, là où le son est du PCM :

```
planning ──▶ décodage ──▶ [ Switcher : profil + effets ] ──▶ encodage ──▶ Icecast
                                     ▲
              au début de chaque morceau : Swap vers le profil de l'émission
```

- **Un seul flux continu** : fournissez au `Switcher` un lecteur unique qui enchaîne les morceaux décodés, pas un lecteur par fichier. L'AGC, le compresseur et le limiteur gardent ainsi leur état d'un morceau à l'autre, et rien n'est vidé à chaque fin de fichier.
- **Un profil par émission** : au changement de morceau ou d'émission, `Switcher.Swap` (avec `live.Pipeline.Build`) change de chaîne sans clic ; un profil identique ne coûte rien.
- **Un format fixe** : la bibliothèque ne rééchantillonne pas. Des fichiers de même fréquence d'échantillonnage et de même nombre de canaux, décodés en s16le, sont ce qu'il faut.
- **La cadence** : le flux doit partir au rythme du temps réel (un décodeur lit bien plus vite que la musique ne passe). L'exemple le fait avant le traitement.

Un exemple complet, testé, avec ffmpeg, un vrai protocole Icecast et l'annonce des titres : [examples/webradio](../examples/webradio/README.md).

## Le Switcher

`NewSwitcher(src, rate, channels, build)` renvoie un flux dont la chaîne se remplace à chaud avec `Swap(build)`.

- **Sans clic** : la nouvelle chaîne est d'abord mise en régime sur les 2 dernières secondes déjà traitées (filtres, enveloppes des compresseurs, AGC, lignes à retard) ; sa sortie est jetée. Puis un fondu enchaîné de 30 ms fait passer de l'ancienne sortie à la nouvelle, sur le même instant du signal.
- **Aucune latence ajoutée**, et le flux garde exactement la longueur de la source.
- **Coût** : un changement rejoue 2 s de signal, soit 25 à 55 ms de calcul (profils `rock` et `radio-hits`, mesuré). Ne le déclenchez pas à chaque image : le panneau limite ses envois à un toutes les 140 ms environ.
- **Concurrence** : `Read` doit être appelé par un seul goroutine à la fois ; `Swap` peut l'être depuis n'importe quel autre. Un changement demandé pendant un fondu attend sa fin ; deux changements demandés avant qu'il soit pris en compte : seul le dernier compte.
- **Limite** : une queue de plus de 2 s (réverb très longue, écho à forte rétroaction) repart de ce que l'historique permet de reconstruire.

## En production

Le panneau, `Watch` et `config` ne servent qu'à régler et déboguer. Une fois les réglages trouvés :

- gardez une chaîne fixe : `audioprofile.ApplyProfile(src, rate, channels, audioprofile.Rock, 1)`, ou les briques assemblées à la main (voir [Les briques](briques.md)), ou des réglages de profil gardés en Go (`Profile.Settings()` puis `ApplyProfileSettings`) ;
- l'export du panneau (« Exporter ») donne le YAML du profil réglé et la ligne de commande équivalente ;
- `fx.Parse` reste utile en production pour construire une chaîne d'effets depuis un texte ou une configuration de votre application.

## Brancher un autre moteur sur le panneau

`panel.Serve` et `panel.Handler` prennent un `panel.Backend` : cinq méthodes (`Schema`, `State`, `Apply`, `Preset`, `Export`). `live.Tuner` en est une implémentation pour les chaînes de ce dépôt ; si votre programme a sa propre notion de chaîne, implémentez l'interface et gardez la page, le serveur et leurs protections.
