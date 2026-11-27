package main

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFullGameFramesDeterministicTails(t *testing.T) {
	const count = 5000
	var csv strings.Builder
	csv.WriteString("Frame,FrameTimeMs\n")
	for i := 0; i < count; i++ {
		ms := 10
		if i >= count-45 {
			ms = 20
		}
		if i >= count-5 {
			ms = 50
		}
		fmt.Fprintf(&csv, "%d,%d\n", i, ms)
	}
	r := Result{}
	expectedFPS := float64(count) / 50.6
	frames, err := computeFrames(strings.NewReader(csv.String()), reportedFrames{FPS: expectedFPS, Count: count, Time: 50600}, &r)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != count || math.Abs(r.FPS-expectedFPS) > 1e-9 || math.Abs(r.Low1-1000.0/22) > 1e-9 || r.Low01 != 20 {
		t.Fatalf("bad metrics: %#v", r)
	}
	if r.FrametimeSD <= 0 || r.FrametimeP99 != 10 {
		t.Fatalf("unexpected frame statistics: %#v", r)
	}
	for _, bad := range []reportedFrames{
		{FPS: expectedFPS, Count: count - 500, Time: 50.6},
		{FPS: 6, Count: count, Time: 50.6},
		{FPS: expectedFPS, Count: count, Time: 65},
	} {
		if _, err = computeFrames(strings.NewReader(csv.String()), bad, &r); err == nil {
			t.Fatalf("accepted inconsistent output: %#v", bad)
		}
	}
}

func TestFramesFromTimestamps(t *testing.T) {
	const count = 3501
	var csv strings.Builder
	csv.WriteString("Index;Timestamp ms\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&csv, "%d;%d\n", i, i*15)
	}
	r := Result{}
	_, err := computeFrames(strings.NewReader(csv.String()), reportedFrames{FPS: 1000. / 15, Count: count, Time: 52.5}, &r)
	if err != nil {
		t.Fatal(err)
	}
	if r.FrameCount != count-1 || math.Abs(r.FPS-1000./15) > 1e-9 {
		t.Fatalf("bad output: %#v", r)
	}
}

func TestBaselineNoiseControlsRegression(t *testing.T) {
	ref := Result{FPS: 100, Low1: 80, Low01: 65, FrametimeP99: 12.5, CoreMedian: 2805, CoreSpread: 30, VoltageMedian: 1045, VRAMMHz: 1300}
	fast, slow := ref, ref
	fast.FPS, fast.Low1, fast.Low01, fast.FrametimeP99 = 100.45, 80.5, 67, 12.45
	slow.FPS, slow.Low1, slow.Low01, slow.FrametimeP99 = 99.55, 79.5, 64, 12.55
	criteria, err := establish([]Result{ref, slow, fast})
	if err != nil {
		t.Fatal(err)
	}
	if !criteria.accepts(slow) {
		t.Fatal("observed natural variance was rejected")
	}
	slow.FPS -= .15
	if criteria.accepts(slow) {
		t.Fatal("accepted an FPS regression beyond natural variance")
	}
	slow = ref
	slow.Low1 = 78
	if criteria.accepts(slow) {
		t.Fatal("accepted a 1% Low regression")
	}
	slow = ref
	slow.Low01 = 62
	if criteria.accepts(slow) {
		t.Fatal("accepted a 0.1% Low regression")
	}
	slow = ref
	slow.CoreMedian = 2700
	if criteria.accepts(slow) {
		t.Fatal("accepted an underclock")
	}
	slow = ref
	slow.FPS = 91
	if _, err = establish([]Result{ref, slow, fast}); err == nil {
		t.Fatal("accepted a polluted baseline")
	}
}

func TestCurveIsDerivedFromNonuniformOriginal(t *testing.T) {
	original := []Point{
		{0, 900000, 2715000, 130000},
		{1, 925000, 2745000, 145000},
		{2, 950000, 2775000, 157000},
		{3, 975000, 2805000, 165000},
		{4, 1000000, 2820000, 167000},
		{5, 1025000, 2835000, 169000},
	}
	clone := slices.Clone(original)
	derived, err := plateau(original, 2, 2805000, -200000, 250000)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(original, clone) || !slices.Equal(original[:2], derived[:2]) {
		t.Fatal("original low points changed")
	}
	for i := 2; i < len(derived); i++ {
		if derived[i].LiveKHz != 2805000 || derived[i].DeltaKHz != original[i].DeltaKHz+2805000-original[i].LiveKHz {
			t.Fatalf("bad point %d: %v", i, derived[i])
		}
	}
	if _, err = plateau(original, 2, 2865000, -200000, 250000); err == nil {
		t.Fatal("accepted a new frequency beyond the original peak")
	}
	if _, err = plateau(original, 2, 2805000, -200000, 170000); err == nil {
		t.Fatal("accepted an offset beyond the driver limit")
	}
}

func TestProgressReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sesja.json")
	for i := 0; i < 5; i++ {
		if err := writeJSON(path, Session{Winner: i}); err != nil {
			t.Fatal(err)
		}
		var current Session
		if err := readJSON(path, &current); err != nil || current.Winner != i {
			t.Fatalf("checkpoint %d: %+v / %v", i, current, err)
		}
	}
}

func TestClockEnvelopeKeepsVoltageGridAndExistingFrequencies(t *testing.T) {
	original := []Point{
		{Slot: 0, UV: 875000, LiveKHz: 2600000, DeltaKHz: 90000},
		{Slot: 1, UV: 900000, LiveKHz: 2700000, DeltaKHz: 100000},
		{Slot: 2, UV: 925000, LiveKHz: 2750000, DeltaKHz: 105000},
		{Slot: 3, UV: 950000, LiveKHz: 2800000, DeltaKHz: 110000},
		{Slot: 4, UV: 975000, LiveKHz: 2825000, DeltaKHz: 110000},
	}
	trial, err := plateau(original, 1, 2800000, -200000, 225000)
	if err != nil {
		t.Fatal(err)
	}
	if err = validateClockEnvelope(original, trial, -200000, 225000); err != nil {
		t.Fatal(err)
	}
	trial[1].UV += 6250
	if err = validateClockEnvelope(original, trial, -200000, 225000); err == nil {
		t.Fatal("accepted a modified voltage grid")
	}
	trial = slices.Clone(original)
	trial[4].DeltaKHz += 5000
	trial[4].LiveKHz += 5000
	if err = validateClockEnvelope(original, trial, -200000, 225000); err == nil {
		t.Fatal("accepted a new peak frequency above original OC")
	}
	trial = slices.Clone(original)
	trial[1].DeltaKHz += 5000
	if err = validateClockEnvelope(original, trial, -200000, 225000); err == nil {
		t.Fatal("accepted a concealed frequency delta")
	}
}

func TestUVStopsAboveMeasuredOCVoltage(t *testing.T) {
	mv := 1010.00
	sample := Sample{Millivolts: &mv}
	if err := withinOCVoltage(sample, 1010); err != nil {
		t.Fatal(err)
	}
	mv = 1010.01
	if err := withinOCVoltage(sample, 1010); err == nil {
		t.Fatal("allowed sampled voltage above observed OC ceiling")
	}
	if err := withinOCVoltage(Sample{}, 1010); err == nil {
		t.Fatal("accepted missing telemetry")
	}
	mv = 1000
	if err := withinOCVoltage(sample, math.NaN()); err == nil {
		t.Fatal("accepted invalid OC ceiling")
	}
}
