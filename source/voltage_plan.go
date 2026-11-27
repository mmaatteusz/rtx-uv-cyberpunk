package main

import (
	"errors"
	"math"
)

type voltagePlan struct {
	upper, lower int
	referenceUV  int
	targetKHz    int
}

func newVoltagePlan(points []Point, workingMV, coreMHz float64) (voltagePlan, error) {
	plan := voltagePlan{referenceUV: int(math.Round(workingMV * 1000))}
	for i, point := range points {
		if point.UV < plan.referenceUV-3000 {
			plan.upper = i + 1
		}
	}
	if plan.upper < 8 || len(points) < 10 {
		return plan, errors.New("pod obciążeniem GPU pracuje blisko dolnej części krzywej")
	}
	plan.upper = min(plan.upper, len(points)-2)
	reference := plan.upper
	if absInt(points[reference+1].UV-plan.referenceUV) < absInt(points[reference].UV-plan.referenceUV) {
		reference++
	}
	plan.targetKHz = points[reference].LiveKHz
	if math.Abs(float64(plan.targetKHz)/1000-coreMHz) > 105 {
		return plan, errors.New("taktowanie w krzywej różni się od benchmarku o ponad 105 MHz")
	}
	plan.lower = 1
	for plan.lower < plan.upper && points[plan.lower].UV < plan.referenceUV-150000 {
		plan.lower++
	}
	if plan.lower >= plan.upper {
		return plan, errors.New("brak niższych punktów V/F w dozwolonym przedziale")
	}
	return plan, nil
}

type voltageTrial struct {
	Index    int
	WantedUV int
	StepUV   int
}

func (p voltagePlan) explore(points []Point, visit func(voltageTrial) (bool, error)) error {
	const coarse, fine = 25000, 12500
	tried := map[int]bool{}
	good, bad, step := p.upper, p.lower-1, coarse
	for attempt := 0; attempt < 15 && good > p.lower && bad+1 < good; attempt++ {
		desired := points[good].UV - step
		if attempt == 0 {
			desired = p.referenceUV - coarse
		}
		if bad >= p.lower && desired <= points[bad].UV {
			desired = (points[good].UV + points[bad].UV) / 2
		}
		idx := nearestBelow(points, good, max(p.lower, bad+1), desired)
		tried[idx] = true
		ok, err := visit(voltageTrial{idx, desired, step})
		if err != nil {
			return err
		}
		if ok {
			good = idx
		} else {
			bad = idx
			step = fine
		}
	}
	for _, desired := range []int{points[good].UV - fine, points[good].UV + fine} {
		idx := nearestBelow(points, p.upper, p.lower, desired)
		if tried[idx] {
			continue
		}
		tried[idx] = true
		if _, err := visit(voltageTrial{idx, desired, fine}); err != nil {
			return err
		}
	}
	return nil
}
