package main

import (
	"archive/zip"
	"errors"
	"path/filepath"
	"slices"
)

func readWinningCurve(dir string, s Session) (base Snapshot, msi MSIBackup, target []Point, err error) {
	core, err := verifiedWinnerTarget(s)
	if err != nil {
		return base, msi, nil, err
	}
	archive, err := zip.OpenReader(filepath.Join(dir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return base, msi, nil, err
	}
	defer archive.Close()
	if err = readArchiveEntry(archive, "krzywa-oryginalna.json", &base); err != nil {
		return base, msi, nil, err
	}
	if err = validateSavedCurve(base); err != nil {
		return base, msi, nil, err
	}
	if err = readArchiveEntry(archive, "manifest-MSI.json", &msi); err != nil {
		return base, msi, nil, err
	}
	var saved []Point
	if err = readJSON(filepath.Join(dir, "wybrany-profil.json"), &saved); err != nil {
		return base, msi, nil, err
	}
	anchor := slices.IndexFunc(base.Points, func(p Point) bool { return p.UV == s.Winner })
	plan, err := newVoltagePlan(base.Points, core.VoltageMedian, core.CoreMedian)
	if err != nil || plan.targetKHz/1000 != core.TargetMHz {
		return base, msi, nil, errors.New("cel profilu nie zgadza się z pomiarami")
	}
	target, err = plateau(base.Points, anchor, plan.targetKHz, base.MinDeltaKHz, base.MaxDeltaKHz)
	if err != nil || !slices.Equal(target, saved) {
		return base, msi, nil, errors.New("zapisany profil różni się od obliczonej krzywej")
	}
	if err = validateClockEnvelope(base.Points, target, base.MinDeltaKHz, base.MaxDeltaKHz); err != nil {
		return base, msi, nil, err
	}
	return base, msi, target, nil
}
