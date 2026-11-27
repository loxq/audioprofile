// Package audioprofile est une bibliothèque de briques de traitement audio composables pour Go, avec quelques
// préréglages bien sentis. Chaque brique prend un flux PCM et en renvoie un autre, au fil de l'eau, sans charger le
// fichier en mémoire.
//
// # Le principe
//
// Toutes les briques ont la même forme : elles prennent un [io.Reader] PCM, la fréquence d'échantillonnage et le
// nombre de canaux, et renvoient un [io.Reader]. Elles s'enchaînent donc comme des filtres :
//
//	r := audioprofile.HighPassFilter(src, 44100, 2, 80, 2)
//	r = audioprofile.Compressor(r, 44100, 2, audioprofile.CompressorSettings{ThresholdDB: -18, Ratio: 3, AttackMs: 10, ReleaseMs: 120})
//	r = audioprofile.Limiter(r, 44100, 2, audioprofile.LimiterSettings{CeilingDB: -1})
//
// Rien n'est calculé avant qu'on lise r. Un état de filtre est tenu par canal : mono, stéréo et multicanal
// fonctionnent. Quand un réglage est sans effet (intensité nulle, mix nul, fréquence d'échantillonnage nulle), le
// flux est renvoyé tel quel, et la longueur de sortie est celle de l'entrée.
//
// # Le format
//
// PCM 16 bits signé, little-endian, entrelacé (s16le), sans en-tête : ni WAV, ni autre conteneur. Une éventuelle
// trame incomplète en fin de flux est ignorée.
//
// # Les profils
//
// [ApplyProfile] applique un profil prédéfini (Rock, Pop, Jazz, Tube, les profils radio…) avec une intensité réglable.
// [Profile.Settings] donne ses réglages complets, qu'on modifie avant de les appliquer avec [ApplyProfileSettings].
// [TubeAmp] et ses préréglages ([AmpClean], [AmpCrunch], [AmpLead]) simulent un ampli à tubes.
//
// # Les briques
//
//   - égalisation : [Equalizer], [HighPassFilter], [LowPassFilter], [NotchFilter], [BassBoost], [MidBoost], [TrebleBoost]
//   - dynamique : [Compressor], [ParallelCompressor], [MultibandCompressor], [Limiter], [Gate], [DeEsser], [TransientShaper]
//   - couleur : [TubeAmp], [Exciter], [LoFi]
//   - modulation, écho, réverbération : [Chorus], [Flanger], [Delay], [Reverb]
//   - image stéréo : [StereoWidth], [Crossfeed]
//
// # Mesurer et comparer
//
// [MeasureLoudness] et [LoudnessMeter] mesurent la sonie (EBU R128). [NormalizeLoudness] amène un morceau à une sonie
// cible sous un plafond de crêtes. [MatchGains], [ApplyGain] et [Alternate] comparent l'original et le traité à sonie
// égale, sans que le plus fort l'emporte.
//
// [Meter] mesure un flux en continu, sans le modifier : crête et niveau efficace par canal, sonie momentanée et à court
// terme, spectre en 64 bandes. Un Meter avant la chaîne et un après montrent ce qu'elle fait : c'est la base d'un
// vumètre, d'un analyseur, ou de la surveillance d'une radio. [BandsResponse] et [ProfileSettings.EQResponse] donnent
// la courbe de l'égaliseur, à tracer sur ce spectre.
//
// # Régler en direct
//
// [Switcher] est un flux dont la chaîne se remplace en cours de lecture, sans clic : la nouvelle chaîne est mise en
// régime sur l'historique, puis un fondu enchaîné passe de l'ancienne à la nouvelle. C'est la base du réglage à
// l'oreille. Autour de lui, des paquets séparés fournissent l'outillage, à garder en développement :
//
//   - github.com/loxq/audioprofile/fx : les effets par nom, "delay:time=300" devient un effet construit et vérifié ;
//   - github.com/loxq/audioprofile/config : le fichier de réglages YAML ;
//   - github.com/loxq/audioprofile/live : la chaîne demandée, le rechargement à chaud du fichier, le panneau branché ;
//   - github.com/loxq/audioprofile/panel : le panneau web de curseurs.
//
// Ce paquet n'a aucune dépendance externe. La documentation détaillée de chaque brique est dans le dossier docs/ du
// dépôt, et les exemples de cette page sont des tests : ils sont exécutés avec le reste.
package audioprofile
