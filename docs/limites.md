# Limites connues

[← Retour au README](../README.md)


- Pas de gestion des en-têtes WAV ni d'autres formats que s16le.
- Les préréglages `BassBoost`, `MidBoost` et `TrebleBoost` ont leurs fréquences et leur gain maximal fixés dans le code (constantes `cutoffHz`, `midCenterHz`, `midQ`, `trebleCutoffHz` et `maxGainDB`) ; utiliser `Equalizer` pour les régler.
- `-ab`, `-target-lufs` et `NormalizeLoudness` travaillent en mémoire : environ 60 Mo par minute de stéréo à 44,1 kHz (mesuré : 232 à 247 Mo pour un morceau de près de 4 minutes). Seuls les profils, `-loudness` et `MeasureLoudness` restent au fil de l'eau.
- La mesure de sonie pèse tous les canaux de la même façon (exact en mono et en stéréo, approché pour du 5.1), et le plafond des crêtes porte sur les valeurs d'échantillons, pas sur les crêtes inter-échantillons (*true peak*).
- Les paramètres des profils (dont `radio-*`) n'ont pas été recalibrés sur la sonie : leurs réglages restent ceux d'une calibration sur le niveau d'un sinus et sur le niveau efficace. La sonie « avant / après » se mesure avec `-loudness`, et `-target-lufs` ou `-ab` permettent de comparer à sonie égale.
- Les profils `radio-*` n'ont pas été validés à l'oreille non plus. Leurs réglages sont des points de départ, calés par mesure sur trois morceaux : niveau, crête, plage dynamique et côté/milieu.
- Le de-esser a un seuil absolu et n'a pas été essayé sur de la voix ; sur de la musique, il atténue aussi les cymbales. L'exciter n'est franc que sur des aigus assez forts.
- La queue de la réverbération est coupée à la fin du flux (même longueur que l'entrée) ; l'algorithme (Freeverb) est reconnu pour sa coloration un peu métallique sur les sons très tenus, et les longueurs des filtres sont fixes (pas de réglage fin du caractère de la pièce).
- Le renforçateur de transitoires n'a pas d'anticipation (les tout premiers millièmes de seconde d'un son ne sont pas atteints) et son plafond doux limite l'effet sur les masters dont les crêtes sont déjà à 0 dBFS.
- Les filtres de séparation du compresseur multibande sont à phase minimale, pas à phase linéaire. Le limiteur borne les crêtes d'échantillons (pas les crêtes inter-échantillons) et n'apporte pas de saturation ou d'écrêtage doux en plus.
- Au-delà de l'intensité 1, le volume baisse et le niveau n'est plus calibré sur l'entrée ; un signal à pleine échelle (0 dBFS) peut écrêter plus largement qu'à l'intensité 1 (Pop, Vocal).
- Les réglages des profils n'ont pas été validés à l'oreille : ils sont vérifiés par mesure (forme des courbes, niveaux) mais restent des points de départ à affiner.
- Le compresseur n'a pas d'anticipation : un pic très bref passe pendant le temps d'attaque. Seul le [`Limiter`](briques.md#limiteur) garantit un plafond. Un signal à pleine échelle (0 dBFS) dès son premier échantillon peut donc écrêter brièvement avec un profil sans limiteur (Pop, Vocal) ; à −3 dBFS, aucun profil n'écrête. En dehors des profils, un fort gain d'égalisation sur un signal déjà proche de la saturation est écrêté (distorsion).
- Les profils et préréglages avec saturation sont calibrés à −18 dBFS : sur un master très fort, la saturation compresse les passages forts et le volume baisse (jusqu'à −6 dB mesuré sur un morceau à RMS −9,7 dBFS).
- Le suréchantillonnage de la saturation réduit fortement le repliement de spectre mais ne l'élimine pas (−25 dB mesuré sur un cas poussé), et ses filtres arrondissent un peu les aigus extrêmes des profils saturés.
- Le dosage de la saturation dépend de la fréquence : le gain automatique compense le drive là où l'égalisation est inférieure à son pic, donc le grain s'entend surtout dans les basses.
