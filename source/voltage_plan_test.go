package main

import (
	"errors"
	"testing"
)

func voltageFixture() []Point {
	points := make([]Point, 64)
	for i := range points {
		points[i] = Point{Slot: i, UV: 700000 + i*6250, LiveKHz: 2450000 + i*10000, DeltaKHz: 100000 + i*1000}
	}
	return points
}

func TestVoltageSearchStartsAt25AndNarrowsTo125(t *testing.T) {
	points := voltageFixture()
	plan, err := newVoltagePlan(points, 1000, 2930)
	if err != nil {
		t.Fatal(err)
	}
	var trials []voltageTrial
	seen := map[int]bool{}
	err = plan.explore(points, func(trial voltageTrial) (bool, error) {
		if seen[trial.Index] {
			t.Fatalf("repeated driver point %+v", trial)
		}
		seen[trial.Index] = true
		trials = append(trials, trial)
		return points[trial.Index].UV >= 962500, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(trials) < 3 || points[trials[0].Index].UV != 975000 || trials[0].StepUV != 25000 {
		t.Fatalf("unexpected initial anchor: %+v", trials)
	}
	if points[trials[1].Index].UV != 950000 || points[trials[2].Index].UV != 962500 {
		t.Fatalf("did not narrow to expected anchor: %+v", trials)
	}
	for _, trial := range trials[2:] {
		if trial.StepUV != 12500 {
			t.Fatalf("used coarse exploratory step after failure: %+v", trial)
		}
	}
}

func TestVoltageSearchDoesNotScheduleAnotherTrialAfterError(t *testing.T) {
	points := voltageFixture()
	plan, err := newVoltagePlan(points, 1000, 2930)
	if err != nil {
		t.Fatal(err)
	}
	incident := errors.New("driver reset")
	calls := 0
	err = plan.explore(points, func(trial voltageTrial) (bool, error) {
		calls++
		if calls == 2 {
			return false, incident
		}
		return true, nil
	})
	if calls != 2 || !errors.Is(err, incident) {
		t.Fatalf("calls %d, error %v", calls, err)
	}
}

func TestFinalistCannotHideOneRejectedRun(t *testing.T) {
	runs := []Result{{Accepted: true}, {Accepted: true}, {Accepted: true}, {Accepted: true}, {Accepted: true}}
	if !allTrialRunsAccepted(runs) {
		t.Fatal("all five runs should qualify")
	}
	runs[2].Accepted = false
	if allTrialRunsAccepted(runs) {
		t.Fatal("rejected benchmark was masked by the others")
	}
}

func TestCoreRegressionCannotUseLargeBaselineSpreadAsAllowance(t *testing.T) {
	c := Criteria{Core: Band{Center: 2800, Slack: 90}}
	if c.accepts(Result{CoreMedian: 2769}) {
		t.Fatal("accepted more than 30 MHz below the reference")
	}
}
