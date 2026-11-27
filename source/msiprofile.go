package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type msiSection struct {
	fields map[string]string
}

func msiINI(raw []byte) (map[string]*msiSection, error) {
	if len(raw) == 0 || len(raw) > 2<<20 || bytes.ContainsRune(raw, 0) {
		return nil, errors.New("nieobsługiwane kodowanie pliku profili MSI")
	}
	result := map[string]*msiSection{}
	var section *msiSection
	body := strings.TrimPrefix(string(raw), "\uFEFF")
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		value := strings.TrimSpace(line)
		if strings.HasPrefix(value, "[") {
			if !strings.HasSuffix(value, "]") {
				return nil, errors.New("nieznana składnia sekcji MSI")
			}
			key := strings.ToLower(strings.TrimSpace(value[1 : len(value)-1]))
			if key == "" || result[key] != nil {
				return nil, errors.New("powtarzająca się lub pusta nazwa sekcji MSI")
			}
			section = &msiSection{fields: map[string]string{}}
			result[key] = section
			continue
		}
		if section == nil || value == "" || strings.HasPrefix(value, ";") || strings.HasPrefix(value, "#") {
			continue
		}
		key, v, ok := strings.Cut(value, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !ok || key == "" {
			return nil, errors.New("nieznana składnia wpisu MSI")
		}
		if _, ok := section.fields[key]; ok {
			return nil, errors.New("powtarzający się klucz MSI: " + key)
		}
		section.fields[key] = strings.TrimSpace(v)
	}
	return result, nil
}

func msiSlotKey(slot int) string { return "profile" + strconv.Itoa(slot) }

func mapMSICurve(value string, original []Point) ([]byte, []int, error) {
	body := strings.TrimSpace(value)
	if body == "" {
		return nil, nil, errors.New("slot MSI nie zawiera krzywej V/F")
	}
	if end := body[len(body)-1]; end == 'h' || end == 'H' {
		body = body[:len(body)-1]
	}
	data, err := hex.DecodeString(body)
	if err != nil || len(data) < 12 {
		return nil, nil, errors.New("zapisana krzywa MSI jest nieczytelna")
	}
	u32 := func(i int) uint32 { return binary.LittleEndian.Uint32(data[i:]) }
	readFloat := func(i int) float64 { return float64(math.Float32frombits(u32(i))) }
	count := int(u32(4))
	if u32(0) != 0x00020000 || u32(8) != 0 || count < 20 || count > 127 || len(data) < 12+count*12 {
		return nil, nil, errors.New("nieobsługiwana wersja lub rozmiar krzywej MSI")
	}
	for _, v := range data[12+count*12:] {
		if v != 0 {
			return nil, nil, errors.New("nieznane dane na końcu krzywej MSI")
		}
	}
	matched := map[int]bool{}
	positions := make([]int, len(original))
	var differences []float64
	lastUV := 0
	for i := 0; i < count; i++ {
		pos := 12 + 12*i
		volts, freq, offset := readFloat(pos), readFloat(pos+4), readFloat(pos+8)
		if math.IsNaN(volts+freq+offset) || math.IsInf(volts+freq+offset, 0) {
			return nil, nil, errors.New("nieskończona wartość w krzywej MSI")
		}
		uv := int(math.Round(volts * 1000))
		if uv < 400000 || uv > 1300000 || uv <= lastUV {
			return nil, nil, errors.New("niespójna siatka napięcia MSI")
		}
		lastUV = uv
		if freq == 225 {
			continue
		}
		if freq < 600 || freq > 3650 || offset < -600 || offset > 1150 {
			return nil, nil, errors.New("nieznana częstotliwość lub offset w krzywej MSI")
		}
		match := -1
		mismatch := 75 // microvolts, approximately float32 rounding plus static table resolution
		for j, p := range original {
			if delta := absInt(p.UV - uv); delta < mismatch {
				match, mismatch = j, delta
			}
		}
		if match < 0 {
			return nil, nil, fmt.Errorf("punkt %.3f mV MSI nie pasuje do punktu NVAPI", volts)
		}
		if matched[match] {
			return nil, nil, errors.New("dwa punkty MSI wskazują ten sam punkt GPU")
		}
		matched[match] = true
		positions[match] = pos
		differences = append(differences, (freq+offset)*1000-float64(original[match].LiveKHz))
	}
	if len(matched) < 20 || len(matched) != len(original) {
		return nil, nil, errors.New("slot MSI nie pokrywa wszystkich odczytanych punktów rdzenia")
	}
	drift := median(differences)
	if math.Abs(drift) > 45000 {
		return nil, nil, errors.New("slot MSI ma inny zegar niż oryginalne OC odczytane z karty")
	}
	for _, d := range differences {
		if math.Abs(d-drift) > 16000 {
			return nil, nil, errors.New("slot MSI nie odwzorowuje kształtu oryginalnego OC")
		}
	}
	return data, positions, nil
}

func verifyMSICurve(value string, original []Point) error {
	_, _, err := mapMSICurve(value, original)
	return err
}

func compareMSISettings(start, slot *msiSection, base Snapshot) error {
	if start == nil || slot == nil {
		return errors.New("brak zapisanej sekcji Startup lub oryginalnego slotu MSI")
	}
	// Validate the selected slot's power limit and any different core boost
	// against the driver. Other protected settings still need matching values.
	compareStoredField := func(field string) bool {
		switch field {
		case "vfcurve", "powerlimit", "coreclkboost":
			return false
		case "fanspeed":
			return start.fields["fanmode"] != "0" || slot.fields["fanmode"] != "0"
		case "fanspeed2":
			return start.fields["fanmode2"] != "0" || slot.fields["fanmode2"] != "0"
		default:
			return true
		}
	}
	for field, value := range slot.fields {
		if compareStoredField(field) && start.fields[field] != value {
			return fmt.Errorf("wartość %s różni się między slotem MSI (%q) a sekcją Startup (%q)", field, value, start.fields[field])
		}
	}
	for field, value := range start.fields {
		if compareStoredField(field) && slot.fields[field] != value {
			return fmt.Errorf("wartość %s różni się między slotem MSI (%q) a sekcją Startup (%q)", field, slot.fields[field], value)
		}
	}
	if saved := slot.fields["coreclkboost"]; saved != start.fields["coreclkboost"] {
		boost, parseErr := strconv.Atoi(saved)
		live, known := base.GraphicsDeltas[0]
		if !known {
			return errors.New("CoreClkBoost slotu różni się od Startup i brakuje odczytu offsetu rdzenia P0 z NVAPI")
		}
		if parseErr != nil || boost != live {
			return fmt.Errorf("CoreClkBoost wybranego slotu (%q kHz) nie potwierdza aktywnego offsetu rdzenia P0 (%d kHz z NVAPI)", saved, live)
		}
	}
	memory, err := strconv.Atoi(slot.fields["memclkboost"])
	delta := base.MemoryDeltas[0]
	if delta == 0 && base.FactoryMemoryKHz > 0 {
		if f := base.MemoryLimits[0][1]; f > 0 {
			delta = f - base.FactoryMemoryKHz
		}
	}
	if err != nil || absInt(memory-delta) > 3000 {
		return errors.New("zapisany slot MSI nie potwierdza bieżącego offsetu VRAM")
	}
	rawPowerLimit := slot.fields["powerlimit"]
	power, err := strconv.Atoi(rawPowerLimit)
	if err != nil || power <= 0 {
		return fmt.Errorf("zapisany slot MSI ma nieprawidłowy Power Limit (%q)", rawPowerLimit)
	}
	if base.PowerMW <= 0 || base.FactoryPowerMW <= 0 {
		return errors.New("brak wiarygodnego odczytu bieżącego lub fabrycznego Power Limit z NVIDIA NVML")
	}
	slotPowerMW := float64(base.FactoryPowerMW) * float64(power) / 100
	if math.Abs(float64(base.PowerMW)-slotPowerMW) > 1000 {
		return fmt.Errorf("Power Limit w slocie MSI to %d%% (%.1f W), a na karcie %.1f W według NVIDIA NVML",
			power, slotPowerMW/1000, float64(base.PowerMW)/1000)
	}
	return nil
}

func verifyMSIOriginalProfile(start, slot *msiSection, base Snapshot) error {
	if err := compareMSISettings(start, slot, base); err != nil {
		return err
	}
	return verifyMSICurve(slot.fields["vfcurve"], base.Points)
}

func equalPointIDs(a, b []Point) bool {
	if len(a) != len(b) {
		return false
	}
	for i, p := range a {
		if p.UV != b[i].UV || p.Slot != b[i].Slot {
			return false
		}
	}
	return true
}
