package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Game, MSI string
	Slot      int
}

type Engine struct {
	ctx                                      context.Context
	root, dir                                string
	cfg                                      Config
	gpu                                      *GPU
	base                                     Snapshot
	msi                                      MSIBackup
	recovery                                 Recovery
	armed                                    bool
	session                                  Session
	criteria                                 Criteria
	runner                                   Runner
	update                                   func(Session)
	live                                     func(Sample)
	expected                                 []Point
	lastGuard, watchSince                    time.Time
	baselineVoltage, baselineVRAM, startTemp float64
}

func (e *Engine) save(status string) error {
	e.session.Updated = time.Now()
	e.session.Status = status
	err := writeJSON(filepath.Join(e.dir, "sesja.json"), e.session)
	e.update(e.session)
	return err
}

func (e *Engine) mark() error {
	if !e.armed {
		return nil
	}
	if err := e.recovery.checkWatchdog(); err != nil {
		return err
	}
	return e.recovery.mark()
}

func (e *Engine) guard(full bool) error {
	if err := e.mark(); err != nil {
		return err
	}
	if !full && time.Since(e.lastGuard) < 5*time.Second {
		return nil
	}
	e.lastGuard = time.Now()
	if e.armed && !e.recovery.ReadOnly {
		if err := e.recovery.checkWrite(e.root); err != nil {
			return err
		}
	}
	if !e.watchSince.IsZero() {
		if err := captureDriverEvent(filepath.Join(e.dir, "zdarzenia-sesji-GPU.xml"), e.watchSince); err != nil {
			return err
		}
	}
	if err := checkBenchmarkInterference(); err != nil {
		return err
	}
	if e.runner.gameSettings != "" {
		if err := e.runner.settingsOK(); err != nil {
			return err
		}
	}

	if full {
		if err := e.msi.guard(e.cfg.MSI); err != nil {
			return err
		}
	}
	if !full {
		running, err := processExists("MSIAfterburner.exe")
		if err != nil {
			return err
		}
		if !running {
			return errors.New("MSI Afterburner zakończył działanie w trakcie sesji")
		}
	}

	current, err := e.gpu.snapshot()
	if err != nil {
		return fmt.Errorf("utracono odczyt profilu GPU: %v", err)
	}
	if !e.base.compatibleWith(current) || !e.base.protectedEqual(current) || !preservedTable(e.base, current) ||
		!equalDeltas(e.expected, current.Points) {
		return errors.New("wykryto nadpisanie krzywej lub parametrów chronionych w trakcie sesji")
	}
	return nil
}

func curveShift(before, after, expected []Point) error {
	var refs []float64
	var changes int
	for i, p := range expected {
		if p.DeltaKHz == before[i].DeltaKHz {
			refs = append(refs, float64(after[i].LiveKHz-before[i].LiveKHz))
		} else {
			changes++
		}
	}
	if changes == 0 {
		return nil
	}
	if len(refs) < 10 {
		return errors.New("za mało niezmienionych punktów do weryfikacji krzywej")
	}
	common := int(median(refs))
	var observed, significant int
	for i, p := range expected {
		change := p.DeltaKHz - before[i].DeltaKHz
		if change == 0 {
			continue
		}
		actual := after[i].LiveKHz - before[i].LiveKHz - common
		if absInt(actual-change) > 22000 {
			return fmt.Errorf("sterownik przekształcił krzywą przy %.2f mV (oczekiwano Δ %.1f, jest %.1f MHz)", float64(p.UV)/1000, float64(change)/1000, float64(actual)/1000)
		}
		if absInt(change) >= 30000 {
			significant++
			if absInt(actual) >= 15000 && change*actual > 0 {
				observed++
			}
		}
	}
	if significant > 0 && observed == 0 {
		return errors.New("nie potwierdzono mapowania offsetów na rzeczywiste punkty krzywej")
	}
	return nil
}

func (e *Engine) apply(target []Point) error {
	if e.ctx != nil && e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	if err := e.guard(true); err != nil {
		return err
	}
	sample, err := e.gpu.sample(e.armed && !e.recovery.ReadOnly)
	if err != nil {
		return err
	}
	if err = withinOCVoltage(sample, e.session.OCCeilingMV); err != nil {
		return err
	}
	if err = checkThermalStop(sample); err != nil {
		return err
	}
	before, err := e.gpu.snapshot()
	if err != nil {
		return err
	}
	if e.ctx != nil && e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	after, err := e.gpu.apply(e.ctx, e.base, target, false, func(live Snapshot) error {
		if !equalDeltas(e.expected, live.Points) {
			return errors.New("inny program zmienił krzywą przed zapisem")
		}
		running, err := processExists("Cyberpunk2077.exe")
		if err != nil {
			return err
		}
		if running {
			return errors.New("przed zmianą krzywej zamknij uruchomionego Cyberpunka")
		}
		return e.recovery.checkWrite(e.root)
	})
	if err != nil {
		return err
	}
	if err = curveShift(before.Points, after.Points, target); err != nil {
		return err
	}
	sample, err = e.gpu.sample(e.armed && !e.recovery.ReadOnly)
	if err != nil {
		return err
	}
	if err = withinOCVoltage(sample, e.session.OCCeilingMV); err != nil {
		return err
	}
	if err = checkThermalStop(sample); err != nil {
		return err
	}
	e.expected = slices.Clone(target)
	return nil
}

func (e *Engine) viaMSI() error {
	_ = e.recovery.mark()
	if err := e.msi.guard(e.cfg.MSI); err != nil {
		return err
	}
	if err := verifyOriginalMSISlot(e.msi, e.base, e.cfg.Slot); err != nil {
		return err
	}
	live, err := e.gpu.snapshot()
	if err != nil {
		return err
	}
	if !e.base.compatibleWith(live) {
		return errors.New("niezgodna tożsamość GPU lub siatka V/F przed poleceniem MSI")
	}
	if err := verifiedMSILoad(e.cfg.MSI, e.cfg.Slot, e.msi); err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		current, err := e.gpu.snapshot()
		if err == nil && exactOriginal(e.base, current) && restoreShape(e.base.Points, current.Points) {
			return e.msi.guard(e.cfg.MSI)
		}
	}
	return errors.New("polecenie MSI nie odtworzyło każdego punktu i parametrów bazowych z wybranego slotu")
}

func (e *Engine) restoreDirect() error {
	if !e.armed {
		return nil
	}
	_ = e.recovery.mark()
	current, err := e.gpu.apply(context.Background(), e.base, e.base.Points, true, nil)
	if err != nil {
		return err
	}
	if !exactOriginal(e.base, current) || !restoreShape(e.base.Points, current.Points) {
		return errors.New("pierwotna krzywa lub ustawienia chronione nie przeszły porównania po odtworzeniu")
	}
	e.expected = e.base.Points
	return nil
}

// Restore from the validated in-memory snapshot when recovery files become unusable.
func (e *Engine) recoverIfSafetyFilesChanged() error {
	damage := e.recovery.safetyFilesIntact(e.root)
	if damage == nil {
		return nil
	}
	directErr := e.restoreDirect()
	restoreErr := e.viaMSI()
	if restoreErr != nil {
		restoreErr = errors.Join(directErr, restoreErr)
	}
	_, journalErr := os.Stat(filepath.Join(e.root, recoveryFile))
	if os.IsNotExist(journalErr) {
		journalErr = e.recovery.recreateMissingJournal(e.root)
	}
	if restoreErr != nil {
		return errors.Join(damage, restoreErr, journalErr)
	}
	if journalErr != nil {
		return errors.Join(damage, journalErr)
	}
	return errors.Join(damage, disarm(e.root, e.recovery))
}

func (e *Engine) armOptimization() error {
	offsets := map[int]bool{}
	for _, p := range e.base.Points {
		if p.DeltaKHz != 0 {
			offsets[p.DeltaKHz] = true
		}
	}
	if len(offsets) < 2 {
		return errors.New("nie znaleziono różnych offsetów punktów OC Scanner; sprawdź oryginalny slot MSI")
	}
	if e.base.MinDeltaKHz >= e.base.MaxDeltaKHz || e.base.MinDeltaKHz > 0 || e.base.MaxDeltaKHz < 0 {
		return errors.New("sterownik nie ujawnił granic offsetu zegara")
	}
	if err := verifyOriginalMSISlot(e.msi, e.base, e.cfg.Slot); err != nil {
		return fmt.Errorf("weryfikacja zapisanego slotu przed próbą: %w", err)
	}
	if !e.gpu.hasSetter() {
		return errors.New("brak obsługi zapisu tabeli V/F w tej wersji sterownika")
	}
	e.recovery = Recovery{MSIX: e.cfg.MSI, SessionDir: e.dir, Slot: e.cfg.Slot, Before: e.base, MSI: e.msi}
	if err := arm(e.root, &e.recovery); err != nil {
		return err
	}
	e.armed = true
	e.watchSince = time.Now()
	return nil
}

func (e *Engine) probe() error {
	if err := e.save("Sprawdzanie zapisu i dwóch ścieżek odtworzenia V/F"); err != nil {
		return err
	}
	if err := e.viaMSI(); err != nil {
		return fmt.Errorf("kontrola oryginalnego slotu przed zapisem: %w", err)
	}
	points := slices.Clone(e.base.Points)
	peak := 0
	for _, p := range points {
		peak = max(peak, p.LiveKHz)
	}
	for i := range points {
		if points[i].LiveKHz <= peak-30000 {
			continue
		}
		points[i].DeltaKHz += peak - 30000 - points[i].LiveKHz
		points[i].LiveKHz = peak - 30000
	}
	if err := e.apply(points); err != nil {
		return fmt.Errorf("próba mapowania V/F: %w", err)
	}
	if err := e.restoreDirect(); err != nil {
		return fmt.Errorf("sprawdzenie niezależnego odtworzenia NVAPI: %w", err)
	}
	if err := e.apply(points); err != nil {
		return err
	}
	if err := e.viaMSI(); err != nil {
		return fmt.Errorf("próba odtworzenia przez oryginalny slot MSI: %w", err)
	}
	e.expected = e.base.Points
	return nil
}

func (e *Engine) run(ctx context.Context, stage string, uv, target int) (Result, error) {
	result := Result{At: time.Now(), Stage: stage, UV: uv, TargetMHz: target / 1000,
		Path: filepath.Join(e.dir, fmt.Sprintf("test-%02d", len(e.session.Runs)+1))}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	err := e.guard(true)
	if err == nil {
		err = e.waitComparable(ctx, &result)
	}
	if err == nil {
		err = e.save(fmt.Sprintf("%s · próba %d · %s · start %.0f °C", stage, len(e.session.Runs)+1, voltageLabel(uv), result.StartTempC))
	}
	if err == nil {
		err = e.guard(true)
	}
	if err == nil {
		err = e.runner.run(ctx, &result, e.session.OCCeilingMV)
	}
	if err == nil {
		err = e.guard(true)
	}
	if err != nil {
		result.Error = err.Error()
	}
	if err == nil && result.UV > 0 {
		if math.Abs(result.VRAMMHz-e.baselineVRAM) > 15 {
			err = errors.New("zmienił się obserwowany zegar VRAM pod obciążeniem")
			result.Error = err.Error()
		}
	}
	result.Accepted = err == nil && (result.UV == 0 || e.acceptsTrial(result))
	e.session.Runs = append(e.session.Runs, result)
	if saveErr := e.save(e.session.Status); saveErr != nil {
		return result, errors.Join(err, saveErr)
	}
	return result, err
}

func voltageLabel(uv int) string {
	if uv <= 0 {
		return "oryginalny OC"
	}
	return fmt.Sprintf("%.2f mV", float64(uv)/1000)
}

func (e *Engine) acceptsTrial(r Result) bool {
	return e.criteria.accepts(r) && r.VoltageMedian <= e.baselineVoltage-3 &&
		r.VoltageMedian <= float64(r.UV)/1000+14
}

func (e *Engine) updateRank(rank Rank) error {
	count := len(e.session.Ranking)
	for i, v := range e.session.Ranking {
		if v.UV == rank.UV {
			e.session.Ranking[i] = rank
			return e.save(e.session.Status)
		}
	}
	e.session.Ranking = append(e.session.Ranking, rank)
	if len(e.session.Ranking) > count {
		return e.save(e.session.Status)
	}
	return nil
}

type candidate struct {
	idx     int
	points  []Point
	rank    Rank
	results []Result
}

func favoriteCandidate(candidates []*candidate, c Criteria) int {
	best := 0
	for i := 1; i < len(candidates); i++ {
		if preferred(candidates[i].rank, candidates[best].rank, c) {
			best = i
		}
	}
	return best
}

func (e *Engine) setCandidate(points []Point) error {
	if equalDeltas(e.expected, points) {
		return e.guard(true)
	}
	return e.apply(points)
}

func (e *Engine) controlOC(ctx context.Context, stage string) error {
	r, err := e.run(ctx, stage, 0, 0)
	if err != nil {
		return err
	}
	c := e.criteria
	if math.Abs(r.FPS-c.FPS.Center) > max(.2, 1.5*c.FPS.Slack) ||
		math.Abs(r.Low1-c.Low1.Center) > max(.25, 1.5*c.Low1.Slack) ||
		math.Abs(r.Low01-c.Low01.Center) > max(.5, 1.5*c.Low01.Slack) ||
		math.Abs(r.FrametimeP99-c.P99.Center) > max(.1, 1.5*c.P99.Slack) ||
		math.Abs(r.CoreMedian-c.Core.Center) > max(30, 1.5*c.Core.Slack) ||
		math.Abs(r.VRAMMHz-e.baselineVRAM) > 15 {
		return errors.New("kontrolny pomiar oryginalnego OC wykracza poza rozrzut bazowy lub zmienił się zegar VRAM")
	}
	return nil
}

func (e *Engine) periodicOC(ctx context.Context, stage string) error {
	if err := e.restoreDirect(); err != nil {
		return err
	}
	return e.controlOC(ctx, stage)
}

func (e *Engine) dryRun(ctx context.Context) error {
	// Exercise the same saved-slot check and candidate planner with the write
	// path disabled. Simulated outcomes are kept separate from real measurements.
	if err := verifyOriginalMSISlot(e.msi, e.base, e.cfg.Slot); err != nil {
		return err
	}
	plan, err := newVoltagePlan(e.base.Points, e.baselineVoltage, e.criteria.Core.Center)
	if err != nil {
		return err
	}
	type plannedTrial struct {
		RequestedUV, DriverPointUV, StepUV int
		Supported, SimulatedPass           bool
	}
	var trials []plannedTrial
	err = plan.explore(e.base.Points, func(trial voltageTrial) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		uv := e.base.Points[trial.Index].UV
		_, curveErr := e.candidatePoints(plan, trial.Index)
		supported := curveErr == nil
		pass := supported && uv >= plan.referenceUV-37500
		trials = append(trials, plannedTrial{trial.WantedUV, uv, trial.StepUV, supported, pass})
		return pass, nil
	})
	if err != nil {
		return err
	}
	if err = writeJSON(filepath.Join(e.dir, "plan-dry-run.json"), trials); err != nil {
		return err
	}
	e.recovery = Recovery{MSIX: e.cfg.MSI, SessionDir: e.dir, Slot: e.cfg.Slot, Before: e.base, MSI: e.msi, ReadOnly: true}
	if err := arm(e.root, &e.recovery); err != nil {
		return err
	}
	e.armed = true
	e.watchSince = time.Now()
	for _, stage := range []string{"Dry Run · symulacja kroku 25 mV", "Dry Run · symulacja kroku 12,5 mV", "Dry Run · kontrola OC"} {
		if err := e.controlOC(ctx, stage); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) candidatePoints(plan voltagePlan, idx int) ([]Point, error) {
	points, err := plateau(e.base.Points, idx, plan.targetKHz, e.base.MinDeltaKHz, e.base.MaxDeltaKHz)
	if err != nil {
		return nil, err
	}
	if err = validateClockEnvelope(e.base.Points, points, e.base.MinDeltaKHz, e.base.MaxDeltaKHz); err != nil {
		return nil, err
	}
	return points, nil
}

func (e *Engine) search(ctx context.Context, ocRank Rank) error {
	plan, err := newVoltagePlan(e.base.Points, e.baselineVoltage, e.criteria.Core.Center)
	if err != nil {
		return err
	}
	target := plan.targetKHz

	sinceControl := 0
	var passing []*candidate
	try := func(trial voltageTrial) (bool, error) {
		idx := trial.Index
		uv := e.base.Points[idx].UV
		points, err := e.candidatePoints(plan, idx)
		if err != nil {
			return false, e.updateRank(Rank{UV: uv, Phase: "Poza zakresem offsetu"})
		}
		if sinceControl >= 4 {
			if err = e.periodicOC(ctx, "Kontrola w doborze"); err != nil {
				return false, err
			}
			sinceControl = 0
		}
		if err = e.setCandidate(points); err != nil {
			return false, err
		}
		sinceControl++
		result, err := e.run(ctx, "Dobór napięcia", uv, target)
		entry, combined := combine([]Result{result}, "Dobór", e.criteria)
		entry.Accepted = result.Accepted
		if err != nil {
			return false, err
		}
		if saveErr := e.updateRank(entry); saveErr != nil {
			return false, saveErr
		}
		if !result.Accepted {
			return false, e.restoreDirect()
		}
		if combined.FPS > 0 {
			passing = append(passing, &candidate{idx, points, entry, []Result{result}})
		}
		return true, nil
	}
	if err := plan.explore(e.base.Points, try); err != nil {
		return err
	}

	if len(passing) > 0 {
		if err := e.periodicOC(ctx, "Kontrola przed finałem"); err != nil {
			return err
		}
	}
	var finalists []*candidate
	for finalistNo := 0; finalistNo < 3 && len(passing) > 0; finalistNo++ {
		bestIdx := favoriteCandidate(passing, e.criteria)
		candidate := passing[bestIdx]
		passing = slices.Delete(passing, bestIdx, bestIdx+1)
		if finalistNo > 0 {
			if err := e.periodicOC(ctx, "Kontrola między finalistami"); err != nil {
				return err
			}
		}
		if err := e.setCandidate(candidate.points); err != nil {
			return err
		}
		repeats, err := measureFinalist(func() (Result, error) {
			return e.run(ctx, "Powtórka finalisty", candidate.rank.UV, target)
		})
		if err != nil {
			return err
		}
		candidate.results = append(candidate.results, repeats...)
		rank, combined := combine(candidate.results[1:], "Finalista", e.criteria)
		rank.Accepted = rank.Accepted && e.acceptsTrial(combined) && allTrialRunsAccepted(candidate.results[1:])
		candidate.rank = rank
		if err := e.updateRank(rank); err != nil {
			return err
		}
		if rank.Accepted {
			finalists = append(finalists, candidate)
		} else if err := e.restoreDirect(); err != nil {
			return err
		}
	}
	// Maintain deterministic traversal with natural-noise comparisons that need
	// not be transitive. Re-select against the remaining finalists on failure.
	var selected *candidate
	var winnerRank Rank
	for len(finalists) > 0 {
		bestIndex := favoriteCandidate(finalists, e.criteria)
		best := finalists[bestIndex]
		finalists = slices.Delete(finalists, bestIndex, bestIndex+1)
		if !preferred(best.rank, ocRank, e.criteria) {
			continue
		}
		if err := e.setCandidate(best.points); err != nil {
			return err
		}
		var stress []Result
		since := time.Now()
		ok := true
		// Count only the actual scene toward the minimum hour.
		var activeSeconds float64
		nextControl := 600.0
		for activeSeconds < 3600 {
			if activeSeconds >= nextControl {
				if err := e.periodicOC(ctx, "Kontrola w próbie godzinnej"); err != nil {
					return err
				}
				if err := e.setCandidate(best.points); err != nil {
					return err
				}
				nextControl += 600
			}
			run, err := e.run(ctx, "Wydłużona próba", best.rank.UV, target)
			if err != nil {
				return err
			}
			stress = append(stress, run)
			activeSeconds += run.Seconds
			if !e.acceptsTrial(run) {
				ok = false
				break
			}
			if time.Since(since) > 4*time.Hour {
				return errors.New("nie udało się uzyskać 60 minut sceny w cztery godziny")
			}
		}
		rank, combined := combine(stress, "Próba wydłużona", e.criteria)
		if !ok || !rank.Accepted || !e.acceptsTrial(combined) || !preferred(rank, ocRank, e.criteria) {
			rank.Accepted = false
			if err := e.updateRank(rank); err != nil {
				return err
			}
			if err := e.restoreDirect(); err != nil {
				return err
			}
			continue
		}
		rank.Phase = "Godzina zaliczona"
		if err := e.updateRank(rank); err != nil {
			return err
		}
		if selected == nil || preferred(rank, winnerRank, e.criteria) {
			selected, winnerRank = best, rank
		}
		if err := e.periodicOC(ctx, "Kontrola po godzinie"); err != nil {
			return err
		}
	}
	if selected != nil {
		winnerRank.Phase = "Zwycięzca"
		if err := e.updateRank(winnerRank); err != nil {
			return err
		}
		if err := writeJSON(filepath.Join(e.dir, "wybrany-profil.json"), selected.points); err != nil {
			return err
		}
		if err := saveCurveCSV(filepath.Join(e.dir, "wybrana-krzywa.csv"), selected.points); err != nil {
			return err
		}
		e.session.Winner = winnerRank.UV
	}
	return nil
}

func saveCurveCSV(path string, points []Point) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	w := csv.NewWriter(out)
	if err = w.Write([]string{"punkt", "napięcie mV", "rdzeń MHz", "delta kHz"}); err != nil {
		return err
	}
	for _, p := range points {
		row := []string{strconv.Itoa(p.Slot), fmt.Sprintf("%.3f", float64(p.UV)/1000), fmt.Sprintf("%.3f", float64(p.LiveKHz)/1000), strconv.Itoa(p.DeltaKHz)}
		if err = w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	if err = w.Error(); err != nil {
		return err
	}
	return out.Sync()
}

func (e *Engine) start(ctx context.Context, mode string) (retErr error) {
	e.ctx = ctx
	lease, err := singleInstance()
	if err != nil {
		return err
	}
	defer closeHandle(lease)
	if _, err = os.Stat(filepath.Join(e.root, recoveryFile)); !os.IsNotExist(err) {
		return errors.New("oczekuje odzyskiwanie. Użyj „Odzyskaj oryginał”")
	}
	if !filepath.IsAbs(e.cfg.Game) || !strings.EqualFold(filepath.Base(e.cfg.Game), "Cyberpunk2077.exe") {
		return errors.New("wskaż pełną ścieżkę do bin\\x64\\Cyberpunk2077.exe")
	}
	if !filepath.IsAbs(e.cfg.MSI) || !strings.EqualFold(filepath.Base(e.cfg.MSI), "MSIAfterburner.exe") {
		return errors.New("wskaż pełną ścieżkę do MSIAfterburner.exe")
	}
	if _, err = os.Stat(e.cfg.Game); err != nil {
		return err
	}
	if mode != "Pomiar OC" && (e.cfg.Slot < 1 || e.cfg.Slot > 5) {
		return errors.New("wskaż już zapisany, oryginalny slot MSI (1–5)")
	}
	doc, err := knownFolder(&documentsGUID)
	if err != nil {
		return err
	}
	e.dir, err = os.MkdirTemp(filepath.Join(e.root, "sesje"), time.Now().Format("2006-01-02_15-04-05")+"-")
	if err != nil {
		return err
	}
	e.session = Session{Started: time.Now(), Mode: mode, Game: e.cfg.Game, MSISlot: e.cfg.Slot}
	if err = e.save("Kopiowanie konfiguracji MSI i odczyt V/F"); err != nil {
		return err
	}
	defer func() {
		if e.armed && !e.recovery.ReadOnly && retErr != nil {
			retErr = errors.Join(retErr, e.recoverIfSafetyFilesChanged())
		}
		if e.armed && (retErr == nil || e.recovery.ReadOnly) {
			var restoreErr error
			if e.recovery.ReadOnly {
				current, err := e.gpu.snapshot()
				restoreErr = err
				if restoreErr == nil {
					restoreErr = confirmOriginal(e.recovery, current)
				}
			} else {
				restoreErr = e.restoreDirect()
			}
			if restoreErr == nil {
				restoreErr = disarm(e.root, e.recovery)
			}
			retErr = errors.Join(retErr, restoreErr)
		}
		switch {
		case retErr != nil:
			status := "Przerwano. " + retErr.Error()
			retErr = errors.Join(retErr, e.save(status))
		case mode == "Dry Run":
			e.session.Healthy = true
			retErr = e.save("Dry Run ukończony. Oryginalne ustawienia potwierdzone")
		case mode != "Optymalizacja":
			e.session.Healthy = true
			retErr = e.save("Ukończono pomiary aktualnego profilu OC")
		case e.session.Winner == 0:
			e.session.Healthy = true
			retErr = e.save("Przywrócono oryginał. Żaden profil UV nie wygrał z obecnym OC")
		default:
			e.session.Healthy = true
			retErr = e.save("Przywrócono oryginał. Wynik UV oczekuje na Twoją decyzję")
		}
	}()
	cfgPath := filepath.Join(e.msiPath(), "MSIAfterburner.cfg")
	if _, err = os.Stat(cfgPath); err != nil {
		return err
	}
	e.msi, err = scanMSI(e.cfg.MSI)
	if err != nil {
		return err
	}
	if err = e.msi.guard(e.cfg.MSI); err != nil {
		return err
	}
	e.gpu, err = openGPU()
	if err != nil {
		return err
	}
	// Restoration uses the same handle before it is released by the outer caller.
	e.base, err = e.gpu.snapshot()
	if err != nil {
		return err
	}
	if err = e.msi.save(filepath.Join(e.dir, "kopia-MSI-i-VF.zip"), e.base, recoveryPolicy{mode, e.cfg.Slot}); err != nil {
		return err
	}
	if err = saveCurveCSV(filepath.Join(e.dir, "oryginalna-krzywa.csv"), e.base.Points); err != nil {
		return err
	}
	e.expected = e.base.Points
	if mode == "Optymalizacja" {
		if err = e.armOptimization(); err != nil {
			return err
		}
	}
	gameConfig := filepath.Join(os.Getenv("LOCALAPPDATA"), "CD Projekt Red", "Cyberpunk 2077", "UserSettings.json")
	settingsHash, err := hashPath(gameConfig)
	if err != nil {
		return fmt.Errorf("nie można ustalić odcisku UserSettings.json: %w", err)
	}
	e.session.SettingsSHA256 = settingsHash
	e.runner = Runner{game: e.cfg.Game, gameResults: filepath.Join(doc, "CD Projekt Red", "Cyberpunk 2077", "benchmarkResults"),
		gameSettings: gameConfig, settingsHash: settingsHash, folder: e.dir, gpu: e.gpu, fullSensors: mode == "Optymalizacja", mark: func() error { return e.guard(false) }, live: e.live}
	var baseRuns []Result
	for i := 0; i < 3; i++ {
		run, err := e.run(ctx, "Punkt odniesienia", 0, 0)
		if err != nil {
			return err
		}
		baseRuns = append(baseRuns, run)
		e.session.OCCeilingMV = max(e.session.OCCeilingMV, run.VoltageMax)
	}
	e.criteria, err = establish(baseRuns)
	if err != nil {
		return err
	}
	ocRank, _ := combine(baseRuns, "Oryginalny OC", e.criteria)
	e.baselineVoltage = ocRank.Volts
	e.baselineVRAM = median([]float64{baseRuns[0].VRAMMHz, baseRuns[1].VRAMMHz, baseRuns[2].VRAMMHz})
	if err = e.updateRank(ocRank); err != nil {
		return err
	}
	if mode == "Dry Run" {
		return e.dryRun(ctx)
	}
	if mode != "Optymalizacja" {
		return nil
	}
	if err = e.probe(); err != nil {
		return err
	}
	if err = e.search(ctx, ocRank); err != nil {
		return err
	}
	if err = e.restoreDirect(); err != nil {
		return err
	}
	if err = e.viaMSI(); err != nil {
		return err
	}
	return e.controlOC(ctx, "Kontrola po przywróceniu")
}

func (e *Engine) msiPath() string { return filepath.Dir(e.cfg.MSI) }
