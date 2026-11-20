package audioprofile

// Profils « radio » : chaîne de traitement de diffusion, dans l'ordre
//
//	égalisation → AGC (compresseur lent) → compression multibande → saturation
//	→ élargissement stéréo → limiteur à −1 dBFS
//
// inspirée de la structure des processeurs de diffusion (nettoyage, niveau,
// mise en forme par bandes, limitation). Les réglages sont des points de départ
// choisis par mesure, pas une copie d'un produit existant.
//
// Contrairement aux autres profils, leur niveau n'est pas calé sur celui de l'entrée :
// il converge vers une valeur commune. Un morceau calme ou moyen sort plus fort, un
// master déjà très fort sort plus faible (mesuré : de −6 à +8 dB selon le morceau), et
// les crêtes ne dépassent jamais le plafond. La dynamique réelle est réduite
// (compression et limitation) ; si le son paraît plus vivant, c'est plutôt par le
// relâchement lent des bandes graves, la présence des médiums et des aigus et
// l'élargissement stéréo.

// radioStage regroupe les étages propres aux profils radio.
type radioStage struct {
	multi *MultibandSettings // compression multibande (nil = aucune)
	width float64            // largeur stéréo à intensité 1 (0 = pas d'élargissement)
	limit *LimiterSettings   // limiteur de crête, dernier étage (nil = aucun)
}

// radioCrossovers sépare les bandes sub-basses | médiums | présence | air.
var radioCrossovers = []float64{120, 1200, 6000}

// radioCeilingDB est le plafond des profils radio : 1 dB de marge pour les
// crêtes inter-échantillons.
const radioCeilingDB = -1.0

func band(thresholdDB, ratio, attackMs, releaseMs, makeupDB float64) CompressorSettings {
	return CompressorSettings{ThresholdDB: thresholdDB, Ratio: ratio, AttackMs: attackMs, ReleaseMs: releaseMs, KneeDB: 6, MakeupDB: makeupDB}
}

func radioMulti(low, mid, presence, air CompressorSettings) *MultibandSettings {
	return &MultibandSettings{
		Crossovers: append([]float64(nil), radioCrossovers...),
		Bands:      []CompressorSettings{low, mid, presence, air},
	}
}

func radioLimit(releaseMs float64) *LimiterSettings {
	return &LimiterSettings{CeilingDB: radioCeilingDB, LookaheadMs: 5, ReleaseMs: releaseMs}
}

var profileRadio = [numProfiles]radioStage{
	RadioHits: {
		multi: radioMulti(band(-22, 2.5, 30, 250, 2), band(-21, 2, 20, 180, 2.5), band(-23, 2.5, 10, 120, 3), band(-26, 2, 5, 80, 3.5)),
		width: 1.25,
		limit: radioLimit(80),
	},
	RadioRock: {
		multi: radioMulti(band(-22, 3, 15, 150, 2), band(-21, 2.5, 15, 150, 2.5), band(-23, 3, 8, 100, 3), band(-26, 2, 5, 80, 3)),
		width: 1.2,
		limit: radioLimit(80),
	},
	RadioUrban: {
		multi: radioMulti(band(-22, 3, 40, 200, 2.5), band(-21, 2, 20, 180, 2.5), band(-23, 2, 10, 120, 2.5), band(-26, 2, 5, 80, 3.5)),
		width: 1.15,
		limit: radioLimit(80),
	},
	RadioClub: {
		multi: radioMulti(band(-22, 3.5, 10, 120, 3), band(-21, 3, 10, 100, 3), band(-23, 3, 5, 80, 3.5), band(-26, 2.5, 3, 60, 4)),
		width: 1.4,
		limit: radioLimit(50),
	},
	RadioTalk: {
		multi: radioMulti(band(-22, 1.8, 30, 250, 1), band(-21, 3, 5, 150, 3), band(-23, 2.5, 5, 100, 2.5), band(-26, 2, 5, 80, 2.5)),
		limit: radioLimit(120),
	},
	RadioGold: {
		multi: radioMulti(band(-22, 1.8, 40, 300, 1.5), band(-21, 1.8, 25, 200, 1.5), band(-23, 1.8, 15, 150, 2), band(-26, 1.5, 10, 100, 2)),
		width: 1.15,
		limit: radioLimit(120),
	},
	RadioNatural: {
		multi: radioMulti(band(-20, 1.5, 50, 300, 1), band(-20, 1.5, 40, 250, 1), band(-22, 1.5, 30, 200, 1), band(-25, 1.5, 20, 150, 1)),
		limit: radioLimit(150),
	},
}

// active indique si le profil a au moins un étage propre aux profils radio.
func (r radioStage) active() bool { return r.multi != nil || r.width != 0 || r.limit != nil }
