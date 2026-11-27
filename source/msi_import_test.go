package main

import (
	"bytes"
	"encoding/binary"
	"maps"
	"math"
	"strings"
	"testing"
)

func msiImportFixture(t *testing.T) (Snapshot, []Point, []byte) {
	t.Helper()
	base, raw := fixtureMSI()
	base.MinDeltaKHz, base.MaxDeltaKHz = -400000, 500000
	target, err := plateau(base.Points, 21, base.Points[28].LiveKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil {
		t.Fatal(err)
	}
	profile := strings.ReplaceAll(string(raw), "[Profile1]", "[Profile2]")
	return base, target, []byte(profile)
}

func TestMSIImportPreservesOccupiedAndStartupBytes(t *testing.T) {
	base, target, raw := msiImportFixture(t)
	slot, out, err := planMSIImport(raw, raw, base, target, 2)
	if err != nil || slot != 4 || !bytes.HasPrefix(out, raw) {
		t.Fatalf("slot=%d, existing content preserved=%t, err=%v", slot, bytes.HasPrefix(out, raw), err)
	}
	before, _ := msiINI(raw)
	after, err := msiINI(out)
	if err != nil {
		t.Fatal(err)
	}
	original, next := before["profile2"].fields, after["profile4"].fields
	for key, value := range original {
		if key != "vfcurve" && key != "coreclkboost" && value != next[key] {
			t.Fatalf("changed protected field %s", key)
		}
	}
	if next["coreclkboost"] != "1000000" || !maps.Equal(before["startup"].fields, after["startup"].fields) {
		t.Fatal("invalid custom-curve flag or modified Startup")
	}
	oldCurve, positions, err := mapMSICurve(original["vfcurve"], base.Points)
	if err != nil {
		t.Fatal(err)
	}
	newCurve, _, err := mapMSICurve(next["vfcurve"], target)
	if err != nil {
		t.Fatal(err)
	}
	for i, pos := range positions {
		diff := float32(target[i].DeltaKHz-base.Points[i].DeltaKHz) / 1000
		before := math.Float32frombits(binary.LittleEndian.Uint32(oldCurve[pos+8:]))
		after := math.Float32frombits(binary.LittleEndian.Uint32(newCurve[pos+8:]))
		if !bytes.Equal(oldCurve[pos:pos+8], newCurve[pos:pos+8]) || math.Abs(float64(after-before-diff)) > .0002 {
			t.Fatalf("incorrect encoding at point %d", i)
		}
	}
	if _, _, err := planMSIImport(out, raw, base, target, 2); err == nil || !strings.Contains(err.Error(), "już zapisana") {
		t.Fatalf("did not prevent a duplicate import: %v", err)
	}
}

func TestMSIImportOnlyReplacesGenuinelyEmptyPlaceholder(t *testing.T) {
	base, target, raw := msiImportFixture(t)
	raw = bytes.ReplaceAll(raw, []byte("Other=55"), []byte("; wolny\r\nFormat=\r\nVFCurve=\r\n\r\n[Settings]\r\nCaptureDefaults=0"))
	slot, out, err := planMSIImport(raw, raw, base, target, 2)
	if err != nil || slot != 3 {
		t.Fatalf("did not use placeholder: slot=%d, err=%v", slot, err)
	}
	if !bytes.Equal(bytes.Split(raw, []byte("[Profile3]"))[0], bytes.Split(out, []byte("[Profile3]"))[0]) ||
		!bytes.HasSuffix(out, []byte("; wolny\r\n\r\n[Settings]\r\nCaptureDefaults=0\r\n")) {
		t.Fatal("changed bytes outside the destination slot")
	}
}

func TestMSIImportRejectsChangedOriginalAndAllSlotsOccupied(t *testing.T) {
	base, target, raw := msiImportFixture(t)
	current := bytes.Replace(raw, []byte("FanMode=0"), []byte("FanMode=1"), 1)
	if _, _, err := planMSIImport(current, raw, base, target, 2); err == nil {
		t.Fatal("accepted changed Startup")
	}
	occupied := append(bytes.Clone(raw), []byte("[Profile1]\r\nMemClkBoost=0\r\n[Profile4]\r\nCustom=\r\n[Profile5]\r\nFormat=2\r\n")...)
	if _, _, err := planMSIImport(occupied, raw, base, target, 2); err == nil {
		t.Fatal("overwrote existing slot")
	}
	for _, slot := range []int{2, 3} {
		if _, err := insertMSIProfile(raw, 2, slot, nil); err == nil {
			t.Fatalf("did not defend slot %d", slot)
		}
	}
}
