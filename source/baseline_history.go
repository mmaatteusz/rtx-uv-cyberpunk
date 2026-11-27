package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const baselineStage = "Punkt odniesienia"
const dryRunStage = "Dry Run · test odczytu"
const baselineCount = 3

var baselineFiles = []string{"summary.json", "frames.csv", "telemetria.csv"}

func checkHistoricalProfile(dir string, old Session, live Snapshot, msi MSIBackup) error {
	archive, err := zip.OpenReader(filepath.Join(dir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return err
	}
	defer archive.Close()
	var policy recoveryPolicy
	var saved Snapshot
	var manifest MSIBackup
	if err = readArchiveEntry(archive, "tryb-odzyskiwania.json", &policy); err != nil {
		return err
	}
	if policy.Mode != old.Mode || policy.MSISlot != old.MSISlot {
		return errors.New("tryb lub slot w historii różni się od kopii")
	}
	if err = readArchiveEntry(archive, "krzywa-oryginalna.json", &saved); err != nil {
		return err
	}
	if err = validateSavedCurve(saved); err != nil {
		return err
	}
	if !exactOriginal(saved, live) {
		return errors.New("zmienił się sterownik, krzywa GPU lub chronione ustawienia karty")
	}
	if err = readArchiveEntry(archive, "manifest-MSI.json", &manifest); err != nil {
		return err
	}
	if !msi.same(manifest) {
		return errors.New("konfiguracja MSI Afterburnera różni się od kopii z pomiaru")
	}
	return verifyMSIArchive(archive, manifest)
}

func historicalResult(dir string, result Result, gameModified time.Time) (Result, error) {
	if !result.Accepted || result.Error != "" || result.UV != 0 || result.ReusedFrom != "" ||
		result.At.IsZero() || result.At.Before(gameModified) {
		return Result{}, errors.New("brak zaliczonego pomiaru OC dla tej wersji pliku gry")
	}
	switch {
	case result.StartTempC <= 0 || result.StartTempC > maxBenchmarkStartTempC:
		return Result{}, fmt.Errorf("temperatura startowa w historii %.1f°C jest poza zakresem do %d°C", result.StartTempC, maxBenchmarkStartTempC)
	case result.StartCPUPct < 0 || result.StartCPUPct > 12 || result.StartGPUPct < 0 || result.StartGPUPct > 12:
		return Result{}, fmt.Errorf("obciążenie startowe w historii: CPU %.1f%%, GPU %.1f%%, limit 12%%", result.StartCPUPct, result.StartGPUPct)
	case result.TempMax < result.StartTempC || result.TempMax >= 87 || result.HotSpotMax >= 105:
		return Result{}, fmt.Errorf("nieprawidłowe temperatury w historii: start %.1f°C, maksimum GPU %.1f°C, Hot Spot %.1f°C", result.StartTempC, result.TempMax, result.HotSpotMax)
	case result.HotSpotMax <= 0:
		return Result{}, errors.New("brakuje pomiaru Hot Spot w historii")
	case len(result.PerfCap) == 0:
		return Result{}, errors.New("brakuje odczytu PerfCap w historii")
	case result.CoreMedian < 100 || result.CoreMedian > 3600 || result.LoadMedian < 70 || result.LoadMedian > 100 || result.VRAMMHz <= 0:
		return Result{}, fmt.Errorf("nieprawidłowa telemetria GPU w historii: rdzeń %.0f MHz, VRAM %.0f MHz, obciążenie %.0f%%", result.CoreMedian, result.VRAMMHz, result.LoadMedian)
	case result.VoltageMedian < 300 || result.VoltageMax < result.VoltageMedian || result.VoltageMax > 1300:
		return Result{}, fmt.Errorf("nieprawidłowe napięcie w historii: mediana %.1f mV, maksimum %.1f mV", result.VoltageMedian, result.VoltageMax)
	}
	for _, name := range baselineFiles {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			return Result{}, err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return Result{}, fmt.Errorf("brak kompletnego pliku pomiaru %s", name)
		}
	}
	checked := result
	if _, err := readBenchmarkFrames(dir, &checked); err != nil {
		return Result{}, err
	}
	if checked.FrameCount != result.FrameCount || checked.FrameCountRatio != result.FrameCountRatio ||
		math.Abs(checked.Seconds-result.Seconds) > .001 || math.Abs(checked.FPS-result.FPS) > .001 ||
		math.Abs(checked.GameFPS-result.GameFPS) > .001 || math.Abs(checked.Low1-result.Low1) > .001 ||
		math.Abs(checked.Low01-result.Low01) > .001 || math.Abs(checked.FrametimeP99-result.FrametimeP99) > .001 {
		return Result{}, errors.New("zapis wyniku różni się od przeliczonego eksportu gry")
	}
	checked.Path = dir
	return checked, nil
}

func reusableBaseline(ctx context.Context, history string, current Session, live Snapshot, msi MSIBackup) ([]Result, error) {
	game, err := os.Stat(current.Game)
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(history)
	if err != nil {
		return nil, err
	}
	var runs []Result
	var reason error
	for _, entry := range slices.Backward(dirs) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(history, entry.Name())
		var old Session
		if readJSON(filepath.Join(dir, "sesja.json"), &old) != nil || (old.Mode != "Dry Run" && old.Mode != "Pomiar OC") {
			continue
		}
		if old.MSISlot != current.MSISlot || !strings.EqualFold(filepath.Clean(old.Game), filepath.Clean(current.Game)) ||
			old.SettingsSHA256 == "" || old.SettingsSHA256 != current.SettingsSHA256 || old.BenchmarkFromMenu != current.BenchmarkFromMenu {
			if reason == nil {
				reason = errors.New("różnią się ścieżka gry, grafika, tryb startu benchmarku lub numer slotu")
			}
			continue
		}
		if err = checkHistoricalProfile(dir, old, live, msi); err != nil {
			if reason == nil {
				reason = err
			}
			continue
		}
		for i, result := range slices.Backward(old.Runs) {
			if (old.Mode == "Dry Run" && result.Stage != dryRunStage) || (old.Mode == "Pomiar OC" && result.Stage != baselineStage) ||
				!result.Accepted || result.Error != "" {
				continue
			}
			result, err = historicalResult(filepath.Join(dir, fmt.Sprintf("test-%02d", i+1)), result, game.ModTime())
			if err != nil {
				if reason == nil {
					reason = err
				}
				continue
			}
			if len(runs) > 0 && (result.FrameCountRatio != runs[0].FrameCountRatio || math.Abs(result.VRAMMHz-runs[0].VRAMMHz) > 15) {
				continue
			}
			runs = append(runs, result)
			if len(runs) == baselineCount-1 {
				slices.Reverse(runs)
				return runs, nil
			}
		}
	}
	if len(runs) == 0 {
		if reason == nil {
			reason = errors.New("brak zaliczonych przebiegów OC w historii")
		}
		return nil, reason
	}
	return runs, nil
}
