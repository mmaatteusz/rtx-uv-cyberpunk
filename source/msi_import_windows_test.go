package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsImportTransaction(t *testing.T) {
	if err := msiClosedForImport(); err != nil {
		t.Skip(err)
	}
	base, target, original := msiImportFixture(t)
	slot, output, err := planMSIImport(original, original, base, target, 2)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.cfg")
	plan := importPlan{path, dir, original, output, slot}
	changed := append(bytes.Clone(original), []byte("; changed concurrently\r\n")...)
	if err = os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.commit(); err == nil {
		t.Fatal("replaced changed profile")
	}
	if readback, err := os.ReadFile(path); err != nil || !bytes.Equal(readback, changed) {
		t.Fatalf("modified unexpected profile: %v", err)
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	backup, err := plan.commit()
	if err != nil {
		t.Fatal(err)
	}
	if saved, err := os.ReadFile(backup); err != nil || !bytes.Equal(saved, original) {
		t.Fatalf("wrong original backup: %v", err)
	}
	if readback, err := os.ReadFile(path); err != nil || !bytes.Equal(readback, output) {
		t.Fatalf("wrong imported profile: %v", err)
	}
}
