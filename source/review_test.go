package main

import (
	"archive/zip"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRejectsMSISlotMissingAnEndpoint(t *testing.T) {
	base, raw := fixtureMSI()
	sections, err := msiINI(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, err := hex.DecodeString(strings.TrimSuffix(sections["profile1"].fields["vfcurve"], "h"))
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[4:], 39)
	if verifyMSICurve(hex.EncodeToString(data[:len(data)-12]), base.Points) == nil {
		t.Fatal("accepted incomplete original")
	}
}

func TestRestoreChecksAllIDsAndCommonFrequencyDrift(t *testing.T) {
	b := voltageFixture()
	if !restoreShape(b, b) {
		t.Fatal("rejected original")
	}
	for _, d := range []int{60000, -60000} {
		v := slices.Clone(b)
		for i := range v {
			v[i].LiveKHz += d
		}
		if restoreShape(b, v) {
			t.Fatal("accepted whole-curve shift", d)
		}
	}
	if restoreShape(b, b[1:]) {
		t.Fatal("accepted missing point")
	}
}

func TestBadFinalistStopsAtFirstRegressionOrIncident(t *testing.T) {
	for _, stop := range []int{1, 3, 5, 6} {
		calls := 0
		runs, err := measureFinalist(func() (Result, error) { calls++; return Result{Accepted: calls != stop}, nil })
		if err != nil || calls != min(stop, 5) || allTrialRunsAccepted(runs) != (stop > 5) {
			t.Fatal(stop, calls, err)
		}
	}
	calls := 0
	expected := errors.New("TDR")
	_, err := measureFinalist(func() (Result, error) { calls++; return Result{}, expected })
	if err != expected || calls != 1 || allTrialRunsAccepted([]Result{{Accepted: true}}) {
		t.Fatal("accepted a short final or ignored TDR")
	}
}

func TestTelemetryRejectsMiddleAndEndGaps(t *testing.T) {
	end := time.Now()
	core, v, w, load, mem := 2800., 995., 150., 99., 10000.
	var data []Sample
	for i := 1; i <= 120; i++ {
		data = append(data, Sample{Time: end.Add(-time.Minute + time.Duration(i)*500*time.Millisecond), CoreMHz: &core, Millivolts: &v, Watts: &w, LoadPct: &load, MemoryMHz: &mem})
	}
	if err := telemetryMetrics(data, end, &Result{Seconds: 60}); err != nil {
		t.Fatal(err)
	}
	for _, g := range [][]Sample{data[:110], data[10:], append(slices.Clone(data[:40]), data[50:]...)} {
		if telemetryMetrics(g, end, &Result{Seconds: 60}) == nil {
			t.Fatal("accepted missing telemetry")
		}
	}
}

func TestTelemetryEvaluatesSceneAfterSlowLoading(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	scene := start.Add(95 * time.Second)
	end := scene.Add(time.Minute)
	core, voltage, watts, memory := 2_800., 995., 150., 10_000.
	high, low := 99., 8.
	var data []Sample
	for at := start; !at.After(end); at = at.Add(500 * time.Millisecond) {
		load := &low
		if at.Equal(start) || !at.Before(scene) {
			load = &high
		}
		data = append(data, Sample{Time: at, CoreMHz: &core, Millivolts: &voltage, Watts: &watts, LoadPct: load, MemoryMHz: &memory})
	}
	result := Result{Seconds: 60}
	if err := telemetryMetrics(data, end, &result); err != nil || result.LoadMedian != high {
		t.Fatalf("prawidłowa scena po długim ładowaniu: obciążenie %.0f%%, błąd %v", result.LoadMedian, err)
	}
	for i := range data {
		data[i].LoadPct = &low
	}
	if err := telemetryMetrics(data, end, &Result{Seconds: 60}); err == nil {
		t.Fatal("zaakceptowano przebieg bez obciążenia GPU w scenie")
	}
}

func TestBenchmarkStartTemperatureAllowsStableReadingsAtOrBelow45(t *testing.T) {
	for _, trial := range []struct {
		samples []float64
		ready   bool
	}{
		{[]float64{42, 41, 41, 41, 40}, true},
		{[]float64{45, 45, 45, 45, 45}, true},
		{[]float64{46, 45, 45, 45, 45}, false},
		{[]float64{45, 44, 43, 42, 41}, false},
		{[]float64{41, 41, 41, 41}, false},
	} {
		if ready := stableBenchmarkTemperature(trial.samples, maxBenchmarkStartTempC); ready != trial.ready {
			t.Errorf("próbki %v: gotowość %t, oczekiwano %t", trial.samples, ready, trial.ready)
		}
	}
	if !stableBenchmarkTemperature([]float64{47, 47, 48, 47, 47}, 0) {
		t.Fatal("Dry Run nie powinien czekać na próg 45°C")
	}
}

func TestFramesRequireIndependentDuration(t *testing.T) {
	data := "FrameTimeMs\n" + strings.Repeat("10\n", 5000)
	if _, err := computeFrames(strings.NewReader(data), reportedFrames{FPS: 100, Count: 5000}, &Result{}); err == nil {
		t.Fatal("accepted missing summary duration")
	}
}

func TestRejectsBrokenMSIFileInsideOtherwiseReadableZIP(t *testing.T) {
	r := makeRecoveryFiles(t, "Optymalizacja", false)
	p := filepath.Join(r.SessionDir, "kopia-MSI-i-VF.zip")
	z, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p + ".new")
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range z.File {
		dst, err := w.Create(e.Name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(e.Name, "Original.cfg") {
			fmt.Fprint(dst, "wrong contents")
			continue
		}
		src, err := e.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.Copy(dst, src); err != nil {
			t.Fatal(err)
		}
		src.Close()
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	z.Close()
	if err = atomicReplace(p+".new", p); err != nil {
		t.Fatal(err)
	}
	if verifyRecoveryBackup(r) == nil {
		t.Fatal("accepted damaged original config")
	}
}

func TestPreviewNeedsFiveSuccessesAndAnHourForThatWinner(t *testing.T) {
	s := Session{Mode: "Optymalizacja", Winner: 950000, Healthy: true, OCCeilingMV: 1050}
	for range 3 {
		s.Runs = append(s.Runs, Result{Stage: "Punkt odniesienia", Accepted: true, CoreMedian: 2800, VoltageMedian: 1040, VoltageMax: 1050})
	}
	for i := 0; i < 65; i++ {
		stage := "Wydłużona próba"
		if i < 5 {
			stage = "Powtórka finalisty"
		}
		s.Runs = append(s.Runs, Result{Stage: stage, UV: s.Winner, Accepted: true, Seconds: 60, TargetMHz: 2800})
	}
	s.Runs = append(s.Runs, Result{Stage: "Kontrola po przywróceniu", Accepted: true})
	if _, err := verifiedWinnerTarget(s); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 4, 10, len(s.Runs) - 1} {
		v := s
		v.Runs = slices.Clone(s.Runs)
		v.Runs[i].Accepted = false
		if _, err := verifiedWinnerTarget(v); err == nil {
			t.Fatal("accepted missing evidence", i)
		}
	}
	s.Runs[8].Seconds -= 1
	if _, err := verifiedWinnerTarget(s); err == nil {
		t.Fatal("accepted only 3599 seconds")
	}
}

func TestWritableModeRequiresSensors(t *testing.T) {
	core, hot, mem := 64., 77., 11000.
	s := Sample{TempC: &core, HotSpotC: &hot, MemoryMHz: &mem, PerfCap: "Moc"}
	if err := verifyTuningSensors(s); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Sample){func(v *Sample) { v.PerfCap = "" }, func(v *Sample) { v.HotSpotC = nil }, func(v *Sample) { v.MemoryMHz = nil }} {
		c := s
		change(&c)
		if verifyTuningSensors(c) == nil {
			t.Fatal("accepted incomplete tuning telemetry")
		}
	}
	hot = 160
	if verifyTuningSensors(s) == nil {
		t.Fatal("dropped high hotspot")
	}
}

func TestRejectsUnsupportedDriverVersionAndDamagedActiveEndpoint(t *testing.T) {
	for i := 0; i < 4; i++ {
		b := syntheticSnapshot()
		switch i {
		case 0:
			put(b.RawTable, 0, 0)
		case 1:
			put(b.RawCurve, 0, 0)
		case 2:
			put(b.RawCurve, 0x44+71*28+8, 0)
		case 3:
			b.RawMask[35] |= 0x80
		}
		if _, err := decodeCurve(b.RawMask, b.RawCurve, b.RawTable); err == nil {
			t.Fatal("accepted damaged driver layout", i)
		}
	}
}

func TestRecoveryUsesArchiveRegardlessOfProgressFile(t *testing.T) {
	for _, mode := range []string{"Optymalizacja", "Dry Run"} {
		r := makeRecoveryFiles(t, mode, mode == "Dry Run")
		path := filepath.Join(r.SessionDir, "sesja.json")
		for _, content := range []string{"{", `{"Mode":"Optymalizacja","MSISlot":5}`} {
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := verifyRecoveryBackup(r); err != nil {
				t.Fatal(mode, err)
			}
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := verifyRecoveryBackup(r); err != nil {
			t.Fatal(mode, err)
		}
		r.Format = 0
		if err := verifyRecoveryBackup(r); err == nil {
			t.Fatal("accepted downgraded journal")
		}
	}
}
