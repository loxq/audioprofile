# CLAUDE.md

Consignes pour travailler sur ce dépôt. Elles viennent des échanges avec le mainteneur ; en cas de doute, elles passent avant les habitudes par défaut.

## Le projet

**audioprofile est une bibliothèque Go**, prévue pour être importée dans d'autres programmes Go (typiquement du streaming audio) : des briques de traitement audio composables (`io.Reader → io.Reader`), quelques préréglages, et l'outillage pour les régler et les déboguer. La commande `audioprofile` n'est pas le produit : c'est un outil de test posé sur la bibliothèque.

Conséquences à garder en tête à chaque changement :

- **L'API est le produit.** Un nom, une signature ou un message d'erreur est lu par des développeurs qui n'ont pas notre contexte. Pas d'état global, pas de goroutine cachée, pas d'effet de bord à l'import, pas d'écriture sur stdout/stderr dans un paquet (on renvoie des erreurs ou on prend un `Logf`).
- **Peu de dépendances.** La racine, `fx` et `panel` n'en ont aucune ; seule `config` tire `gopkg.in/yaml.v3`. N'en ajoute pas sans en parler. Les dépendances vont dans un seul sens : `cmd` → `live` → (`panel`, `config`, `fx`) → racine. `panel` ne sait pas ce que veut dire un réglage (c'est `live.Tuner`, un `panel.Backend`).
- **Tout est public** (`fx`, `config`, `panel`, `live`) : pas de `internal/` pour le code, parce que le projet qui importe la lib doit pouvoir monter le panneau, charger un fichier de réglages, etc. Seul `internal/docscheck`, qui ne contient que des tests de documentation, est privé.
- La carte des paquets est dans [README.md](README.md) et [docs/streaming.md](docs/streaming.md).

## Façon de travailler

- **Prévenir avant de changer un comportement.** Toute modification qui change ce qu'un utilisateur observe (sortie audio, valeur par défaut, option de la commande, message d'erreur, format de fichier, ordre des étages) est signalée explicitement dans la réponse, même si elle semble mineure, et avant de coder quand elle est importante. Quand une consigne contredit l'objectif du projet (exemple vécu : mettre `config` et `fx` dans `internal/` alors que la lib doit être importable), le dire et demander avant d'exécuter.
- **Demander une décision** : expose les options avec une recommandation, pas un exposé exhaustif. Quand le cas est complexe (plusieurs étapes, flux de données, avant/après), joins un **schéma ASCII** dans un bloc de code pour l'illustrer : un petit dessin du cas, de ce qui se passe aujourd'hui et de ce qui se passerait avec chaque option. Ce n'est pas obligatoire : pour un choix simple, une phrase suffit.
- **Compatibilité ascendante : pas un sujet pour l'instant.** Le projet est en développement : on renomme, supprime et restructure librement, sans alias dépréciés ni couches de compatibilité. Le mainteneur retirera cette consigne quand la situation changera. Ça ne dispense pas de prévenir (point précédent).
- **Avant de terminer : `gofmt -l .` (doit être vide) et `go vet ./...`**, puis `go test ./...`. Pour du code concurrent (`Switcher`, `live`, `panel`, `-watch`, `-serve`), ajoute `go test -race` sur les paquets concernés.
- **Commiter sans attendre qu'on le demande** dès que le code est prêt : formaté, `go vet` propre, tests verts, docs à jour. Pas de commit d'un travail en cours ou qui ne passe pas. Regarde `git status` avant : seulement ce qui appartient au changement (le binaire `audioprofile` à la racine est ignoré, ne l'ajoute pas). Ne pousse pas (`git push`) sans demande.
- **Message de commit** : une ligne de sujet courte à l'impératif (anglais, comme l'historique), puis un corps qui résume les changements de façon détaillée mais de haut niveau : ce qui change, pourquoi, ce qu'il faut savoir (comportement changé, limites). Pas de liste de fichiers ni de récit ligne à ligne.
- **Langue** : documentation, commentaires de code, messages d'erreur et sorties de la commande sont en français. Les messages de commit et les identifiants sont en anglais.
- **Fichiers temporaires hors du dépôt** (répertoire de travail de la session), jamais dans l'arbre.

## Conventions du code

- Une brique : `Nom(src io.Reader, sampleRate, channels int, réglages) io.Reader`, avec un type `XxxSettings` aux champs documentés (unité et bornes dans le commentaire). Les valeurs hors bornes sont ramenées dans leurs bornes ; `NaN` compte pour « sans effet » ; `sampleRate <= 0` rend le flux tel quel ; la longueur de sortie égale celle de l'entrée.
- Les validations strictes (qui renvoient une erreur) vivent dans `config` et `fx`, pas dans les briques.
- Les messages d'erreur d'un paquet ne citent pas les options de la commande (`-fx`, `-config`) : quand ils sont préfixés, c'est par le nom du paquet (`fx reverb : …`, `config : …`).
- Une valeur par défaut ou de départ existe **à un seul endroit**. Exemple vécu : les valeurs de départ du compresseur étaient écrites dans `config` et dans le schéma du panneau ; elles viennent maintenant de `config.StartCompressor()`, avec un test de cohérence.
- Les bornes des curseurs du panneau (`fx/bounds.go`) et les définitions d'effets (`fx/fx.go`) vont ensemble : un test vérifie qu'aucun paramètre n'a de plage manquante et que les extrémités passent la validation.

## Tests

Les tests s'appuient sur des signaux synthétiques et **mesurent** (gain à une fréquence, harmoniques, longueur, identité bit à bit) plutôt que de comparer à des valeurs figées. Pour chaque nouvelle brique, on retrouve les mêmes familles de cas : identité (réglage nul, `NaN`, fréquence nulle), bornes, silence exact, lecture fragmentée (octet par octet) identique à une lecture d'un bloc, longueur conservée, absence d'écrêtage, mono/stéréo/multicanal.

- **Pas de `time.Sleep` pour attendre un résultat** : attendre une condition avec une échéance (`waitFor`). Un test qui dépend du temps réel est fragile.
- **Unités explicites** : trames, échantillons (trames × canaux) et octets se confondent facilement. Nomme-les dans les variables et les commentaires des tests.
- Un test qui passe du premier coup n'est pas une preuve : vérifie avec `-v` qu'il s'exécute, et qu'il pourrait échouer (casse volontairement ce qu'il protège quand c'est peu coûteux).
- Le tableau de [docs/tests.md](docs/tests.md) décrit ce que chaque fichier de test vérifie : mets-le à jour avec les tests.

## Documentation

- [README.md](README.md) reste court (démarrage rapide, tableau des briques, renvois) ; le détail est dans `docs/`, une section `##` par brique dans [docs/briques.md](docs/briques.md).
- Un changement de comportement ou d'API met à jour la doc dans le même commit.
- **Le test `internal/docscheck` garde la doc honnête** (`go test ./internal/docscheck`, ~2 s) : liens et ancres, compilation de chaque bloc Go, signatures et types montrés identiques au code, blocs YAML valides. Il échoue si un exemple invente un champ ou si un lien casse : lance-le après tout changement de doc ou d'API, et corrige la doc plutôt que d'affaiblir le test. Un bloc volontairement incomplet se marque `<!-- doccheck:skip -->` juste avant son ouverture (à garder rare).
- Ne dis jamais d'un réglage ou d'un effet qu'il « sonne bien » : les réglages sont vérifiés **par mesure**, jamais à l'oreille (c'est écrit dans [docs/limites.md](docs/limites.md)). L'oreille est celle du mainteneur.

## Pièges déjà rencontrés

Des erreurs réelles, pour ne pas les refaire :

- **Un champ inventé dans un exemple** (`ReverbSettings.Room` au lieu de `RoomSize`) : lis la déclaration du type avant d'écrire un exemple ; `internal/docscheck` l'attrape maintenant.
- **Un exemple jamais chargé** : le fichier `audioprofile.example.yaml` annonçait qu'on pouvait ne changer que la 1re bande d'un multibande, alors que la configuration était refusée (une liste plus courte que le profil était invalide). Tout exemple doit être exécuté ou validé par un test. Quand doc et comportement divergent, ne choisis pas seul lequel corriger : si c'est le comportement, préviens (ici le mainteneur a décidé que les bandes non nommées gardent leur valeur de départ).
- **Une affirmation fausse dans la doc** (« seuls `config` et `cmd` ont une dépendance externe » : `live` aussi, via `config`) : toute affirmation vérifiable (dépendances, valeurs mesurées, comportements) se vérifie par une commande (`go list -deps`, un test), pas de mémoire.
- **Des scripts d'édition qui touchent le mauvais endroit** : une recherche de sous-chaîne (`"\tif *ab {"`) trouvait aussi une occurrence imbriquée et dupliquait du code. Préfère l'outil d'édition ; avec un script, vérifie que le motif est unique (`assert count == 1`) et relis le résultat (`git diff`).
- **Un test faux à cause d'une hypothèse sur le son** : un profil « de test » qui contenait aussi compresseur et saturation, alors que le test attendait un simple gain. Pour isoler un étage, construis un profil qui n'a que cet étage.
- **Un serveur de test qui s'arrête avant que le client arrive** quand l'entrée est lue plus vite que le temps réel : alimente-le en temps réel.
- **Un `pkill -f motif` qui tue sa propre commande** (le motif est dans la ligne de commande du shell) : tue par PID.
- **Ce que les tests ne couvrent pas, dis-le** : par exemple, la page du panneau est rejouée dans un DOM simulé (jsdom), mais son rendu visuel n'a pas été vu dans un navigateur.
