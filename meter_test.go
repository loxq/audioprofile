package audioprofile

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// mtone renvoie des trames PCM s16le : une sinusoïde d'amplitude amp (sur 32768) sur chacun des canaux de amps.
func mtone(freq float64, amps []float64, fs, frames int) []byte {
	b := make([]byte, 0, frames*len(amps)*2)
	for i := 0; i < frames; i++ {
		for _, a := range amps {
			v := int16(math.Round(a * math.Sin(2*math.Pi*freq*float64(i)/float64(fs))))
			b = binary.LittleEndian.AppendUint16(b, uint16(v))
		}
	}
	return b
}

func mseconds(s float64) int { return int(s * 44100) }

func near(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Errorf("%s : %.3f, attendu %.3f ± %.3f", name, got, want, tol)
	}
}

func TestMeterLevels(t *testing.T) {
	m := NewMeter(44100, 2)
	m.Write(mtone(1000, []float64{16384, 4096}, 44100, mseconds(1)))
	s := m.Snapshot()
	if s.Frames != 44100 || len(s.PeakDBFS) != 2 || len(s.RMSDBFS) != 2 {
		t.Fatalf("%+v", s)
	}
	near(t, "crête gauche", s.PeakDBFS[0], -6.02, 0.02)   // 16384 / 32768
	near(t, "crête droite", s.PeakDBFS[1], -18.06, 0.02)  // 4096 / 32768
	near(t, "efficace gauche", s.RMSDBFS[0], -9.03, 0.05) // une sinusoïde : crête − 3,01 dB
	near(t, "efficace droite", s.RMSDBFS[1], -21.07, 0.05)
	if s.ClipCount != 0 {
		t.Errorf("écrêtages : %d", s.ClipCount)
	}
}

// Silence, ou rien mesuré : des valeurs finies (MeterFloorDB), jamais -Inf ni NaN, pour qu'un instantané s'écrive en JSON.
func TestMeterSilenceIsFinite(t *testing.T) {
	m := NewMeter(44100, 2)
	check := func(label string, s MeterSnapshot) {
		all := append(append(append([]float64{}, s.PeakDBFS...), s.RMSDBFS...), s.SpectrumDBFS...)
		all = append(all, s.MomentaryLUFS, s.ShortTermLUFS)
		for i, v := range all {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("%s : valeur %d non finie (%v)", label, i, v)
			}
			if v != MeterFloorDB {
				t.Fatalf("%s : valeur %d = %g, attendu le plancher %g", label, i, v, MeterFloorDB)
			}
		}
	}
	check("rien mesuré", m.Snapshot())
	m.Write(make([]byte, 4*mseconds(1))) // une seconde de silence
	s := m.Snapshot()
	check("silence", s)
	if !s.MomentaryValid || s.ShortTermValid {
		t.Errorf("après 1 s : momentanée valable %v (attendu oui), court terme %v (attendu non)", s.MomentaryValid, s.ShortTermValid)
	}
}

func TestMeterCountsClipping(t *testing.T) {
	m := NewMeter(44100, 1)
	var b []byte
	for _, v := range []int16{0, 32767, -32768, 100, 32767, 32766, -32767} {
		b = binary.LittleEndian.AppendUint16(b, uint16(v))
	}
	m.Write(b)
	if got := m.Snapshot().ClipCount; got != 3 {
		t.Errorf("écrêtages : %d, attendu 3 (32767, −32768, 32767)", got)
	}
}

// La sonie du Meter est celle de MeasureLoudness sur un signal stable, et les fenêtres ne sont valables qu'une fois pleines.
func TestMeterLoudness(t *testing.T) {
	pcm := mtone(1000, []float64{3277, 3277}, 44100, mseconds(10))
	m := NewMeter(44100, 2)
	m.Write(pcm)
	s := m.Snapshot()
	ref, err := MeasureLoudness(bytes.NewReader(pcm), 44100, 2)
	if err != nil || !ref.Valid {
		t.Fatalf("référence : %v %+v", err, ref)
	}
	if !s.MomentaryValid || !s.ShortTermValid {
		t.Fatalf("après 10 s : %+v", s)
	}
	near(t, "momentanée", s.MomentaryLUFS, ref.IntegratedLUFS, 0.1)
	near(t, "court terme", s.ShortTermLUFS, ref.IntegratedLUFS, 0.1)
	near(t, "étalonnage (−20 dBFS crête stéréo)", s.MomentaryLUFS, -20, 0.1)

	// validité des fenêtres
	m = NewMeter(44100, 2)
	step := mtone(1000, []float64{3277, 3277}, 44100, mseconds(0.3))
	m.Write(step)
	if s := m.Snapshot(); s.MomentaryValid || s.ShortTermValid {
		t.Errorf("après 0,3 s : %+v", s)
	}
	m.Write(mtone(1000, []float64{3277, 3277}, 44100, mseconds(0.3))) // 0,6 s
	if s := m.Snapshot(); !s.MomentaryValid || s.ShortTermValid {
		t.Errorf("après 0,6 s : %+v", s)
	}
	m.Write(mtone(1000, []float64{3277, 3277}, 44100, mseconds(2.5))) // 3,1 s
	if s := m.Snapshot(); !s.ShortTermValid {
		t.Errorf("après 3,1 s : %+v", s)
	}
}

// Les fenêtres : la crête oublie en 200 ms, le niveau efficace en 300 ms, la sonie à court terme en 3 s.
func TestMeterWindows(t *testing.T) {
	m := NewMeter(44100, 1)
	m.Write(mtone(1000, []float64{16384}, 44100, mseconds(4))) // 4 s de signal
	loud := m.Snapshot()
	m.Write(make([]byte, 2*mseconds(0.1))) // 100 ms de silence
	s := m.Snapshot()
	near(t, "crête 100 ms après", s.PeakDBFS[0], -6.02, 0.05) // encore dans la fenêtre de 200 ms
	m.Write(make([]byte, 2*mseconds(0.15)))
	if s = m.Snapshot(); s.PeakDBFS[0] != MeterFloorDB { // 250 ms de silence
		t.Errorf("crête 250 ms après : %g, attendu le plancher", s.PeakDBFS[0])
	}
	if s.RMSDBFS[0] == MeterFloorDB || s.RMSDBFS[0] > loud.RMSDBFS[0]-0.5 {
		t.Errorf("niveau efficace 250 ms après : %g (avant : %g) ; il doit baisser sans être nul", s.RMSDBFS[0], loud.RMSDBFS[0])
	}
	m.Write(make([]byte, 2*mseconds(0.1))) // 350 ms
	if s = m.Snapshot(); s.RMSDBFS[0] != MeterFloorDB {
		t.Errorf("niveau efficace 350 ms après : %g, attendu le plancher", s.RMSDBFS[0])
	}
	if s.MomentaryLUFS != MeterFloorDB { // 400 ms de silence presque atteints : 350 ms de silence, 50 ms de signal
		// la momentanée n'est pas encore tombée au plancher : elle garde 50 ms de signal
		if s.MomentaryLUFS > loud.MomentaryLUFS-3 {
			t.Errorf("momentanée 350 ms après : %g (avant : %g)", s.MomentaryLUFS, loud.MomentaryLUFS)
		}
	}
	if s.ShortTermLUFS < loud.ShortTermLUFS-1.5 || s.ShortTermLUFS == MeterFloorDB {
		t.Errorf("court terme 350 ms après : %g (avant : %g) ; il garde presque toute sa mémoire", s.ShortTermLUFS, loud.ShortTermLUFS)
	}
	m.Write(make([]byte, 2*mseconds(3.2)))
	if s = m.Snapshot(); s.ShortTermLUFS != MeterFloorDB {
		t.Errorf("court terme 3,5 s après : %g, attendu le plancher", s.ShortTermLUFS)
	}
}

// Les octets peuvent arriver par morceaux de n'importe quelle taille : mêmes mesures qu'en un seul bloc.
func TestMeterFragmentedWritesAreIdentical(t *testing.T) {
	pcm := mtone(440, []float64{9000, 5000}, 44100, mseconds(1))
	whole := NewMeter(44100, 2)
	whole.Write(pcm)
	for name, chunk := range map[string]int{"1 octet": 1, "3 octets": 3, "4097 octets": 4097} {
		m := NewMeter(44100, 2)
		for i := 0; i < len(pcm); i += chunk {
			m.Write(pcm[i:min(i+chunk, len(pcm))])
		}
		a, b := whole.Snapshot(), m.Snapshot()
		if a.Frames != b.Frames || a.ClipCount != b.ClipCount || !floatsEqual(a.PeakDBFS, b.PeakDBFS) || !floatsEqual(a.RMSDBFS, b.RMSDBFS) ||
			a.MomentaryLUFS != b.MomentaryLUFS || a.ShortTermLUFS != b.ShortTermLUFS || !floatsEqual(a.SpectrumDBFS, b.SpectrumDBFS) {
			t.Errorf("%s : l'instantané diffère de celui d'un bloc", name)
		}
	}
}

func floatsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMeterSpectrum(t *testing.T) {
	m := NewMeter(44100, 2)
	freqs := m.SpectrumFreqs()
	if len(freqs) != 64 || freqs[0] < 20 || freqs[63] > 20000 {
		t.Fatalf("%d bandes de %g à %g Hz", len(freqs), freqs[0], freqs[len(freqs)-1])
	}
	for i := 1; i < len(freqs); i++ {
		if !(freqs[i] > freqs[i-1]) {
			t.Fatalf("fréquences non croissantes en %d : %v", i, freqs[i-1:i+1])
		}
	}
	if r0, r1 := freqs[1]/freqs[0], freqs[63]/freqs[62]; math.Abs(r0-r1) > 1e-9 {
		t.Errorf("échelle non logarithmique : %g puis %g", r0, r1)
	}

	// un sinus de 1 kHz d'amplitude 0,5 : −6 dBFS dans sa bande, rien ailleurs
	m.Write(mtone(1000, []float64{16384, 16384}, 44100, 8192))
	s := m.Snapshot()
	best := 0
	for i, v := range s.SpectrumDBFS {
		if v > s.SpectrumDBFS[best] {
			best = i
		}
	}
	if f := freqs[best]; f < 900 || f > 1110 {
		t.Errorf("le sinus de 1 kHz tombe dans la bande de %.0f Hz", f)
	}
	near(t, "niveau du sinus", s.SpectrumDBFS[best], -6.02, 1.5) // 1,4 dB de perte au pire entre deux cases
	for i, v := range s.SpectrumDBFS {
		if f := freqs[i]; (f < 250 || f > 4000) && v > -70 {
			t.Errorf("bande de %.0f Hz : %.1f dBFS, trop près de rien du tout", f, v)
		}
	}

	// deux sinus : deux pics, au bon endroit et au bon écart (200 Hz à 0,25, 5 kHz à 0.5 : 6 dB d'écart)
	m = NewMeter(44100, 1)
	two := mtone(200, []float64{8192}, 44100, 8192)
	hi := mtone(5000, []float64{16384}, 44100, 8192)
	mix := make([]byte, len(two))
	for i := 0; i < len(two); i += 2 {
		a := int16(binary.LittleEndian.Uint16(two[i:]))
		b := int16(binary.LittleEndian.Uint16(hi[i:]))
		binary.LittleEndian.PutUint16(mix[i:], uint16(a+b))
	}
	m.Write(mix)
	s = m.Snapshot()
	peakNear := func(f float64) float64 {
		best := math.Inf(-1)
		for i, v := range s.SpectrumDBFS {
			if freqs[i] > f*0.9 && freqs[i] < f*1.1 && v > best {
				best = v
			}
		}
		return best
	}
	low, high := peakNear(200), peakNear(5000)
	near(t, "écart entre les deux sinus", high-low, 6.02, 2)
	near(t, "200 Hz", low, -12.04, 1.5)
}

// À basse fréquence d'échantillonnage, les bandes restent sous Nyquist.
func TestMeterSpectrumLowSampleRate(t *testing.T) {
	m := NewMeter(8000, 1)
	f := m.SpectrumFreqs()
	if len(f) != 64 || f[len(f)-1] >= 4000 {
		t.Errorf("%d bandes, la dernière à %g Hz (Nyquist : 4000)", len(f), f[len(f)-1])
	}
	m.Write(mtone(1000, []float64{16384}, 8000, 8192))
	s := m.Snapshot()
	maxV := math.Inf(-1)
	for _, v := range s.SpectrumDBFS {
		maxV = math.Max(maxV, v)
	}
	near(t, "sinus de 1 kHz à 8 kHz", maxV, -6.02, 1.5)
}

func TestMeterWrapAndWrite(t *testing.T) {
	pcm := mtone(500, []float64{8000, 8000}, 44100, 5000)
	m := NewMeter(44100, 2)
	got, err := io.ReadAll(iotest.HalfReader(m.Wrap(bytes.NewReader(pcm))))
	if err != nil || !bytes.Equal(got, pcm) {
		t.Fatalf("Wrap doit rendre le flux à l'identique (err %v)", err)
	}
	if m.Snapshot().Frames != 5000 {
		t.Errorf("trames mesurées : %d", m.Snapshot().Frames)
	}
	if n, err := m.Write([]byte{1, 2, 3}); n != 3 || err != nil {
		t.Errorf("Write : %d %v", n, err)
	}
	// l'erreur de la source passe telle quelle
	boom := io.ErrClosedPipe
	r := NewMeter(44100, 1).Wrap(io.MultiReader(bytes.NewReader(make([]byte, 100)), iotest.ErrReader(boom)))
	if _, err := io.ReadAll(r); err != boom {
		t.Errorf("erreur de la source : %v", err)
	}
	// io.TeeReader : un Meter est un io.Writer
	var _ io.Writer = m
	m2 := NewMeter(44100, 2)
	io.Copy(io.Discard, io.TeeReader(bytes.NewReader(pcm), m2))
	if m2.Snapshot().Frames != 5000 {
		t.Errorf("TeeReader : %d trames", m2.Snapshot().Frames)
	}
}

func TestMeterInvalidParameters(t *testing.T) {
	for name, m := range map[string]*Meter{"fréquence nulle": NewMeter(0, 2), "sans canal": NewMeter(44100, 0), "négatifs": NewMeter(-1, -1)} {
		if n, err := m.Write(make([]byte, 100)); n != 100 || err != nil {
			t.Errorf("%s : Write %d %v", name, n, err)
		}
		s := m.Snapshot()
		if s.Frames != 0 || s.MomentaryLUFS != MeterFloorDB || s.PeakDBFS == nil || s.SpectrumDBFS == nil {
			t.Errorf("%s : %+v", name, s)
		}
		if m.SpectrumFreqs() == nil {
			t.Errorf("%s : SpectrumFreqs ne doit pas être nil", name)
		}
	}
}

// Le même instantané deux fois de suite est identique, et modifier ce qu'on reçoit n'abîme pas le suivant.
func TestMeterSnapshotIsStable(t *testing.T) {
	m := NewMeter(44100, 1)
	m.Write(mtone(1000, []float64{16384}, 44100, 8192))
	a := m.Snapshot()
	for i := range a.SpectrumDBFS {
		a.SpectrumDBFS[i] = 999
	}
	b, c := m.Snapshot(), m.Snapshot()
	if !floatsEqual(b.SpectrumDBFS, c.SpectrumDBFS) || b.SpectrumDBFS[0] == 999 {
		t.Error("l'instantané doit être indépendant de ceux qu'on a déjà rendus")
	}
}

// À lancer avec -race : écrire et lire en même temps, depuis plusieurs goroutines.
func TestMeterConcurrentUse(t *testing.T) {
	m := NewMeter(44100, 2)
	pcm := mtone(1000, []float64{12000, 12000}, 44100, 2000)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := m.Snapshot()
					if len(s.SpectrumDBFS) != 64 {
						t.Error("spectre incomplet")
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		m.Write(pcm)
	}
	close(stop)
	wg.Wait()
	if m.Snapshot().Frames != 200000 {
		t.Errorf("%d trames", m.Snapshot().Frames)
	}
}

// ---------------------------------------------------------------- réponse de l'égaliseur

// gainThrough mesure le gain réel (dB) d'une chaîne à une fréquence, avec un sinus de 1 s.
func gainThrough(t *testing.T, freq float64, process func(io.Reader) io.Reader) float64 {
	t.Helper()
	in := mtone(freq, []float64{8000}, 44100, 44100)
	out, err := io.ReadAll(process(bytes.NewReader(in)))
	if err != nil {
		t.Fatal(err)
	}
	rms := func(b []byte) float64 {
		var s float64
		n := len(b) / 2
		for i := n / 2; i < n; i++ {
			v := float64(int16(binary.LittleEndian.Uint16(b[2*i:])))
			s += v * v
		}
		return 10 * math.Log10(s/float64(n-n/2))
	}
	return rms(out) - rms(in)
}

func TestBandsResponse(t *testing.T) {
	bands := []Band{{Type: Peaking, FreqHz: 1000, GainDB: 6, Q: 1}}
	r := BandsResponse(44100, bands, []float64{1000, 100, 10000, 0, -5, 30000})
	near(t, "gain au centre", r[0], 6, 0.001)
	if math.Abs(r[1]) > 1 || math.Abs(r[2]) > 1 {
		t.Errorf("loin du centre : %.2f et %.2f dB", r[1], r[2])
	}
	if r[3] != 0 || r[4] != 0 || r[5] != 0 {
		t.Errorf("fréquences invalides ou au-delà de Nyquist : %v", r[3:])
	}
	if z := BandsResponse(44100, nil, []float64{100, 1000}); z[0] != 0 || z[1] != 0 {
		t.Errorf("sans bande : %v", z)
	}
	if z := BandsResponse(0, bands, []float64{1000}); z[0] != 0 {
		t.Errorf("fréquence d'échantillonnage nulle : %v", z)
	}
	hp := BandsResponse(44100, []Band{{Type: HighPass, FreqHz: 1000, Order: 2}}, []float64{1000, 250, 8000})
	near(t, "passe-haut à la coupure", hp[0], -3.01, 0.05)
	near(t, "passe-haut deux octaves plus bas", hp[1], -24.1, 0.3)
	near(t, "passe-haut dans la bande passante", hp[2], 0, 0.1)
	near(t, "gain global", BandsResponse(44100, []Band{{Type: Gain, GainDB: -7}}, []float64{50})[0], -7, 1e-9)
}

// La courbe annoncée est celle de ce que fait vraiment Equalizer, pour plusieurs types de bande et de fréquences.
func TestBandsResponseMatchesEqualizer(t *testing.T) {
	bands := []Band{
		{Type: LowShelf, FreqHz: 120, GainDB: 5},
		{Type: Peaking, FreqHz: 800, GainDB: -4, Q: 1.4},
		{Type: HighShelf, FreqHz: 6000, GainDB: 3},
		{Type: Notch, FreqHz: 3000, Q: 4},
		{Type: LowPass, FreqHz: 12000, Order: 2},
	}
	freqs := []float64{60, 200, 800, 1500, 2950, 5000, 9000, 14000}
	want := BandsResponse(44100, bands, freqs)
	for i, f := range freqs {
		got := gainThrough(t, f, func(r io.Reader) io.Reader { return Equalizer(r, 44100, 1, bands) })
		if math.Abs(got-want[i]) > 0.3 {
			t.Errorf("%.0f Hz : mesuré %.2f dB, annoncé %.2f dB", f, got, want[i])
		}
	}
}

// EQResponse suit ApplyProfileSettings : même intensité, même gain automatique.
func TestEQResponseMatchesApplyProfileSettings(t *testing.T) {
	freqs := []float64{50, 100, 400, 1000, 3000, 8000, 12000}
	for _, tc := range []struct {
		p         Profile
		intensity float64
	}{{Rock, 1}, {Jazz, 0.5}, {Electronic, 2}, {Vocal, 1}} {
		s := tc.p.Settings()
		// seulement l'égalisation : le reste dépend du signal
		eqOnly := ProfileSettings{Bands: s.Bands}
		want := eqOnly.EQResponse(44100, tc.intensity, freqs)
		peak := math.Inf(-1)
		for _, v := range want {
			peak = math.Max(peak, v)
		}
		for i, f := range freqs {
			got := gainThrough(t, f, func(r io.Reader) io.Reader { return ApplyProfileSettings(r, 44100, 1, eqOnly, tc.intensity) })
			if math.Abs(got-want[i]) > 0.3 {
				t.Errorf("%s ×%g à %.0f Hz : mesuré %.2f dB, annoncé %.2f dB", tc.p, tc.intensity, f, got, want[i])
			}
		}
		if peak > 0.05 {
			t.Errorf("%s ×%g : le gain automatique doit ramener le pic à 0 dB, il est à %.2f", tc.p, tc.intensity, peak)
		}
	}
}

func TestEQResponseEdgeCases(t *testing.T) {
	freqs := []float64{100, 1000, 10000}
	rock := Rock.Settings()
	for name, v := range map[string][]float64{
		"intensité nulle":        rock.EQResponse(44100, 0, freqs),
		"intensité négative":     rock.EQResponse(44100, -1, freqs),
		"intensité NaN":          rock.EQResponse(44100, math.NaN(), freqs),
		"fréquence d'éch. nulle": rock.EQResponse(0, 1, freqs),
		"Flat":                   Flat.Settings().EQResponse(44100, 1, freqs),
		"sans fréquence":         {},
	} {
		for _, x := range v {
			if x != 0 {
				t.Errorf("%s : %v, attendu des zéros", name, v)
			}
		}
	}
	// un profil avec limiteur n'a pas de gain automatique : sa courbe peut dépasser 0 dB
	if r := RadioHits.Settings().EQResponse(44100, 1, []float64{12000}); r[0] < 1 {
		t.Errorf("radio-hits à 12 kHz : %.2f dB, attendu un renfort (pas de gain automatique)", r[0])
	}
	// au-delà de MaxIntensity : ramenée, comme ApplyProfileSettings
	a, b := rock.EQResponse(44100, MaxIntensity, freqs), rock.EQResponse(44100, 10, freqs)
	if !floatsEqual(a, b) {
		t.Errorf("l'intensité doit être ramenée à %g : %v et %v", MaxIntensity, a, b)
	}
	if got := strings.TrimSpace(""); got != "" {
		t.Fatal()
	}
}

// Coût d'un Meter : une seconde de stéréo à 44,1 kHz mesurée, et un instantané (qui refait la transformée de Fourier).
func BenchmarkMeterWrite(b *testing.B) {
	pcm := mtone(1000, []float64{12000, 12000}, 44100, 44100)
	m := NewMeter(44100, 2)
	b.SetBytes(int64(len(pcm)))
	for i := 0; i < b.N; i++ {
		m.Write(pcm)
	}
}

func BenchmarkMeterSnapshot(b *testing.B) {
	m := NewMeter(44100, 2)
	pcm := mtone(1000, []float64{12000, 12000}, 44100, 4096)
	for i := 0; i < b.N; i++ {
		m.Write(pcm[:4]) // une trame de plus : le cache de spectre ne sert pas
		m.Snapshot()
	}
}
