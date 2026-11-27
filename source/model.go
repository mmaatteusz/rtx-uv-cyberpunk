package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Point struct {
	Slot, UV, LiveKHz, DeltaKHz int
}

type Identity struct {
	Name, UUID, VBIOS          string
	Device, Board, Driver, Bus uint32
}

type Snapshot struct {
	GPU                                             Identity
	Points                                          []Point
	RawMask, RawCurve, RawTable, RawPstates         []byte
	MemoryDeltas, GraphicsDeltas                    map[uint32]int
	MemoryLimits                                    map[uint32][2]int
	FactoryMemoryKHz                                int
	PowerMW, FactoryPowerMW, MinPowerMW, MaxPowerMW int
	ThermalPolicies                                 []uint32
	MinDeltaKHz, MaxDeltaKHz                        int
	FanPercent, VoltageBoostPercent                 *float64
	Time                                            time.Time
}

type Sample struct {
	Time                                                            time.Time
	CoreMHz, MemoryMHz, Millivolts, Watts, TempC, HotSpotC, LoadPct *float64
	PerfCap                                                         string
}

type Result struct {
	At                                                                                           time.Time
	Stage, Error, Path                                                                           string
	UV, TargetMHz, FrameCount                                                                    int
	FPS, Low1, Low01, FrametimeSD, FrametimeP99, GameFPS, Seconds                                float64
	CoreMedian, CoreP10, CoreSpread, VoltageMedian, WattsMedian, TempMax, HotSpotMax, LoadMedian float64
	VRAMMHz, StartTempC, StartCPUPct, StartGPUPct, VoltageMax                                    float64
	PerfCap                                                                                      map[string]int
	Accepted                                                                                     bool
}

type Rank struct {
	UV, Count                                                            int
	FPS, Low1, Low01, CoreMHz, ClockSpread, Volts, Watts, MaxTemp, Score float64
	Accepted                                                             bool
	Phase                                                                string
}

type Session struct {
	Started, Updated                   time.Time
	Mode, Game, Status, SettingsSHA256 string
	Healthy                            bool
	Winner, MSISlot                    int
	OCCeilingMV                        float64
	Runs                               []Result
	Ranking                            []Rank
}

func encodeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".zapis-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(payload)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return atomicReplace(file.Name(), path)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func median(values []float64) float64 { return quantile(values, .5) }

func quantile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	ix := float64(len(sorted)-1) * p
	left, right := int(math.Floor(ix)), int(math.Ceil(ix))
	return sorted[left] + (sorted[right]-sorted[left])*(ix-float64(left))
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, x := range values {
		total += x
	}
	return total / float64(len(values))
}

type reportedFrames struct {
	FPS   float64 `json:"averageFps"`
	Count int     `json:"frameNumber"`
	Time  float64 `json:"time"`
}

func normalize(label string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, label)
}

type frameColumn struct {
	index int
	mode  string
	scale float64
}

func computeFrames(source io.Reader, summary reportedFrames, result *Result) ([]float64, error) {
	// REDengine exports individual frames. Match durations (or elapsed timestamps)
	// against the separately reported count and FPS before using a column.
	data, err := io.ReadAll(io.LimitReader(source, (20<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 20<<20 {
		return nil, errors.New("plik klatek przekracza limit rozmiaru")
	}
	if !(summary.Time > 0) || math.IsNaN(summary.FPS) || math.IsInf(summary.FPS, 0) {
		return nil, errors.New("brak poprawnego czasu lub FPS w podsumowaniu gry")
	}
	var rows [][]string
	for _, delimiter := range []rune{',', ';', '\t'} {
		parser := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\uFEFF")))
		parser.Comma, parser.TrimLeadingSpace, parser.FieldsPerRecord = delimiter, true, -1
		rows, err = parser.ReadAll()
		if err == nil && len(rows) > 500 && (len(rows[0]) > 1 || !strings.ContainsAny(rows[0][0], ",;\t")) {
			break
		}
	}
	if err != nil || len(rows) < 501 || len(rows[0]) == 0 {
		return nil, errors.New("brak kompletnego pliku klatek gry")
	}
	if summary.FPS < 8 || summary.FPS > 1500 || summary.Count < 500 || math.Abs(float64(len(rows)-1-summary.Count)) > math.Max(3, float64(summary.Count)*.015) {
		return nil, fmt.Errorf("niespójne podsumowanie gry (%.2f FPS, %d klatek, %d wierszy)", summary.FPS, summary.Count, len(rows)-1)
	}
	var columns []frameColumn
	for ix, header := range rows[0] {
		key := normalize(header)
		mode := ""
		switch {
		case strings.Contains(key, "frametime") || strings.Contains(key, "frameduration") || strings.Contains(key, "delta") || strings.Contains(key, "msbetweenpresents"):
			mode = "duration"
		case strings.Contains(key, "fps") || strings.Contains(key, "framespersecond"):
			mode = "fps"
		case strings.Contains(key, "time") || strings.Contains(key, "seconds"):
			mode = "time"
		}
		if mode == "" {
			continue
		}
		scales := []float64{1, 1000, .001}
		switch {
		case strings.HasSuffix(key, "ms") || strings.Contains(key, "milliseconds"):
			scales = []float64{1}
		case strings.HasSuffix(key, "us") || strings.Contains(key, "microseconds"):
			scales = []float64{.001}
		case mode == "fps":
			scales = []float64{1}
		}
		for _, scale := range scales {
			columns = append(columns, frameColumn{ix, mode, scale})
		}
	}
	for _, col := range columns {
		frames := make([]float64, 0, len(rows)-1)
		var previous float64
		valid := true
		for i, row := range rows[1:] {
			if col.index >= len(row) {
				valid = false
				break
			}
			value, parseErr := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(row[col.index]), ",", "."), 64)
			if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				valid = false
				break
			}
			ms := value * col.scale
			switch col.mode {
			case "fps":
				if value > 0 {
					ms = 1000 / value
				}
			case "time":
				ms, previous = (value-previous)*col.scale, value
				if i == 0 {
					continue
				}
			}
			if i == 0 && ms == 0 {
				continue
			}
			if ms <= 0 || ms > 15000 {
				valid = false
				break
			}
			frames = append(frames, ms)
		}
		if !valid || len(frames) < 500 {
			continue
		}
		average := mean(frames)
		fps, seconds := 1000/average, average*float64(len(frames))/1000
		if seconds < 30 || seconds > 130 || math.Abs(fps/summary.FPS-1) > .025 {
			continue
		}
		if summary.Time > 0 {
			matchesTime := false
			for _, scale := range []float64{1, .001, .000001} {
				t := summary.Time * scale
				if t >= 30 && t <= 130 && math.Abs(seconds-t) <= math.Max(2.5, t*.04) {
					matchesTime = true
					break
				}
			}
			if !matchesTime {
				continue
			}
		}
		sorted := slices.Clone(frames)
		slices.Sort(sorted)
		tail := func(p float64) float64 {
			count := int(math.Ceil(float64(len(sorted)) * p))
			return 1000 / mean(sorted[len(sorted)-count:])
		}
		var variance float64
		for _, ms := range frames {
			variance += (ms - average) * (ms - average)
		}
		result.FPS, result.Low1, result.Low01 = fps, tail(.01), tail(.001)
		result.Seconds, result.FrameCount, result.GameFPS = seconds, len(frames), summary.FPS
		result.FrametimeSD = math.Sqrt(variance / float64(len(frames)))
		result.FrametimeP99 = quantile(frames, .99)
		return frames, nil
	}
	return nil, fmt.Errorf("niezgodny format klatek lub czas testu (kolumny: %s)", strings.Join(rows[0], ", "))
}

type Band struct{ Center, Slack float64 }

func noiseBand(results []Result, getter func(Result) float64, maxRelative float64) (Band, bool) {
	values := make([]float64, 0, len(results))
	for _, r := range results {
		values = append(values, getter(r))
	}
	center := median(values)
	if len(values) < 3 || center <= 0 {
		return Band{}, false
	}
	var maxDiff float64
	for _, x := range values {
		maxDiff = math.Max(maxDiff, math.Abs(x-center))
	}
	return Band{center, maxDiff}, maxDiff <= maxRelative*center
}

type Criteria struct{ FPS, Low1, Low01, P99, Core, Spread, Power, Temp Band }

func establish(results []Result) (Criteria, error) {
	var c Criteria
	for _, metric := range []struct {
		band *Band
		get  func(Result) float64
		max  float64
	}{
		{&c.FPS, func(r Result) float64 { return r.FPS }, .03},
		{&c.Low1, func(r Result) float64 { return r.Low1 }, .09},
		{&c.Low01, func(r Result) float64 { return r.Low01 }, .25},
		{&c.P99, func(r Result) float64 { return r.FrametimeP99 }, .10},
		{&c.Core, func(r Result) float64 { return r.CoreMedian }, .04},
	} {
		band, ok := noiseBand(results, metric.get, metric.max)
		if !ok {
			return c, errors.New("bazowe przebiegi są zbyt rozbieżne, aby wiarygodnie ocenić regresję")
		}
		*metric.band = band
	}
	c.Power, _ = noiseBand(results, func(r Result) float64 { return r.WattsMedian }, 10)
	c.Temp, _ = noiseBand(results, func(r Result) float64 { return r.TempMax }, 10)
	c.Spread, _ = noiseBand(results, func(r Result) float64 { return r.CoreSpread }, 10)
	return c, nil
}

func (c Criteria) accepts(r Result) bool {
	return r.Error == "" &&
		r.FPS >= c.FPS.Center-c.FPS.Slack &&
		r.Low1 >= c.Low1.Center-c.Low1.Slack &&
		r.Low01 >= c.Low01.Center-c.Low01.Slack &&
		r.FrametimeP99 <= c.P99.Center+c.P99.Slack &&
		r.CoreMedian >= c.Core.Center-min(30, max(15, c.Core.Slack))
}

func verifiedWinnerTarget(s Session) (Result, error) {
	if !s.Healthy || s.Mode != "Optymalizacja" || s.Winner <= 0 || len(s.Runs) < 10 {
		return Result{}, errors.New("brak zakończonej optymalizacji")
	}
	last := s.Runs[len(s.Runs)-1]
	if last.Stage != "Kontrola po przywróceniu" || last.UV != 0 || !last.Accepted || last.Error != "" {
		return Result{}, errors.New("brak końcowej kontroli oryginalnego OC")
	}
	var baseline []Result
	repeats, seconds, target := 0, 0.0, 0
	ceiling := 0.0
	for _, r := range s.Runs {
		if r.UV == 0 && r.Stage == "Punkt odniesienia" && r.Accepted && r.Error == "" {
			baseline = append(baseline, r)
			ceiling = max(ceiling, r.VoltageMax)
		}
		if r.UV != s.Winner || (r.Stage != "Powtórka finalisty" && r.Stage != "Wydłużona próba") {
			continue
		}
		if !r.Accepted || r.Error != "" || !(r.Seconds >= 30 && r.Seconds <= 130) || r.TargetMHz <= 0 || (target != 0 && target != r.TargetMHz) {
			return Result{}, errors.New("niespójne wyniki zwycięskiego profilu")
		}
		target = r.TargetMHz
		if r.Stage == "Powtórka finalisty" {
			repeats++
		} else {
			seconds += r.Seconds
		}
	}
	if len(baseline) != 3 || repeats != 5 || seconds < 3600 || s.OCCeilingMV != ceiling {
		return Result{}, errors.New("brak potwierdzenia bazy OC, pięciu powtórzeń lub pełnej godziny sceny")
	}
	return Result{CoreMedian: median([]float64{baseline[0].CoreMedian, baseline[1].CoreMedian, baseline[2].CoreMedian}), VoltageMedian: median([]float64{baseline[0].VoltageMedian, baseline[1].VoltageMedian, baseline[2].VoltageMedian}), TargetMHz: target}, nil
}

func measureFinalist(run func() (Result, error)) ([]Result, error) {
	var results []Result
	for len(results) < 5 {
		r, err := run()
		if err != nil {
			return results, err
		}
		results = append(results, r)
		if !r.Accepted {
			break
		}
	}
	return results, nil
}

func allTrialRunsAccepted(results []Result) bool {
	return len(results) == 5 && !slices.ContainsFunc(results, func(result Result) bool { return !result.Accepted })
}

func combine(results []Result, stage string, c Criteria) (Rank, Result) {
	med := func(get func(Result) float64) float64 {
		values := make([]float64, 0, len(results))
		for _, r := range results {
			values = append(values, get(r))
		}
		return median(values)
	}
	r := Result{
		UV: results[0].UV, FPS: med(func(r Result) float64 { return r.FPS }), Low1: med(func(r Result) float64 { return r.Low1 }),
		Low01: med(func(r Result) float64 { return r.Low01 }), FrametimeP99: med(func(r Result) float64 { return r.FrametimeP99 }),
		CoreMedian: med(func(r Result) float64 { return r.CoreMedian }), CoreSpread: med(func(r Result) float64 { return r.CoreSpread }),
		VoltageMedian: med(func(r Result) float64 { return r.VoltageMedian }),
		WattsMedian:   med(func(r Result) float64 { return r.WattsMedian }),
		TempMax:       med(func(r Result) float64 { return r.TempMax }),
	}
	valid := c.accepts(r)
	for _, run := range results {
		valid = valid && run.Error == "" && run.FPS >= c.FPS.Center-2*c.FPS.Slack
	}
	return Rank{UV: r.UV, Count: len(results), FPS: r.FPS, Low1: r.Low1, Low01: r.Low01, CoreMHz: r.CoreMedian,
		ClockSpread: r.CoreSpread, Volts: r.VoltageMedian, Watts: r.WattsMedian, MaxTemp: r.TempMax, Score: r.FPS / c.FPS.Center, Accepted: valid, Phase: stage}, r
}

func preferred(a, b Rank, c Criteria) bool {
	if math.Abs(a.FPS-b.FPS) > max(.1, c.FPS.Slack) {
		return a.FPS > b.FPS
	}
	if math.Abs(a.Low1-b.Low1) > max(.1, c.Low1.Slack) {
		return a.Low1 > b.Low1
	}
	if math.Abs(a.ClockSpread-b.ClockSpread) > max(15, c.Spread.Slack) {
		return a.ClockSpread < b.ClockSpread
	}
	if math.Abs(a.Watts-b.Watts) > max(3, c.Power.Slack) {
		return a.Watts < b.Watts
	}
	if math.Abs(a.MaxTemp-b.MaxTemp) > max(2, c.Temp.Slack) {
		return a.MaxTemp < b.MaxTemp
	}
	return false
}

func nearestBelow(points []Point, high, low, targetUV int) int {
	best := low
	for i := low + 1; i < high; i++ {
		if absInt(points[i].UV-targetUV) < absInt(points[best].UV-targetUV) {
			best = i
		}
	}
	return best
}

// A trial is always derived from the original curve, with higher-voltage
// points limited to the new anchor's target. Unaffected points retain their deltas.
func plateau(original []Point, anchor, target, lower, upper int) ([]Point, error) {
	if anchor < 1 || anchor >= len(original)-2 {
		return nil, errors.New("punkt poza roboczą częścią krzywej")
	}
	out := slices.Clone(original)
	peak := 0
	for _, p := range original {
		peak = max(peak, p.LiveKHz)
	}
	if target < 1200000 || target > peak {
		return nil, errors.New("cel poza częstotliwością oryginalnej krzywej")
	}
	if target < original[anchor-1].LiveKHz {
		return nil, errors.New("cel narusza rosnącą krzywą przed kotwicą")
	}
	for i := anchor; i < len(out); i++ {
		p := &out[i]
		p.DeltaKHz += target - p.LiveKHz
		if p.DeltaKHz < lower || p.DeltaKHz > upper || absInt(p.DeltaKHz-original[i].DeltaKHz) > 210000 {
			return nil, errors.New("profil przekracza limit offsetu punktu V/F")
		}
		p.LiveKHz = target
	}
	return out, nil
}

// Validate the requested deltas independently of how a candidate was built.
func validateClockEnvelope(original, target []Point, lower, upper int) error {
	if !equalPointIDs(original, target) || len(original) == 0 {
		return errors.New("zmieniły się identyfikatory lub napięcia punktów fabrycznej siatki V/F")
	}
	for i, p := range target {
		before := original[i]
		change := p.DeltaKHz - before.DeltaKHz
		frequency := before.LiveKHz + change
		if p.LiveKHz != frequency || i > 0 && before.UV <= original[i-1].UV {
			return errors.New("niespójny obraz punktów V/F")
		}
		if change == 0 {
			continue
		}
		if p.DeltaKHz < lower || p.DeltaKHz > upper || absInt(change) > 210000 {
			return errors.New("próba przekroczenia granic offsetu V/F")
		}
		if change > 0 {
			// A higher frequency is allowed only when the same or a greater
			// frequency already belonged to the original curve at a higher voltage.
			if !slices.ContainsFunc(original[i+1:], func(ref Point) bool { return ref.LiveKHz >= frequency }) {
				return errors.New("częstotliwość wymagałaby wyjścia poza oryginalną krzywą OC")
			}
		}
	}
	return nil
}

func withinOCVoltage(s Sample, ceiling float64) error {
	if ceiling < 300 || ceiling > 1300 || math.IsNaN(ceiling) || s.Millivolts == nil || math.IsNaN(*s.Millivolts) || *s.Millivolts < 300 || *s.Millivolts > 1300 {
		return errors.New("brak wiarygodnego napięcia lub pułapu oryginalnego OC")
	}
	if *s.Millivolts > ceiling {
		return fmt.Errorf("odczyt %.2f mV przekroczył zmierzony pułap oryginalnego OC %.2f mV", *s.Millivolts, ceiling)
	}
	return nil
}

func verifyTuningSensors(s Sample) error {
	if s.HotSpotC == nil || !(*s.HotSpotC > 0 && *s.HotSpotC < 105) || s.MemoryMHz == nil || !(*s.MemoryMHz > 0) || s.PerfCap == "" {
		return errors.New("brak bezpiecznego odczytu Hot Spot, zegara VRAM lub PerfCap podczas optymalizacji")
	}
	return checkThermalStop(s)
}

func checkThermalStop(sample Sample) error {
	if sample.TempC == nil || math.IsNaN(*sample.TempC) || *sample.TempC < 0 || *sample.TempC > 120 {
		return errors.New("brak wiarygodnego odczytu temperatury GPU")
	}
	if *sample.TempC >= 87 || (sample.HotSpotC != nil && *sample.HotSpotC >= 105) {
		return errors.New("osiągnięto próg przerwania z powodu temperatury")
	}
	return nil
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func restoreShape(before, after []Point) bool {
	if len(before) == 0 || !equalPointIDs(before, after) {
		return false
	}
	diff := make([]float64, len(before))
	for i, p := range before {
		diff[i] = float64(after[i].LiveKHz - p.LiveKHz)
	}
	drift := median(diff)
	if math.Abs(drift) > 45000 {
		return false
	}
	for _, delta := range diff {
		if math.Abs(delta-drift) > 15000 {
			return false
		}
	}
	return true
}
