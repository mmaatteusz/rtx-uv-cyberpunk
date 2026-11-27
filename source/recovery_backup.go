package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type recoveryPolicy struct {
	Mode    string
	MSISlot int
}
type Recovery struct {
	Format                  int
	ReadOnly                bool
	Token, MSIX, SessionDir string
	Slot                    int
	Before                  Snapshot
	MSI                     MSIBackup
	watchdogDone            <-chan struct{}
	journalHash             string
	backupHash              string
}

func digestBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hashPath(path string) (string, error) {
	data, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer data.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type MSIBackup struct {
	Folder, EXEHash string
	Hashes          map[string]string
}

func scanMSI(path string) (MSIBackup, error) {
	backup := MSIBackup{Folder: filepath.Dir(path), Hashes: map[string]string{}}
	if !strings.EqualFold(filepath.Base(path), "MSIAfterburner.exe") {
		return backup, errors.New("wskaż MSIAfterburner.exe")
	}
	var err error
	backup.EXEHash, err = hashPath(path)
	if err != nil {
		return backup, err
	}
	config := filepath.Join(backup.Folder, "MSIAfterburner.cfg")
	backup.Hashes["MSIAfterburner.cfg"], err = hashPath(config)
	if err != nil {
		return backup, fmt.Errorf("brak konfiguracji MSI: %w", err)
	}
	rootConfig, err := os.ReadDir(backup.Folder)
	if err != nil {
		return backup, err
	}
	for _, item := range rootConfig {
		if item.IsDir() || !strings.HasSuffix(strings.ToLower(item.Name()), ".cfg") {
			continue
		}
		backup.Hashes[item.Name()], err = hashPath(filepath.Join(backup.Folder, item.Name()))
		if err != nil {
			return backup, err
		}
	}
	profileRoot := filepath.Join(backup.Folder, "Profiles")
	err = filepath.WalkDir(profileRoot, func(file string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		if !item.Type().IsRegular() {
			return fmt.Errorf("plik profilu nie jest zwykłym plikiem: %s", file)
		}
		relative, err := filepath.Rel(backup.Folder, file)
		if err != nil {
			return err
		}
		backup.Hashes[relative], err = hashPath(file)
		return err
	})
	if err != nil {
		return backup, fmt.Errorf("błąd odczytu katalogu Profiles: %w", err)
	}
	if len(backup.Hashes) < 2 {
		return backup, errors.New("brak zapisanych profili MSI Afterburner")
	}
	return backup, nil
}

func (a MSIBackup) same(b MSIBackup) bool {
	if a.Folder != b.Folder || a.EXEHash != b.EXEHash || len(a.Hashes) != len(b.Hashes) {
		return false
	}
	for name, hash := range a.Hashes {
		if b.Hashes[name] != hash {
			return false
		}
	}
	return true
}

func (a MSIBackup) save(path string, base Snapshot, policy recoveryPolicy) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	archive := zip.NewWriter(out)
	names := make([]string, 0, len(a.Hashes))
	for name := range a.Hashes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		src, err := os.Open(filepath.Join(a.Folder, name))
		if err != nil {
			return err
		}
		file, err := archive.Create("MSI-Afterburner/" + filepath.ToSlash(name))
		if err != nil {
			src.Close()
			return err
		}
		digest := sha256.New()
		_, err = io.Copy(io.MultiWriter(file, digest), src)
		src.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(digest.Sum(nil)) != a.Hashes[name] {
			return errors.New("MSI zmienił plik podczas wykonywania kopii")
		}
	}
	for name, obj := range map[string]any{"krzywa-oryginalna.json": base, "manifest-MSI.json": a, "tryb-odzyskiwania.json": policy} {
		w, err := archive.Create(name)
		if err != nil {
			return err
		}
		if err = encodeJSON(w, obj); err != nil {
			return err
		}
	}
	if err = archive.Close(); err != nil {
		return err
	}
	return out.Sync()
}

func readArchiveEntry(archive *zip.ReadCloser, name string, target any) error {
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		defer in.Close()
		return json.NewDecoder(io.LimitReader(in, 2<<20)).Decode(target)
	}
	return fmt.Errorf("brak %s w kopii sesji", name)
}

func verifyMSIArchive(archive *zip.ReadCloser, msi MSIBackup) error {
	pending := make(map[string]string, len(msi.Hashes))
	for name, digest := range msi.Hashes {
		pending["MSI-Afterburner/"+filepath.ToSlash(name)] = digest
	}
	for _, file := range archive.File {
		if !strings.HasPrefix(file.Name, "MSI-Afterburner/") {
			continue
		}
		expected, ok := pending[file.Name]
		if !ok {
			return fmt.Errorf("dodatkowy lub zduplikowany plik MSI w kopii: %s", file.Name)
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, src)
		closeErr := src.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return fmt.Errorf("błędna suma pliku MSI w kopii: %s", file.Name)
		}
		delete(pending, file.Name)
	}
	if len(pending) != 0 || len(msi.Hashes) == 0 {
		return errors.New("brak pliku MSI w kopii")
	}
	return nil
}

func verifyRecoveryBackup(r Recovery) error {
	archive, err := zip.OpenReader(filepath.Join(r.SessionDir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return fmt.Errorf("nie można zweryfikować kopii oryginalnego OC: %w", err)
	}
	defer archive.Close()
	var archived Snapshot
	var msi MSIBackup
	if err = readArchiveEntry(archive, "krzywa-oryginalna.json", &archived); err != nil {
		return err
	}
	if err = readArchiveEntry(archive, "manifest-MSI.json", &msi); err != nil {
		return err
	}
	if err = verifyMSIArchive(archive, msi); err != nil {
		return err
	}
	if err = validateSavedCurve(r.Before); err != nil {
		return err
	}
	if err = validateSavedCurve(archived); err != nil {
		return err
	}
	if !exactOriginal(archived, r.Before) || !slices.Equal(archived.Points, r.Before.Points) || !msi.same(r.MSI) ||
		!strings.EqualFold(filepath.Clean(r.MSIX), filepath.Join(msi.Folder, "MSIAfterburner.exe")) {
		return errors.New("dziennik odzyskiwania różni się od kopii oryginalnego OC")
	}
	var session recoveryPolicy
	switch r.Format {
	case 1:
		if err = readArchiveEntry(archive, "tryb-odzyskiwania.json", &session); err != nil {
			return err
		}
	case 0:
		// Old journals used the progress file and had no immutable mode record.
		for _, entry := range archive.File {
			if entry.Name == "tryb-odzyskiwania.json" {
				return errors.New("niezgodna wersja dziennika i kopii")
			}
		}
		if err = readJSON(filepath.Join(r.SessionDir, "sesja.json"), &session); err != nil {
			return err
		}
	default:
		return errors.New("nieznana wersja dziennika odzyskiwania")
	}
	if !((session.Mode == "Dry Run" && r.ReadOnly) || (session.Mode == "Optymalizacja" && !r.ReadOnly)) || session.MSISlot != r.Slot || r.Slot < 1 || r.Slot > 5 {
		return errors.New("tryb odzyskiwania lub numer slotu różnią się od pierwotnego zapisu sesji")
	}

	return nil
}
