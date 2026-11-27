package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func makeRecoveryFiles(t *testing.T, mode string, readOnly bool) Recovery {
	t.Helper()
	root := t.TempDir()
	msiDir := filepath.Join(root, "MSI")
	profiles := filepath.Join(msiDir, "Profiles")
	sessionDir := filepath.Join(root, "session")
	for _, p := range []string{profiles, sessionDir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(msiDir, "MSIAfterburner.exe")
	for _, p := range []string{exe, filepath.Join(msiDir, "MSIAfterburner.cfg"), filepath.Join(profiles, "Original.cfg")} {
		if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backup, err := scanMSI(exe)
	if err != nil {
		t.Fatal(err)
	}
	r := Recovery{Format: 1, ReadOnly: readOnly, MSIX: exe, SessionDir: sessionDir, Slot: 1, Before: syntheticSnapshot(), MSI: backup}
	if err := backup.save(filepath.Join(sessionDir, "kopia-MSI-i-VF.zip"), r.Before, recoveryPolicy{mode, r.Slot}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(sessionDir, "sesja.json"), Session{Mode: mode, MSISlot: r.Slot}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecoveryValidatesTheActualSavedArchiveAndSession(t *testing.T) {
	original := makeRecoveryFiles(t, "Optymalizacja", false)
	if err := verifyRecoveryBackup(original); err != nil {
		t.Fatal("valid backup:", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*Recovery)
	}{
		{"different MSI slot", func(r *Recovery) { r.Slot++ }},
		{"different game mode", func(r *Recovery) { r.ReadOnly = true }},
		{"different driver", func(r *Recovery) { r.Before.GPU.Driver++ }},
		{"different MSI installation", func(r *Recovery) { r.MSIX = filepath.Join(r.SessionDir, "MSIAfterburner.exe") }},
		{"different memory amount", func(r *Recovery) { r.Before.FactoryMemoryKHz++ }},
		{"bad curve data", func(r *Recovery) {
			r.Before.Points = slices.Clone(r.Before.Points)
			r.Before.Points[50].DeltaKHz++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			altered := original
			tc.change(&altered)
			if verifyRecoveryBackup(altered) == nil {
				t.Fatal("would have let inconsistent recovery state reach the driver")
			}
		})
	}
	if err := os.Remove(filepath.Join(original.SessionDir, "kopia-MSI-i-VF.zip")); err != nil {
		t.Fatal(err)
	}
	if verifyRecoveryBackup(original) == nil {
		t.Fatal("would have recovered with no original backup")
	}
}

func TestDryRunCannotBecomeWritableDuringRecovery(t *testing.T) {
	r := makeRecoveryFiles(t, "Dry Run", true)
	if err := verifyRecoveryBackup(r); err != nil {
		t.Fatal(err)
	}
	r.ReadOnly = false
	if verifyRecoveryBackup(r) == nil {
		t.Fatal("read-only session got a writable recovery record")
	}
}
