package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
)

func msiWinningFields(source *msiSection, base Snapshot, target []Point) (map[string]string, error) {
	if source == nil || source.fields["format"] != "2" {
		return nil, errors.New("import wymaga oryginalnego profilu MSI w formacie 2")
	}
	if err := validateClockEnvelope(base.Points, target, base.MinDeltaKHz, base.MaxDeltaKHz); err != nil {
		return nil, err
	}
	data, positions, err := mapMSICurve(source.fields["vfcurve"], base.Points)
	if err != nil {
		return nil, err
	}
	changed := false
	for i, old := range base.Points {
		diff := target[i].DeltaKHz - old.DeltaKHz
		if diff == 0 {
			continue
		}
		changed = true
		offset := data[positions[i]+8:]
		oldOffset := math.Float32frombits(binary.LittleEndian.Uint32(offset))
		value := float64(oldOffset) + float64(diff)/1000
		binary.LittleEndian.PutUint32(offset, math.Float32bits(float32(value)))
	}
	if !changed {
		return nil, errors.New("krzywa UV nie różni się od oryginalnej")
	}
	curve := strings.ToUpper(hex.EncodeToString(data)) + "h"
	if err := verifyMSICurve(curve, target); err != nil {
		return nil, err
	}
	fields := maps.Clone(source.fields)
	// The MSI format uses a special CoreClkBoost value for custom curves.
	fields["coreclkboost"] = "1000000"
	fields["vfcurve"] = curve
	return fields, nil
}

func msiSlotEmpty(section *msiSection) bool {
	if section == nil {
		return true
	}
	for field, value := range section.fields {
		switch field {
		case "format", "powerlimit", "coreclkboost", "vfcurve", "memclkboost", "fanmode", "fanspeed", "fanmode2", "fanspeed2":
			if value == "" {
				continue
			}
		}
		return false
	}
	return true
}

func freeMSISlot(sections map[string]*msiSection, origin int, fields map[string]string) (int, error) {
	free := 0
	for distance := 1; distance < 5; distance++ {
		slot := (origin-1+distance)%5 + 1
		section := sections[msiSlotKey(slot)]
		if section != nil && maps.Equal(section.fields, fields) {
			return 0, fmt.Errorf("ta krzywa jest już zapisana w slocie MSI %d", slot)
		}
		if free == 0 && msiSlotEmpty(section) {
			free = slot
		}
	}
	if free == 0 {
		return 0, errors.New("brak wolnego slotu MSI")
	}
	return free, nil
}

func msiSectionBounds(lines [][]byte, name string) (first, last int) {
	first = -1
	for i, line := range lines {
		label := strings.TrimSpace(strings.TrimPrefix(string(line), "\uFEFF"))
		if !strings.HasPrefix(label, "[") {
			continue
		}
		if first >= 0 {
			return first, i
		}
		if strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(label[1:], "]")), name) {
			first = i
		}
	}
	return first, len(lines)
}

// Reuse the original section's spelling and field ordering. All bytes outside
// the destination slot survive, including defaults, startup, and other slots.
func insertMSIProfile(raw []byte, original, slot int, fields map[string]string) ([]byte, error) {
	if original < 1 || original > 5 || slot < 1 || slot > 5 || original == slot {
		return nil, errors.New("slot docelowy musi być inny od oryginalnego")
	}
	sections, err := msiINI(raw)
	if err != nil {
		return nil, err
	}
	dst := msiSlotKey(slot)
	if sections[msiSlotKey(original)] == nil || !msiSlotEmpty(sections[dst]) {
		return nil, errors.New("brak slotu oryginalnego lub slot docelowy jest zajęty")
	}
	nl := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		nl = "\r\n"
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	src, end := msiSectionBounds(lines, msiSlotKey(original))
	if src < 0 {
		return nil, errors.New("nie można odczytać tekstu oryginalnego slotu")
	}
	var entry strings.Builder
	fmt.Fprintf(&entry, "[Profile%d]%s", slot, nl)
	for _, line := range lines[src+1 : end] {
		key, _, ok := strings.Cut(strings.TrimSpace(string(line)), "=")
		key = strings.TrimSpace(key)
		value, known := fields[strings.ToLower(key)]
		if ok && known {
			fmt.Fprintf(&entry, "%s=%s%s", key, value, nl)
		}
	}
	if _, ok := sections[msiSlotKey(original)].fields["coreclkboost"]; !ok {
		fmt.Fprintf(&entry, "CoreClkBoost=%s%s", fields["coreclkboost"], nl)
	}
	first, last := msiSectionBounds(lines, dst)
	var output []byte
	if first < 0 {
		output = bytes.Clone(raw)
		if !bytes.HasSuffix(output, []byte("\n")) {
			output = append(output, nl...)
		}
		output = append(output, entry.String()...)
	} else {
		// The destination placeholder is empty. Retain its comments and blank lines.
		output = bytes.Join(lines[:first], nil)
		if first == 0 && bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
			output = append(output, "\xef\xbb\xbf"...)
		}
		output = append(output, entry.String()...)
		for _, line := range lines[first+1 : last] {
			value := strings.TrimSpace(string(line))
			if value == "" || strings.HasPrefix(value, ";") || strings.HasPrefix(value, "#") {
				output = append(output, line...)
			}
		}
		output = append(output, bytes.Join(lines[last:], nil)...)
	}
	check, err := msiINI(output)
	if err != nil || check[dst] == nil || !maps.Equal(check[dst].fields, fields) {
		return nil, errors.New("nie można zweryfikować nowej sekcji MSI")
	}
	for name, section := range sections {
		if name != dst && (check[name] == nil || !maps.Equal(section.fields, check[name].fields)) {
			return nil, errors.New("import zmieniłby istniejący wpis MSI")
		}
	}
	return output, nil
}

func planMSIImport(current, archived []byte, base Snapshot, target []Point, original int) (int, []byte, error) {
	if original < 1 || original > 5 {
		return 0, nil, errors.New("nieprawidłowy slot oryginalnego OC")
	}
	before, err := msiINI(archived)
	if err != nil {
		return 0, nil, err
	}
	now, err := msiINI(current)
	if err != nil {
		return 0, nil, err
	}
	key := msiSlotKey(original)
	for _, name := range []string{key, "startup"} {
		if before[name] == nil || now[name] == nil || !maps.Equal(before[name].fields, now[name].fields) {
			return 0, nil, fmt.Errorf("sekcja MSI [%s] różni się od kopii oryginału", name)
		}
	}
	if err := compareMSISettings(now["startup"], now[key], base); err != nil {
		return 0, nil, err
	}
	fields, err := msiWinningFields(now[key], base, target)
	if err != nil {
		return 0, nil, err
	}
	slot, err := freeMSISlot(now, original, fields)
	if err != nil {
		return 0, nil, err
	}
	output, err := insertMSIProfile(current, original, slot, fields)
	return slot, output, err
}
