package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const maxMSIProfileBytes = 2 << 20

func msiClosedForImport() error {
	running, err := processExists("MSIAfterburner.exe")
	if err != nil {
		return err
	}
	if running {
		return errors.New("zakończ MSI Afterburner także w zasobniku i ponów zapis. Przez ten czas pozostaw grę zamkniętą")
	}
	running, err = processExists("Cyberpunk2077.exe")
	if err == nil && running {
		return errors.New("zamknij Cyberpunka przed zapisem slotu MSI")
	}
	return err
}

func readMSIProfile(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxMSIProfileBytes+1))
	if err == nil && len(raw) > maxMSIProfileBytes {
		err = errors.New("plik profilu MSI przekracza obsługiwany rozmiar")
	}
	return raw, err
}

func archivedMSIProfile(dir, name string) ([]byte, error) {
	z, err := zip.OpenReader(filepath.Join(dir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	f, err := z.Open("MSI-Afterburner/" + filepath.ToSlash(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readMSIProfile(f)
}

type importPlan struct {
	path, sessionDir string
	original, output []byte
	slot             int
}

func prepareMSIImport(root, dir, msiPath string, s Session) (importPlan, error) {
	var plan importPlan
	if _, err := os.Lstat(filepath.Join(root, recoveryFile)); !os.IsNotExist(err) {
		return plan, errors.New("przed zapisem zakończ oczekujące odzyskiwanie oryginalnego OC")
	}
	if err := msiClosedForImport(); err != nil {
		return plan, err
	}
	base, before, target, err := readWinningCurve(dir, s)
	if err != nil {
		return plan, err
	}
	if !strings.EqualFold(filepath.Clean(msiPath), filepath.Join(before.Folder, "MSIAfterburner.exe")) {
		return plan, errors.New("wskaż tę samą instalację MSI, której użyto w benchmarku")
	}
	digest, err := hashPath(msiPath)
	if err != nil {
		return plan, err
	}
	if before.EXEHash != digest {
		return plan, errors.New("wersja MSI zmieniła się od zakończenia testów")
	}
	path, err := msiProfilePath(before, base)
	if err != nil {
		return plan, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return plan, err
	}
	if !strings.EqualFold(filepath.Clean(real), filepath.Clean(path)) {
		return plan, errors.New("import wymaga zwykłego pliku MSI bez przekierowań")
	}
	f, err := os.Open(path)
	if err != nil {
		return plan, err
	}
	defer f.Close()
	original, err := readMSIProfile(f)
	if err != nil {
		return plan, err
	}
	name, err := filepath.Rel(before.Folder, path)
	if err != nil {
		return plan, err
	}
	saved, err := archivedMSIProfile(dir, name)
	if err != nil {
		return plan, err
	}
	if digestBytes(saved) != before.Hashes[name] {
		return plan, errors.New("plik profilu MSI w kopii ma niezgodną sumę")
	}
	slot, output, err := planMSIImport(original, saved, base, target, s.MSISlot)
	if err != nil {
		return plan, err
	}
	return importPlan{path, dir, original, output, slot}, nil
}

// The original stays open without write sharing until replacement. The second
// check prevents committing over an edit made after confirmation.
func (plan importPlan) commit() (string, error) {
	if err := msiClosedForImport(); err != nil {
		return "", err
	}
	path, err := syscall.UTF16PtrFromString(plan.path)
	if err != nil {
		return "", err
	}
	h, err := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return "", err
	}
	locked := os.NewFile(uintptr(h), plan.path)
	defer locked.Close()
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &info); err != nil {
		return "", err
	}
	if info.FileAttributes&(syscall.FILE_ATTRIBUTE_REPARSE_POINT|syscall.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		return "", errors.New("plik profilu MSI ma dowiązanie lub przekierowanie")
	}
	current, err := readMSIProfile(locked)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(current, plan.original) {
		return "", errors.New("MSI zmienił plik po sprawdzeniu wolnego slotu. Ponów wybór")
	}
	backup, err := writeImportFile(plan.sessionDir, "MSI-przed-importem-*.cfg", current)
	if err != nil {
		return "", fmt.Errorf("nie zapisano kopii profili MSI: %w", err)
	}
	temp, err := writeImportFile(filepath.Dir(plan.path), ".RTX-UV-*.tmp", plan.output)
	if err != nil {
		return backup, fmt.Errorf("nie można zapisać pliku obok profili MSI. Sprawdź uprawnienia do tego katalogu: %w", err)
	}
	defer os.Remove(temp)
	if err := msiClosedForImport(); err != nil {
		return backup, err
	}
	openInfo, err := locked.Stat()
	if err != nil {
		return backup, err
	}
	namedInfo, err := os.Lstat(plan.path)
	if err != nil || !os.SameFile(openInfo, namedInfo) {
		return backup, errors.New("plik MSI zmienił się przed zapisem")
	}
	replacement, err := syscall.UTF16PtrFromString(temp)
	if err != nil {
		return backup, err
	}
	// Keep the original directory entry during Windows' replace operation as
	// well as the durable session copy. Some partial failures rename the old file.
	localDir, err := os.MkdirTemp(filepath.Dir(plan.path), ".RTX-UV-original-*")
	if err != nil {
		return backup, err
	}
	defer os.Remove(localDir) // Keep the directory if an unverified backup still exists.
	localBackup := filepath.Join(localDir, "original.cfg")
	originalName, err := syscall.UTF16PtrFromString(localBackup)
	if err != nil {
		return backup, err
	}
	ok, _, replaceErr := kernel.NewProc("ReplaceFileW").Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(replacement)), uintptr(unsafe.Pointer(originalName)), 0, 0, 0)
	if ok == 0 {
		if _, statErr := os.Lstat(plan.path); os.IsNotExist(statErr) {
			former, readErr := os.ReadFile(localBackup)
			if readErr == nil && bytes.Equal(former, plan.original) {
				// No replacement flag: never overwrite a newly created path.
				moved, _, moveErr := moveFile.Call(uintptr(unsafe.Pointer(originalName)), uintptr(unsafe.Pointer(path)), 0x8)
				if moved == 0 {
					return backup, fmt.Errorf("nie udało się odtworzyć nazwy pliku. Oryginał zachowano w %s: %w", localBackup, moveErr)
				}
			} else {
				return backup, fmt.Errorf("po błędzie Windows plik profilu jest niedostępny: %w", replaceErr)
			}
		}
		if old, e := os.ReadFile(plan.path); e == nil && bytes.Equal(old, plan.original) {
			_ = os.Remove(localBackup)
		}
		return backup, fmt.Errorf("Windows zgłosił błąd zapisu slotu MSI %d: %w", plan.slot, replaceErr)
	}
	readback, err := os.ReadFile(plan.path)
	if err != nil || !bytes.Equal(readback, plan.output) {
		return backup, fmt.Errorf("nie potwierdzono zawartości pliku po zapisie. Kopia przy pliku MSI ma nazwę %s", localBackup)
	}
	_ = os.Remove(localBackup)
	return backup, nil
}

func writeImportFile(dir, pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
