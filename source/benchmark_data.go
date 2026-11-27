package main

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const warmupStage = "Rozgrzewka"

func benchmarkStartTemperature(samples []float64, target float64) (float64, bool) {
	if len(samples) < 5 {
		return target, false
	}
	low, high := slices.Min(samples), slices.Max(samples)
	if high-low > 2 || (target != 0 && (low < target-2 || high > target+2)) {
		return target, false
	}
	if target == 0 {
		target = median(samples)
	}
	return target, true
}

func records(folder string) ([]string, error) {
	items, err := os.ReadDir(folder)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range items {
		if d.IsDir() && strings.HasPrefix(strings.ToLower(d.Name()), "benchmark_") {
			names = append(names, d.Name())
		}
	}
	return names, nil
}

func latestNew(folder string, before []string, since time.Time) (string, error) {
	known := map[string]bool{}
	for _, name := range before {
		known[name] = true
	}
	var found []string
	now, err := records(folder)
	if err != nil {
		return "", err
	}
	for _, name := range now {
		if known[name] {
			continue
		}
		root := filepath.Join(folder, name)
		frames, e1 := os.Stat(filepath.Join(root, "frames.csv"))
		header, e2 := os.Stat(filepath.Join(root, "summary.json"))
		if e1 == nil && e2 == nil && frames.ModTime().After(since.Add(-time.Second)) && header.ModTime().After(since.Add(-time.Second)) {
			found = append(found, root)
		}
	}
	if len(found) != 1 {
		return "", fmt.Errorf("oczekiwano jednego nowego kompletnego wyniku gry, znaleziono %d", len(found))
	}
	return found[0], nil
}

func summaryFrom(path string) (reportedFrames, error) {
	var doc struct {
		reportedFrames
		Data reportedFrames
	}
	if err := readJSON(path, &doc); err != nil {
		return reportedFrames{}, err
	}
	if doc.Data.Count > 0 && doc.Data.FPS > 0 {
		return doc.Data, nil
	}
	return doc.reportedFrames, nil
}

func number(p *float64, places int) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', places, 64)
}

func recordTelemetry(out io.Writer, values []Sample) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"czas", "rdzeń MHz", "VRAM MHz", "napięcie mV", "pobór W", "GPU °C", "Hot Spot °C", "obciążenie %", "PerfCap"}); err != nil {
		return err
	}
	for _, s := range values {
		if err := w.Write([]string{s.Time.Format(time.RFC3339Nano), number(s.CoreMHz, 1), number(s.MemoryMHz, 1), number(s.Millivolts, 2),
			number(s.Watts, 2), number(s.TempC, 1), number(s.HotSpotC, 2), number(s.LoadPct, 1), s.PerfCap}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func telemetryMetrics(values []Sample, end time.Time, r *Result) error {
	begin := end.Add(-time.Duration(r.Seconds * float64(time.Second)))
	var clocks, volts, watts, loads, memory []float64
	previous := begin
	const maxGap = 2500 * time.Millisecond
	r.PerfCap = map[string]int{}
	for _, s := range values {
		if s.Time.Before(begin) || s.Time.After(end) {
			continue
		}
		if s.LoadPct == nil || s.CoreMHz == nil || s.Millivolts == nil || s.Watts == nil {
			continue
		}
		if s.Time.Before(previous) || s.Time.Sub(previous) > maxGap {
			return fmt.Errorf("luka w telemetrii sceny przy %s", s.Time.Format(time.RFC3339))
		}
		previous = s.Time
		loads = append(loads, *s.LoadPct)
		if s.TempC != nil {
			r.TempMax = max(r.TempMax, *s.TempC)
		}
		if s.HotSpotC != nil {
			r.HotSpotMax = max(r.HotSpotMax, *s.HotSpotC)
		}
		if s.PerfCap != "" {
			r.PerfCap[s.PerfCap]++
		}
		if *s.LoadPct < 65 {
			continue
		}
		clocks = append(clocks, *s.CoreMHz)
		volts = append(volts, *s.Millivolts)
		watts = append(watts, *s.Watts)
		if s.MemoryMHz != nil {
			memory = append(memory, *s.MemoryMHz)
		}
	}
	if end.Sub(previous) > maxGap {
		return fmt.Errorf("brakuje telemetrii końca sceny")
	}
	r.LoadMedian = median(loads)
	if len(loads) < int(r.Seconds) || len(clocks) < int(r.Seconds*.9) || r.LoadMedian < 70 || len(memory) < int(r.Seconds*.8) {
		return fmt.Errorf("za mało telemetrii pod obciążeniem RTX (mediana %.0f%%, %d próbek GPU/V, %d VRAM)", r.LoadMedian, len(clocks), len(memory))
	}
	r.CoreMedian, r.CoreP10 = median(clocks), quantile(clocks, .10)
	r.CoreSpread = quantile(clocks, .90) - r.CoreP10
	r.VoltageMedian, r.WattsMedian, r.VRAMMHz = median(volts), median(watts), median(memory)
	return nil
}
