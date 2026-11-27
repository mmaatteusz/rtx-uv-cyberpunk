package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

func confirmOriginal(r Recovery, current Snapshot) error {
	if !exactOriginal(r.Before, current) || !restoreShape(r.Before.Points, current.Points) {
		return errors.New("odczyt krzywej i parametrów nie zgadza się z kopią oryginalnego OC")
	}
	return r.MSI.guard(r.MSIX)
}

func restoreIndependent(root string, waiting bool) error {
	lease, err := singleInstance()
	if err != nil {
		return err
	}
	defer closeHandle(lease)
	var r Recovery
	if err = readJSON(filepath.Join(root, recoveryFile), &r); err != nil {
		return err
	}
	if err = verifyRecoveryBackup(r); err != nil {
		return err
	}
	if r.ReadOnly {
		if err = disarm(root, r); err != nil {
			return err
		}
		recoveryLog(root, "zamknięto dziennik Dry Run; sesja wyłącznie do odczytu")
		return nil
	}
	if waiting {
		time.Sleep(12 * time.Second)
	}
	var last error
	msiTried := false
	for attempt := 0; attempt < 9; attempt++ {
		_ = r.mark()
		done, retry, err := recoverAttempt(root, r, &msiTried)
		if done || !retry {
			return err
		}
		last = err
		if attempt < 8 {
			time.Sleep(4 * time.Second)
		}
	}
	return fmt.Errorf("odzyskiwanie pozostaje aktywne: %w", last)
}

func recoverAttempt(root string, r Recovery, msiTried *bool) (done, retry bool, err error) {
	g, err := openGPU()
	if err != nil {
		return false, true, err
	}
	defer g.close()
	current, err := g.snapshot()
	if err != nil {
		return false, true, err
	}
	if !r.Before.compatibleWith(current) {
		return false, false, errors.New("zmieniła się karta, wersja sterownika, VBIOS lub układ punktów. Wczytaj oryginalny slot MSI i zweryfikuj ustawienia")
	}
	if !*msiTried {
		msi, err := scanMSI(r.MSIX)
		if err != nil {
			return false, true, err
		}
		if !r.MSI.same(msi) {
			return false, false, errors.New("pliki MSI różnią się od kopii. Odzyskiwanie oczekuje weryfikacji oryginalnej konfiguracji")
		}
		err = verifyOriginalMSISlot(r.MSI, r.Before, r.Slot)
		if err == nil {
			err = verifiedMSILoad(r.MSIX, r.Slot, r.MSI)
		}
		*msiTried = err == nil
		if err == nil {
			return false, true, errors.New("wczytano oryginalny slot MSI. Trwa weryfikacja odczytu")
		}
		recoveryLog(root, "wczytanie slotu MSI: "+err.Error())
	}
	if err = confirmOriginal(r, current); err == nil {
		if err = disarm(root, r); err != nil {
			return false, false, err
		}
		recoveryLog(root, "oryginalne offsety V/F, kształt krzywej i ustawienia MSI zweryfikowane")
		return true, false, nil
	}
	if !r.Before.protectedEqual(current) || !preservedTable(r.Before, current) {
		// Keep the recovery gate. Only the verified original MSI slot can
		// re-establish protected settings after a driver reset.
		*msiTried = false
		return false, true, errors.New("chronione ustawienia różnią się od kopii. Zastosuj oryginalny slot MSI i wybierz „Odzyskaj oryginał”")
	}
	// The only backup route writes the exact saved graphics curve through NVAPI.
	// Confirm the matching MSI configuration and protected state afterward.
	if err := r.MSI.guard(r.MSIX); err != nil {
		return false, true, err
	}
	restored, err := g.apply(context.Background(), r.Before, r.Before.Points, true, nil)
	if err != nil {
		return false, true, err
	}
	if err = confirmOriginal(r, restored); err != nil {
		*msiTried = false
		return false, true, err
	}
	if err = disarm(root, r); err != nil {
		return false, false, err
	}
	recoveryLog(root, "przywrócono i sprawdzono wszystkie punkty V/F oraz oryginalną konfigurację MSI")
	return true, false, nil
}
