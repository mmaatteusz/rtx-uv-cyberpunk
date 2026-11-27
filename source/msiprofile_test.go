package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

func fixtureMSI() (Snapshot, []byte) {
	raw := make([]byte, 12+40*12)
	binary.LittleEndian.PutUint32(raw, 0x20000)
	binary.LittleEndian.PutUint32(raw[4:], 40)
	base := Snapshot{PowerMW: 220000, FactoryPowerMW: 220000, MemoryDeltas: map[uint32]int{0: 200000}}
	for i := 0; i < 40; i++ {
		uv := 850000 + i*6250
		f := 2500 + i*10
		offset := 120 + i
		base.Points = append(base.Points, Point{i, uv, (f + offset) * 1000, offset * 1000})
		pos := 12 + 12*i
		binary.LittleEndian.PutUint32(raw[pos:], math.Float32bits(float32(uv)/1000))
		binary.LittleEndian.PutUint32(raw[pos+4:], math.Float32bits(float32(f)))
		binary.LittleEndian.PutUint32(raw[pos+8:], math.Float32bits(float32(offset)))
	}
	body := "Format=2\r\nMemClkBoost=200000\r\nPowerLimit=100\r\nFanMode=0\r\nThermalLimit=84\r\nVFCurve=" + hex.EncodeToString(raw) + "h"
	return base, []byte(fmt.Sprintf("[Startup]\r\n%s\r\n[Profile1]\r\n%s\r\n[Profile3]\r\nOther=55\r\n", body, body))
}

func TestMSIRejectsMismatchedSlotAndCurve(t *testing.T) {
	base, raw := fixtureMSI()
	sections, _ := msiINI(raw)
	start, original := sections["startup"], sections["profile1"]
	original.fields["fanmode"] = "1"
	if err := compareMSISettings(start, original, base); err == nil {
		t.Fatal("allowed a mismatched fan mode")
	}
	original.fields["fanmode"] = "0"
	err := verifyMSICurve(original.fields["vfcurve"], base.Points)
	if err != nil {
		t.Fatal(err)
	}
	wrong := slices.Clone(base.Points)
	wrong[29].LiveKHz += 100000
	if err = verifyMSICurve(original.fields["vfcurve"], wrong); err == nil {
		t.Fatal("accepted a different curve shape")
	}
	if _, err = msiINI([]byte(strings.Repeat("[Profile1]\r\n", 2))); err == nil {
		t.Fatal("accepted an ambiguous slot name")
	}
}

func TestMeasuredTiesFavorExistingOC(t *testing.T) {
	c := Criteria{FPS: Band{Slack: .5}, Low1: Band{Slack: .6}, Spread: Band{Slack: 12}, Power: Band{Slack: 2}, Temp: Band{Slack: 1}}
	original := Rank{FPS: 90, Low1: 70, ClockSpread: 30, Watts: 221, MaxTemp: 75, Volts: 1060}
	trial := original
	trial.Volts = 1000
	if preferred(trial, original, c) {
		t.Fatal("chose UV simply for a lower voltage without a measured improvement")
	}
	trial.Watts -= 5
	if !preferred(trial, original, c) {
		t.Fatal("ignored a power improvement with equivalent performance")
	}
	trial.FPS -= 1
	if preferred(trial, original, c) {
		t.Fatal("power savings outweighed loss of FPS")
	}
}

func TestChooseNearestHardwareVoltage(t *testing.T) {
	var points []Point
	for i := 0; i < 20; i++ {
		points = append(points, Point{UV: 900000 + i*6250})
	}
	last := 16
	p25 := nearestBelow(points, last, 0, points[last].UV-25000)
	p125 := nearestBelow(points, p25, 0, points[p25].UV-12500)
	if points[last].UV-points[p25].UV != 25000 || points[p25].UV-points[p125].UV != 12500 {
		t.Fatal("did not choose requested hardware voltage steps")
	}
}
