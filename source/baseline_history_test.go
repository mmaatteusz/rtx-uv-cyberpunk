package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeHistoricalBaseline(t *testing.T, dir string, session Session, msi MSIBackup, base Snapshot) {
	t.Helper()
	runDir := filepath.Join(dir, "test-01")
	if err := os.MkdirAll(runDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := msi.save(filepath.Join(dir, "kopia-MSI-i-VF.zip"), base, recoveryPolicy{session.Mode, session.MSISlot}); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"summary.json":   `{"averageFps":100,"frameNumber":6000,"time":60}`,
		"frames.csv":     "FrameTimeMs\n" + strings.Repeat("10\n", 6000),
		"telemetria.csv": "czas,rdzeń MHz\n2026-11-27T18:25:12Z,2820\n",
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(runDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := Result{At: time.Now(), Stage: dryRunStage, Path: runDir, Accepted: true,
		CoreMedian: 2820, CoreSpread: 30, VRAMMHz: 11000, VoltageMedian: 1012.5, VoltageMax: 1030,
		LoadMedian: 96, WattsMedian: 220, StartTempC: 42, TempMax: 65, HotSpotMax: 75,
		PerfCap: map[string]int{"Moc": 60},
	}
	if _, err := readBenchmarkFrames(runDir, &run); err != nil {
		t.Fatal(err)
	}
	session.Runs = []Result{run, {Stage: dryRunStage, Error: "przerwano przed następnym benchmarkiem"}}
	if err := writeJSON(filepath.Join(dir, "sesja.json"), session); err != nil {
		t.Fatal(err)
	}
}

func TestReuseSelectedBenchmarksFromPartialDryRuns(t *testing.T) {
	fixture := makeRecoveryFiles(t, "Dry Run", true)
	root := t.TempDir()
	history := filepath.Join(root, "sesje")
	game := filepath.Join(root, "Cyberpunk2077.exe")
	if err := os.WriteFile(game, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	state := Session{Mode: "Dry Run", Game: game, SettingsSHA256: "identyczny", MSISlot: fixture.Slot}
	for _, name := range []string{"2026-11-27_16-00", "2026-11-27_16-01"} {
		writeHistoricalBaseline(t, filepath.Join(history, name), state, fixture.MSI, fixture.Before)
	}
	runs, err := reusableBaseline(t.Context(), history, state, fixture.Before, fixture.MSI)
	if err != nil || len(runs) != 2 || runs[0].Path == runs[1].Path {
		t.Fatalf("nie użyto dwóch poprawnych przebiegów z przerwanych sesji: %d, %v", len(runs), err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = reusableBaseline(cancelled, history, state, fixture.Before, fixture.MSI); err == nil {
		t.Fatal("nie obsłużono anulowania")
	}
	changed := state
	changed.SettingsSHA256 = "zaktualizowana grafika"
	if _, err = reusableBaseline(t.Context(), history, changed, fixture.Before, fixture.MSI); err == nil {
		t.Fatal("ponownie użyto wyników z inną grafiką")
	}
	changed = state
	changed.BenchmarkFromMenu = true
	if _, err = reusableBaseline(t.Context(), history, changed, fixture.Before, fixture.MSI); err == nil {
		t.Fatal("ponownie użyto wyników z innym trybem uruchomienia gry")
	}
	other := fixture.Before
	other.GPU.Driver++
	if _, err = reusableBaseline(t.Context(), history, state, other, fixture.MSI); err == nil {
		t.Fatal("ponownie użyto wyników z innym sterownikiem")
	}
	if err := os.WriteFile(filepath.Join(runs[1].Path, "frames.csv"), []byte("niekompletny"), 0600); err != nil {
		t.Fatal(err)
	}
	remaining, err := reusableBaseline(t.Context(), history, state, fixture.Before, fixture.MSI)
	if err != nil || len(remaining) != 1 || remaining[0].Path != runs[0].Path {
		t.Fatalf("nie odfiltrowano uszkodzonego eksportu: %d, %v", len(remaining), err)
	}
}
