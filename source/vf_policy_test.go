package main

import (
	"bytes"
	"maps"
	"slices"
	"testing"
)

func syntheticSnapshot() Snapshot {
	mask, curve, table := make([]byte, maskSize), make([]byte, curveSize), make([]byte, tableSize)
	put(mask, 0, version(maskSize, 1))
	put(curve, 0, version(curveSize, 1))
	for i := 0; i < 72; i++ {
		mask[4+i/8] |= 1 << uint(i%8)
		off := 0x44 + i*28
		put(curve, off+4, uint32(1950000+i*15000))
		put(curve, off+8, uint32(650000+i*6250))
		put(table, tableDelta(i), uint32(112000+i*600))
	}
	put(table, 0, version(tableSize, 1))
	// Bit 128 and its entry stand for a different hardware domain.
	mask[4+128/8] |= 1
	put(curve, 0x44+128*28, 1)
	put(table, tableDelta(128), 87000)
	// Keep raw bytes that must be passed through rather than synthesized.
	table[55] = 0xE2
	points, err := decodeCurve(mask, curve, table)
	if err != nil {
		panic(err)
	}
	boost := 0.0
	return Snapshot{
		GPU:    Identity{Name: "RTX 4070 SUPER", UUID: "GPU-simulated", Driver: 123},
		Points: points, RawMask: mask, RawCurve: curve, RawTable: table,
		PowerMW: 210000, FactoryPowerMW: 220000, MinPowerMW: 140000, MaxPowerMW: 240000,
		MinDeltaKHz: -220000, MaxDeltaKHz: 400000, VoltageBoostPercent: &boost,
		MemoryDeltas: map[uint32]int{0: 200000}, GraphicsDeltas: map[uint32]int{0: 0},
		MemoryLimits:    map[uint32][2]int{0: {11000000, 11300000}},
		ThermalPolicies: []uint32{1, 85},
	}
}

func TestDecodeCurveAcceptsOnlyEmptyTrailingRequests(t *testing.T) {
	base := syntheticSnapshot()
	base.RawMask[4+72/8] |= 1 << (72 % 8)
	points, err := decodeCurve(base.RawMask, base.RawCurve, base.RawTable)
	if err != nil || !slices.Equal(points, base.Points) {
		t.Fatalf("pusty koniec tabeli: %v", err)
	}

	for _, name := range []string{"offset bez stanu", "niepełny stan", "aktywny punkt po przerwie"} {
		t.Run(name, func(t *testing.T) {
			mask, curve, table := slices.Clone(base.RawMask), slices.Clone(base.RawCurve), slices.Clone(base.RawTable)
			switch name {
			case "offset bez stanu":
				put(table, tableDelta(72), 5_000)
			case "niepełny stan":
				put(curve, 0x44+72*28+4, 2_500_000)
			case "aktywny punkt po przerwie":
				mask[4+73/8] |= 1 << (73 % 8)
				put(curve, 0x44+73*28+4, 2_500_000)
				put(curve, 0x44+73*28+8, 1_100_000)
			}
			if _, err := decodeCurve(mask, curve, table); err == nil {
				t.Fatal("niejednoznaczny punkt został zaakceptowany")
			}
		})
	}
}

func TestAdaReportedMemoryEntryIsExcludedFromCoreAndPreservedOnWrite(t *testing.T) {
	base := syntheticSnapshot()
	const memorySlot = 130
	off := 0x44 + memorySlot*28
	base.RawMask[4+memorySlot/8] |= 1 << (memorySlot % 8)
	put(base.RawCurve, off+4, 10_451_000)
	put(base.RawCurve, off+8, 940_000)
	put(base.RawTable, tableDelta(memorySlot), 200_000)
	points, err := decodeCurve(base.RawMask, base.RawCurve, base.RawTable)
	if err != nil || !slices.Equal(points, base.Points) {
		t.Fatalf("odczyt rdzenia z końcowym wpisem VRAM: %v", err)
	}
	target, err := plateau(base.Points, 55, base.Points[64].LiveKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := prepareCurveWrite(base, base, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if maskBit(packet[4:36], memorySlot) || i32(packet, tableDelta(memorySlot)) != 200_000 {
		t.Fatal("wpis pamięci został objęty zapisem lub zmodyfikowany")
	}
	live := base
	live.RawTable = slices.Clone(base.RawTable)
	put(live.RawTable, tableDelta(memorySlot), 215_000)
	if _, err = prepareCurveWrite(base, live, target, false); err == nil {
		t.Fatal("zaakceptowano zmianę offsetu pamięci")
	}
}

func TestDecodeCurveRejectsAmbiguousAdaMemoryEntries(t *testing.T) {
	for _, name := range []string{"w środku rdzenia", "bez potwierdzonej granicy", "rdzeń po pamięci"} {
		t.Run(name, func(t *testing.T) {
			base := syntheticSnapshot()
			point := 72
			voltage := uint32(940_000)
			switch name {
			case "w środku rdzenia":
				point = 60
			case "bez potwierdzonej granicy":
				voltage = 1_100_000
			case "rdzeń po pamięci":
				base.RawMask[4+73/8] |= 1 << (73 % 8)
				put(base.RawCurve, 0x44+73*28+4, 2_800_000)
				put(base.RawCurve, 0x44+73*28+8, 1_100_000)
			}
			base.RawMask[4+point/8] |= 1 << uint(point%8)
			put(base.RawCurve, 0x44+point*28+4, 10_451_000)
			put(base.RawCurve, 0x44+point*28+8, voltage)
			if _, err := decodeCurve(base.RawMask, base.RawCurve, base.RawTable); err == nil {
				t.Fatal("zaakceptowano niejednoznaczny punkt")
			}
		})
	}
}

func simulatedDriverSnapshot(base Snapshot, payload []byte, goal []Point) Snapshot {
	next := base
	next.RawCurve, next.RawTable = slices.Clone(base.RawCurve), slices.Clone(base.RawTable)
	for _, point := range goal {
		if !maskBit(payload[4:36], point.Slot) {
			continue
		}
		delta := u32(payload, tableDelta(point.Slot))
		put(next.RawTable, tableDelta(point.Slot), delta)
		put(next.RawCurve, 0x44+point.Slot*28+4, uint32(point.LiveKHz))
	}
	next.Points, _ = decodeCurve(next.RawMask, next.RawCurve, next.RawTable)
	return next
}

func TestSimulatedWriteReadbackAndOriginalRestore(t *testing.T) {
	base := syntheticSnapshot()
	target, err := plateau(base.Points, 55, base.Points[64].LiveKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := prepareCurveWrite(base, base, target, false)
	if err != nil || len(payload) != tableSize {
		t.Fatalf("prepare write: len=%d err=%v", len(payload), err)
	}
	expected := slices.Clone(base.RawTable)
	clear(expected[4:36])
	for i, p := range target {
		if p.DeltaKHz != base.Points[i].DeltaKHz {
			expected[4+p.Slot/8] |= 1 << uint(p.Slot%8)
			put(expected, tableDelta(p.Slot), uint32(int32(p.DeltaKHz)))
		}
	}
	if !bytes.Equal(expected, payload) {
		t.Fatal("write packet touched bytes outside the selected core points and mask")
	}
	if err := validateSavedCurve(base); err != nil {
		t.Fatal("write packet modified the original snapshot:", err)
	}
	applied := simulatedDriverSnapshot(base, payload, target)
	if err = verifyCurveResult(base, base, applied, target); err != nil {
		t.Fatal(err)
	}
	payload, err = prepareCurveWrite(base, applied, base.Points, true)
	if err != nil || len(payload) != tableSize {
		t.Fatalf("prepare restore: len=%d err=%v", len(payload), err)
	}
	restored := simulatedDriverSnapshot(applied, payload, base.Points)
	if !exactOriginal(base, restored) || !bytes.Equal(base.RawTable, restored.RawTable) {
		t.Fatal("restoration did not reproduce the exact original table")
	}
	if err = verifyCurveResult(base, applied, restored, base.Points); err != nil {
		t.Fatal(err)
	}
}

func TestWriteGateRejectsChangedIdentityOrProtectedSettings(t *testing.T) {
	base := syntheticSnapshot()
	target, err := plateau(base.Points, 55, base.Points[64].LiveKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"other GPU", func(s *Snapshot) { s.GPU.UUID = "GPU-different" }},
		{"new driver", func(s *Snapshot) { s.GPU.Driver++ }},
		{"changed firmware identity", func(s *Snapshot) { s.GPU.VBIOS = "different" }},
		{"increased power limit", func(s *Snapshot) { s.PowerMW++ }},
		{"changed thermal limit", func(s *Snapshot) { s.ThermalPolicies = []uint32{1, 90} }},
		{"changed voltage boost", func(s *Snapshot) { v := 5.0; s.VoltageBoostPercent = &v }},
		{"changed VRAM offset", func(s *Snapshot) { s.MemoryDeltas = maps.Clone(s.MemoryDeltas); s.MemoryDeltas[0]++ }},
		{"corrupted non-core offset", func(s *Snapshot) {
			s.RawTable = slices.Clone(s.RawTable)
			put(s.RawTable, tableDelta(128), 90000)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := base
			tc.change(&live)
			if payload, err := prepareCurveWrite(base, live, target, false); err == nil || payload != nil {
				t.Fatalf("unsafe state produced an experimental packet: %v", err)
			}
			if payload, err := prepareCurveWrite(base, live, base.Points, true); err == nil || payload != nil {
				t.Fatalf("unsafe state produced a direct restoration packet: %v", err)
			}
		})
	}
}

func TestCorruptedOriginalAndNonOriginalRollbackAreRejected(t *testing.T) {
	base := syntheticSnapshot()
	changed := base
	changed.Points = slices.Clone(base.Points)
	changed.Points[61].DeltaKHz++
	if payload, err := prepareCurveWrite(changed, base, changed.Points, true); err == nil || payload != nil {
		t.Fatalf("accepted original point that did not match the disk snapshot: %v", err)
	}
	if payload, err := prepareCurveWrite(base, base, changed.Points, true); err == nil || payload != nil {
		t.Fatalf("accepted rollback to an experimental curve: %v", err)
	}
	if payload, err := prepareCurveWrite(base, base, base.Points, true); err != nil || payload != nil {
		t.Fatalf("unnecessary write requested for already restored settings: %v", err)
	}
}

func TestReadbackFailureDetectsPartialWritesAndCollateralChange(t *testing.T) {
	base := syntheticSnapshot()
	target, err := plateau(base.Points, 55, base.Points[64].LiveKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil {
		t.Fatal(err)
	}
	if verifyCurveResult(base, base, base, target) == nil {
		t.Fatal("unchanged driver readback was accepted for an applied curve")
	}
	payload, err := prepareCurveWrite(base, base, target, false)
	if err != nil {
		t.Fatal(err)
	}
	applied := simulatedDriverSnapshot(base, payload, target)
	applied.ThermalPolicies = []uint32{1, 90}
	if verifyCurveResult(base, base, applied, target) == nil {
		t.Fatal("side effect on protected settings was missed")
	}
}

func FuzzCurveDecoderRejectsMalformedDriverBuffers(f *testing.F) {
	base := syntheticSnapshot()
	f.Add(base.RawMask, base.RawCurve, base.RawTable)
	f.Add([]byte{}, []byte{}, []byte{})
	f.Fuzz(func(t *testing.T, mask, curve, table []byte) {
		points, err := decodeCurve(mask, curve, table)
		if err == nil {
			for i, p := range points {
				if p.Slot < 0 || p.Slot >= 255 || (i > 0 && p.UV <= points[i-1].UV) {
					t.Fatalf("decoder allowed an unsafe slot or voltage ordering: %+v", p)
				}
			}
		}
	})
}

func TestThermalGateRejectsOverheatingAndMissingCoreSensor(t *testing.T) {
	core, hot := 65.0, 85.0
	sample := Sample{TempC: &core, HotSpotC: &hot}
	if err := checkThermalStop(sample); err != nil {
		t.Fatal(err)
	}
	hot = 105
	if checkThermalStop(sample) == nil {
		t.Fatal("accepted an overheated hotspot")
	}
	hot, core = 85, 87
	if checkThermalStop(sample) == nil {
		t.Fatal("accepted an overheated core")
	}
	if checkThermalStop(Sample{}) == nil {
		t.Fatal("accepted a missing core temperature")
	}
}
