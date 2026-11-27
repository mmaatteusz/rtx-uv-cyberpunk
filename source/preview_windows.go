package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func previewWinner(ctx context.Context, root, dir string, cfg Config, s Session, live func(Sample), status func(string), armed *bool) (retErr error) {
	lease, err := singleInstance()
	if err != nil {
		return err
	}
	defer closeHandle(lease)
	if !s.Healthy || s.Winner <= 0 || s.OCCeilingMV <= 0 || cfg.Slot < 1 || cfg.Slot > 5 {
		return errors.New("potrzebny jest ukończony wynik UV i oryginalny slot MSI")
	}
	if _, err = os.Stat(filepath.Join(root, recoveryFile)); !os.IsNotExist(err) {
		return errors.New("najpierw zakończ oczekujące odzyskiwanie")
	}
	e := Engine{ctx: ctx, root: root, dir: dir, cfg: cfg, live: live, session: s}
	base, msi, target, err := readWinningCurve(dir, s)
	if err != nil {
		return err
	}
	e.base, e.msi = base, msi
	e.gpu, err = openGPU()
	if err != nil {
		return err
	}
	defer e.gpu.close()
	e.runner.gameSettings = filepath.Join(os.Getenv("LOCALAPPDATA"), "CD Projekt Red", "Cyberpunk 2077", "UserSettings.json")
	e.runner.settingsHash = s.SettingsSHA256
	e.expected = e.base.Points
	if err = e.guard(true); err != nil {
		return fmt.Errorf("podgląd wymaga tego samego oryginalnego OC i konfiguracji MSI: %w", err)
	}
	current, err := e.gpu.snapshot()
	if err != nil || !restoreShape(e.base.Points, current.Points) {
		return errors.New("zmienił się kształt oryginalnej krzywej")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	e.recovery = Recovery{MSIX: cfg.MSI, Slot: cfg.Slot, SessionDir: dir, Before: e.base, MSI: e.msi}
	if err = arm(root, &e.recovery); err != nil {
		return err
	}
	e.armed = true
	*armed = true
	e.watchSince = time.Now()
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, e.recoverIfSafetyFilesChanged())
			return
		}
		if retErr = e.restoreDirect(); retErr != nil {
			return
		}
		if retErr = e.viaMSI(); retErr != nil {
			return
		}
		retErr = disarm(root, e.recovery)
	}()

	if err = e.apply(target); err != nil {
		return err
	}
	status("Tymczasowy profil UV jest aktywny przez maksymalnie 5 minut.")
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Minute)
	defer timer.Stop()
	nextGuard := time.Now().Add(2 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return nil
		case <-ticker.C:
			if err = e.mark(); err != nil {
				return err
			}
			if time.Now().After(nextGuard) {
				if err = e.guard(true); err != nil {
					return err
				}
				nextGuard = time.Now().Add(2 * time.Second)
			}
			s, err := e.gpu.sample(e.armed && !e.recovery.ReadOnly)
			if err != nil {
				return err
			}
			live(s)
			if err = withinOCVoltage(s, e.session.OCCeilingMV); err != nil {
				return err
			}
			if err = checkThermalStop(s); err != nil {
				return err
			}
		}
	}
}
