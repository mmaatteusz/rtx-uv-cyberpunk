package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"slices"
)

const (
	getMask   = 0x507B4B59
	getVF     = 0x21537AD4
	getTable  = 0x23F1B133
	setTable  = 0x0733E009
	maskSize  = 0x182C
	curveSize = 0x1C28
	tableSize = 0x2420
)

func u32(b []byte, i int) uint32    { return binary.LittleEndian.Uint32(b[i:]) }
func i32(b []byte, i int) int       { return int(int32(u32(b, i))) }
func put(b []byte, i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }
func tableDelta(slot int) int       { return 0x58 + slot*36 }
func version(size, ver int) uint32  { return uint32(size | ver<<16) }
func maskBit(mask []byte, slot int) bool {
	return mask[slot/8]&(byte(1)<<uint(slot%8)) != 0
}

func decodeCurve(mask, curve, table []byte) ([]Point, error) {
	if len(mask) != maskSize || len(curve) != curveSize || len(table) != tableSize {
		return nil, errors.New("nieobsługiwana wersja struktur krzywej")
	}
	if u32(mask, 0) != version(maskSize, 1) || u32(curve, 0) != version(curveSize, 1) || u32(table, 0) != version(tableSize, 1) || maskBit(mask[4:36], 255) {
		return nil, errors.New("nieobsługiwana wersja lub maska tabeli sterownika")
	}
	points := make([]Point, 0, 128)
	ended := false
	var emptyStatus [28]byte
	for i := 0; i < 255; i++ {
		off := 0x44 + i*28
		kind, khz, uv := u32(curve, off), i32(curve, off+4), i32(curve, off+8)
		if !maskBit(mask[4:36], i) || kind != 0 {
			if len(points) > 0 {
				ended = true
			}
			continue
		}
		delta := i32(table, tableDelta(i))
		if len(points) > 0 && delta == 0 && bytes.Equal(curve[off:off+28], emptyStatus[:]) {
			// A requested slot can be empty at the end of the status table.
			// A populated core point after it still makes the read ambiguous.
			ended = true
			continue
		}
		if len(points) >= 40 && points[len(points)-1].UV >= 900000 &&
			khz >= 8000000 && khz <= 14000000 && uv >= 300000 && uv <= 1300000 &&
			absInt(delta) <= 800000 && (ended || uv <= points[len(points)-1].UV) {
			// RTX 4070 SUPER drivers can report trailing GDDR6X entries with
			// the same type ID as the core. Only accept them past the core
			// range or when voltage restarts. Hold their raw offsets intact.
			ended = true
			continue
		}
		if uv < 300000 || uv > 1300000 || khz < 100000 || khz > 3600000 {
			return nil, fmt.Errorf("niepoprawny aktywny punkt rdzenia NVAPI nr %d (napięcie %d µV, zegar %d kHz, offset %d kHz)", i, uv, khz, delta)
		}
		if ended || (len(points) > 0 && uv <= points[len(points)-1].UV) {
			return nil, fmt.Errorf("niejednoznaczny układ krzywej NVAPI przy punkcie nr %d (napięcie %d µV, zegar %d kHz)", i, uv, khz)
		}
		if absInt(delta) > 800000 {
			return nil, errors.New("offset w strukturze V/F poza zakresem")
		}
		points = append(points, Point{i, uv, khz, delta})
	}
	if len(points) < 40 || points[len(points)-1].UV < 900000 {
		return nil, errors.New("sterownik nie udostępnił kompletnej krzywej rdzenia Ada")
	}
	return points, nil
}

func (s Snapshot) compatibleWith(t Snapshot) bool {
	if s.GPU != t.GPU || len(s.Points) != len(t.Points) || len(s.Points) < 40 {
		return false
	}
	if len(s.RawMask) != maskSize || len(t.RawMask) != maskSize || len(s.RawTable) != tableSize || len(t.RawTable) != tableSize ||
		!slices.Equal(s.RawMask[4:36], t.RawMask[4:36]) {
		return false
	}
	for i, p := range s.Points {
		if p.Slot != t.Points[i].Slot || p.UV != t.Points[i].UV {
			return false
		}
	}
	return true
}

func equalDeltas(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i, p := range a {
		if p.Slot != b[i].Slot || p.UV != b[i].UV || p.DeltaKHz != b[i].DeltaKHz {
			return false
		}
	}
	return true
}

func equalMap(a, b map[uint32]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || v != w {
			return false
		}
	}
	return true
}

func (s Snapshot) protectedEqual(t Snapshot) bool {
	voltageOK := s.VoltageBoostPercent == nil && t.VoltageBoostPercent == nil
	if s.VoltageBoostPercent != nil && t.VoltageBoostPercent != nil {
		voltageOK = *s.VoltageBoostPercent == *t.VoltageBoostPercent
	}
	return voltageOK && s.PowerMW == t.PowerMW && s.FactoryPowerMW == t.FactoryPowerMW &&
		s.MinPowerMW == t.MinPowerMW && s.MaxPowerMW == t.MaxPowerMW && s.MinDeltaKHz == t.MinDeltaKHz &&
		s.MaxDeltaKHz == t.MaxDeltaKHz && s.FactoryMemoryKHz == t.FactoryMemoryKHz && slices.Equal(s.ThermalPolicies, t.ThermalPolicies) &&
		equalMap(s.MemoryDeltas, t.MemoryDeltas) && equalMap(s.GraphicsDeltas, t.GraphicsDeltas) &&
		maps.Equal(s.MemoryLimits, t.MemoryLimits)
}

func preservedTable(a, b Snapshot) bool {
	if len(a.RawTable) != tableSize || len(b.RawTable) != tableSize {
		return false
	}
	core := map[int]bool{}
	for _, p := range a.Points {
		core[p.Slot] = true
	}
	for i := 0; i < 255; i++ {
		if !core[i] && i32(a.RawTable, tableDelta(i)) != i32(b.RawTable, tableDelta(i)) {
			return false
		}
	}
	return true
}

func validateSavedCurve(saved Snapshot) error {
	points, err := decodeCurve(saved.RawMask, saved.RawCurve, saved.RawTable)
	if err != nil {
		return err
	}
	if !slices.Equal(points, saved.Points) {
		return errors.New("lista punktów oryginału różni się od kopii tabeli sterownika")
	}
	return nil
}

func prepareCurveWrite(base, live Snapshot, target []Point, restore bool) ([]byte, error) {
	if !base.compatibleWith(live) || len(target) != len(base.Points) {
		return nil, errors.New("zmieniła się karta, sterownik lub geometria krzywej V/F")
	}
	if !base.protectedEqual(live) || !preservedTable(base, live) {
		return nil, errors.New("wykryto zmianę ustawień chronionych przez inny program")
	}
	if err := validateSavedCurve(base); err != nil {
		return nil, err
	}
	if restore {
		if !equalDeltas(base.Points, target) {
			return nil, errors.New("odtwarzanie wymaga dokładnej kopii oryginalnych offsetów")
		}
	} else if err := validateClockEnvelope(base.Points, target, base.MinDeltaKHz, base.MaxDeltaKHz); err != nil {
		return nil, err
	}
	if equalDeltas(live.Points, target) {
		return nil, nil
	}
	payload := slices.Clone(live.RawTable)
	put(payload, 0, version(tableSize, 1))
	clear(payload[4:36])
	for i, p := range target {
		if live.Points[i].DeltaKHz == p.DeltaKHz {
			continue
		}
		if p.Slot <= 0 || p.Slot >= 255 || !maskBit(live.RawMask[4:36], p.Slot) {
			return nil, errors.New("punkt poza oryginalną maską sterownika")
		}
		payload[4+p.Slot/8] |= byte(1) << uint(p.Slot%8)
		put(payload, tableDelta(p.Slot), uint32(int32(p.DeltaKHz)))
	}
	return payload, nil
}

func verifyCurveResult(base, live, applied Snapshot, target []Point) error {
	if !base.compatibleWith(applied) || !equalDeltas(applied.Points, target) || !preservedTable(live, applied) {
		return errors.New("odczyt po zapisie V/F nie zgadza się z profilem")
	}
	if !base.protectedEqual(applied) {
		return errors.New("ustawienia chronione zmieniły się podczas zapisu")
	}
	return nil
}

func exactOriginal(base, current Snapshot) bool {
	return base.compatibleWith(current) && base.protectedEqual(current) &&
		equalDeltas(base.Points, current.Points) && preservedTable(base, current)
}
