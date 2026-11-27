package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func msiProfilePath(msi MSIBackup, base Snapshot) (string, error) {
	device := base.GPU.Device >> 16
	if base.GPU.Device&0xffff != 0x10de || device == 0 {
		return "", errors.New("nie znaleziono identyfikatora NVIDIA w zrzucie profilu")
	}
	prefix := fmt.Sprintf("VEN_10DE&DEV_%04X&SUBSYS_%08X&", device, base.GPU.Board)
	bus := fmt.Sprintf("&BUS_%d&", base.GPU.Bus)
	var profilePath string
	for path := range msi.Hashes {
		name := strings.ToUpper(filepath.Base(path))
		if !strings.EqualFold(filepath.Dir(path), "Profiles") || !strings.HasPrefix(name, prefix) ||
			!strings.Contains(name, bus) || !strings.HasSuffix(name, ".CFG") {
			continue
		}
		if profilePath != "" {
			return "", errors.New("więcej niż jeden plik MSI pasuje do karty")
		}
		profilePath = filepath.Join(msi.Folder, path)
	}
	if profilePath == "" {
		return "", errors.New("nie znaleziono pliku profili MSI tej karty i magistrali PCI")
	}
	return profilePath, nil
}

func verifyOriginalMSISlot(msi MSIBackup, base Snapshot, slot int) error {
	profilePath, err := msiProfilePath(msi, base)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(profilePath)
	if err != nil {
		return err
	}
	sections, err := msiINI(raw)
	if err != nil {
		return err
	}
	if err = verifyMSIOriginalProfile(sections["startup"], sections[msiSlotKey(slot)], base); err != nil {
		return fmt.Errorf("slot MSI %d: %w", slot, err)
	}
	return nil
}
