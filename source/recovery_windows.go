package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const recoveryFile = "oczekujace-odzyskiwanie.json"

func startupEntry() (string, error) {
	directory, err := knownFolder(&startupGUID)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "RTX-UV-Cyberpunk-Odzyskiwanie.cmd"), nil
}

func writeOnce(path string, body []byte) error {
	w, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	if err == nil {
		err = w.Sync()
	}
	closeErr := w.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func arm(root string, r *Recovery) error {
	r.Format = 1
	pending := filepath.Join(root, recoveryFile)
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		return errors.New("najpierw zakończ oczekujące odzyskiwanie")
	}
	if err := verifyRecoveryBackup(*r); err != nil {
		return err
	}
	backupHash, err := hashPath(filepath.Join(r.SessionDir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return err
	}
	r.backupHash = backupHash

	entry, err := startupEntry()
	if err != nil {
		return err
	}
	if _, err = os.Stat(entry); !os.IsNotExist(err) {
		return errors.New("istnieje już wpis odzyskiwania po logowaniu")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	recoverExe := filepath.Join(root, "odzyskiwanie.exe")
	if err = os.Remove(recoverExe); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = copyPath(exe, recoverExe); err != nil {
		return err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return err
	}
	r.Token = hex.EncodeToString(token[:])
	if err = r.mark(); err != nil {
		return err
	}
	if err = writeJSON(pending, r); err != nil {
		return err
	}
	armed := false
	defer func() {
		if !armed {
			_ = disarm(root, *r)
		}
	}()
	r.journalHash, err = hashPath(pending)
	if err != nil {
		return err
	}
	script := "@echo off\r\nif exist \"%LOCALAPPDATA%\\RTX-UV-Cyberpunk\\oczekujace-odzyskiwanie.json\" start \"\" \"%LOCALAPPDATA%\\RTX-UV-Cyberpunk\\odzyskiwanie.exe\" --recover-at-logon\r\n"
	if err = writeOnce(entry, []byte(script)); err != nil {
		return err
	}
	child := exec.Command(recoverExe, "--watch", strconv.Itoa(os.Getpid()), r.Token)
	if err = child.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	r.watchdogDone = done
	go func() { _ = child.Wait(); close(done) }()
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		select {
		case <-done:
			return errors.New("niezależny nadzór zakończył działanie podczas uruchamiania")
		default:
		}
		data, _ := os.ReadFile(filepath.Join(r.SessionDir, "nadzor-gotowy"))
		if string(data) == r.Token && r.checkWatchdog() == nil {
			armed = true
			return nil
		}
	}
	return errors.New("nie potwierdzono uruchomienia niezależnego nadzoru")
}

func (r Recovery) watchdogPulse() string {
	return filepath.Join(r.SessionDir, "nadzor-zyje-"+r.Token)
}

func (r Recovery) checkWatchdog() error {
	info, err := os.Stat(r.watchdogPulse())
	if err != nil {
		return fmt.Errorf("brak sygnału watchdoga: %w", err)
	}
	return checkWatchdogSignal(r.watchdogDone, info.ModTime(), time.Now())
}

func (r Recovery) journalIsIntact(root string) error {
	hash, err := hashPath(filepath.Join(root, recoveryFile))
	if err != nil {
		return err
	}
	if r.journalHash == "" || hash != r.journalHash {
		return errors.New("dziennik odzyskiwania zmienił się w trakcie sesji")
	}
	return nil
}

func (r Recovery) safetyFilesIntact(root string) error {
	if err := r.journalIsIntact(root); err != nil {
		return err
	}
	hash, err := hashPath(filepath.Join(r.SessionDir, "kopia-MSI-i-VF.zip"))
	if err != nil {
		return fmt.Errorf("brak kopii oryginalnego OC: %w", err)
	}
	if r.backupHash == "" || hash != r.backupHash {
		return errors.New("kopia oryginalnego OC zmieniła się w trakcie sesji")
	}
	return nil
}

func (r Recovery) checkWrite(root string) error {
	if r.ReadOnly {
		return errors.New("Dry Run blokuje zapis w sterowniku")
	}
	if err := r.checkWatchdog(); err != nil {
		return err
	}
	if err := r.safetyFilesIntact(root); err != nil {
		return err
	}
	entry, err := startupEntry()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(entry)
	if err != nil {
		return err
	}
	if !containsRecoveryCommand(string(data)) {
		return errors.New("wpis odzyskiwania po zalogowaniu uległ zmianie")
	}
	return nil
}

func (r Recovery) recreateMissingJournal(root string) error {
	path := filepath.Join(root, recoveryFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeOnce(path, data)
}

func (r Recovery) mark() error {
	path := filepath.Join(r.SessionDir, "nadzor-puls")
	now := time.Now()
	if err := os.Chtimes(path, now, now); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(r.Token), 0600)
}

func disarm(root string, r Recovery) error {
	pending := filepath.Join(root, recoveryFile)
	var current Recovery
	if err := readJSON(pending, &current); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if current.Token != r.Token {
		return errors.New("zmienił się właściciel dziennika odzyskiwania")
	}
	entry, err := startupEntry()
	if err != nil {
		return err
	}
	if data, e := os.ReadFile(entry); e == nil {
		if !containsRecoveryCommand(string(data)) {
			return errors.New("wpis odzyskiwania został zmieniony")
		}
		if err = os.Remove(entry); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	return os.Remove(pending)
}

func containsRecoveryCommand(body string) bool {
	return body == "@echo off\r\nif exist \"%LOCALAPPDATA%\\RTX-UV-Cyberpunk\\oczekujace-odzyskiwanie.json\" start \"\" \"%LOCALAPPDATA%\\RTX-UV-Cyberpunk\\odzyskiwanie.exe\" --recover-at-logon\r\n"
}

func recoveryLog(root string, message string) {
	f, err := os.OpenFile(filepath.Join(root, "odzyskiwanie.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s  %s\r\n", time.Now().Format(time.RFC3339), message)
}

// Keep restart and new optimization behind the journal until a verified recovery.
func recoverPending(root string) (bool, error) {
	if _, err := os.Stat(filepath.Join(root, recoveryFile)); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, restoreIndependent(root, false)
}

func watchdog(root string, pid int, token string) {
	var state Recovery
	if readJSON(filepath.Join(root, recoveryFile), &state) != nil || state.Token != token {
		return
	}
	parent, err := syscall.OpenProcess(0x00100001, false, uint32(pid))
	if err != nil {
		recoveryLog(root, "nadzór: "+err.Error())
		return
	}
	defer syscall.CloseHandle(parent)
	if err = os.WriteFile(state.watchdogPulse(), []byte(token), 0600); err != nil {
		return
	}

	if err = os.WriteFile(filepath.Join(state.SessionDir, "nadzor-gotowy"), []byte(token), 0600); err != nil {
		return
	}
	for {
		var current Recovery
		if err = readJSON(filepath.Join(root, recoveryFile), &current); os.IsNotExist(err) || (err == nil && current.Token != token) {
			return
		}
		now := time.Now()
		if err = os.Chtimes(state.watchdogPulse(), now, now); err != nil {
			recoveryLog(root, err.Error())
			return
		}
		waitErr, err := syscall.WaitForSingleObject(parent, uint32(watchdogInterval/time.Millisecond))
		if err != nil {
			recoveryLog(root, err.Error())
			return
		}
		if waitErr == 0 {
			break
		}
		stat, statErr := os.Stat(filepath.Join(state.SessionDir, "nadzor-puls"))
		if statErr != nil || time.Since(stat.ModTime()) > 65*time.Second || time.Until(stat.ModTime()) > watchdogInterval {
			var owner Recovery
			if err := readJSON(filepath.Join(root, recoveryFile), &owner); os.IsNotExist(err) || (err == nil && owner.Token != token) {
				return
			}
			recoveryLog(root, "proces sterujący przestał odpowiadać")
			if err = syscall.TerminateProcess(parent, 1); err != nil {
				recoveryLog(root, err.Error())
				return
			}
			_, _ = syscall.WaitForSingleObject(parent, 10000)
			break
		}
	}
	if err = restoreIndependent(root, false); err != nil {
		recoveryLog(root, "odzyskiwanie: "+err.Error())
	}
}
