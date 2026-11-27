package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel   = syscall.NewLazyDLL("kernel32.dll")
	shell    = syscall.NewLazyDLL("shell32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	ole      = syscall.NewLazyDLL("ole32.dll")
	moveFile = kernel.NewProc("MoveFileExW")
)

func wide(s string) *uint16 { return syscall.StringToUTF16Ptr(s) }

func system32() string {
	var path [1024]uint16
	n, _, _ := kernel.NewProc("GetSystemDirectoryW").Call(uintptr(unsafe.Pointer(&path)), uintptr(len(path)))
	if n > 0 && n < uintptr(len(path)) {
		return syscall.UTF16ToString(path[:n])
	}
	return filepath.Join(os.Getenv("SystemRoot"), "System32")
}

var documentsGUID = syscall.GUID{Data1: 0xFDD39AD0, Data2: 0x238F, Data3: 0x46AF, Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7}}
var startupGUID = syscall.GUID{Data1: 0xB97D20BB, Data2: 0xF46A, Data3: 0x4C97, Data4: [8]byte{0xBA, 0x10, 0x5E, 0x36, 0x08, 0x43, 0x08, 0x54}}

func knownFolder(guid *syscall.GUID) (string, error) {
	var buffer *[32768]uint16
	status, _, _ := shell.NewProc("SHGetKnownFolderPath").Call(uintptr(unsafe.Pointer(guid)), 0, 0, uintptr(unsafe.Pointer(&buffer)))
	if int32(status) != 0 || buffer == nil {
		return "", fmt.Errorf("nie można odczytać folderu Windows (0x%x)", status)
	}
	defer ole.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(buffer)))
	return syscall.UTF16ToString(buffer[:]), nil
}

func closeHandle(handle uintptr) {
	if handle != 0 {
		_ = syscall.CloseHandle(syscall.Handle(handle))
	}
}

func singleInstance() (uintptr, error) {
	handle, _, err := kernel.NewProc("CreateMutexW").Call(0, 0, uintptr(unsafe.Pointer(wide("Global\\RTX-UV-Cyberpunk-Hardware"))))
	if handle == 0 {
		return 0, err
	}
	if err == syscall.ERROR_ALREADY_EXISTS {
		closeHandle(handle)
		return 0, errors.New("inna sesja lub proces odzyskiwania już ma dostęp do GPU")
	}
	return handle, nil
}

type processEntry struct {
	Size, Usage, PID        uint32
	DefaultHeap             uintptr
	Module, Threads, Parent uint32
	Priority                int32
	Flags                   uint32
	Name                    [260]uint16
}

func processExists(name string) (bool, error) {
	match, err := firstRunningProcess([]string{name})
	return match != "", err
}

func firstRunningProcess(names []string) (string, error) {
	handle, err := syscall.CreateToolhelp32Snapshot(2, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	var entry processEntry
	entry.Size = uint32(unsafe.Sizeof(entry))
	status, _, err := kernel.NewProc("Process32FirstW").Call(uintptr(handle), uintptr(unsafe.Pointer(&entry)))
	if status == 0 {
		return "", err
	}
	next := kernel.NewProc("Process32NextW")
	for status != 0 {
		current := syscall.UTF16ToString(entry.Name[:])
		for _, name := range names {
			if strings.EqualFold(current, name) {
				return current, nil
			}
		}
		status, _, err = next.Call(uintptr(handle), uintptr(unsafe.Pointer(&entry)))
	}
	if err == syscall.ERROR_NO_MORE_FILES {
		return "", nil
	}
	return "", err
}

func replaceDurably(src, dst string) error {
	success, _, err := moveFile.Call(uintptr(unsafe.Pointer(wide(src))), uintptr(unsafe.Pointer(wide(dst))), 0x1|0x8)
	if success == 0 {
		return err
	}
	return nil
}

type Job struct{ handle uintptr }

func newJob() (Job, error) {
	handle, _, err := kernel.NewProc("CreateJobObjectW").Call(0, 0)
	if handle == 0 {
		return Job{}, err
	}
	var limits [144]byte
	put(limits[:], 16, 0x2000) // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, err := kernel.NewProc("SetInformationJobObject").Call(handle, 9, uintptr(unsafe.Pointer(&limits)), uintptr(len(limits)))
	if ok == 0 {
		closeHandle(handle)
		return Job{}, err
	}
	return Job{handle}, nil
}

func (j Job) assign(pid int) error {
	handle, err := syscall.OpenProcess(0x0101, false, uint32(pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	ok, _, err := kernel.NewProc("AssignProcessToJobObject").Call(j.handle, uintptr(handle))
	if ok == 0 {
		return err
	}
	return nil
}

func (j Job) close() { closeHandle(j.handle) }

func copyPath(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func (a MSIBackup) guard(path string) error {
	now, err := scanMSI(path)
	if err != nil {
		return err
	}
	if !a.same(now) {
		return errors.New("zmieniły się pliki konfiguracyjne, profile lub wersja MSI Afterburner")
	}
	running, err := processExists("MSIAfterburner.exe")
	if err != nil {
		return err
	}
	if !running {
		return errors.New("MSI Afterburner powinien pozostać uruchomiony dla własnej krzywej wentylatorów")
	}
	return nil
}

func verifiedMSILoad(path string, slot int, backup MSIBackup) error {
	if slot < 1 || slot > 5 {
		return errors.New("nieprawidłowy numer oryginalnego profilu MSI")
	}
	hash, err := hashPath(path)
	if err != nil {
		return err
	}
	if backup.EXEHash != hash {
		return errors.New("instalacja MSI zmieniła się od chwili utworzenia kopii")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	direct := exec.Command(path, fmt.Sprintf("/Profile%d", slot))
	direct.Dir = filepath.Dir(path)
	if err = direct.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- direct.Wait() }()
	select {
	case err = <-done:
		// New instances can return immediately after handing the command to MSI.
		return err
	case <-ctx.Done():
		// If MSI started after reboot it should keep running for the fan curve.
		return nil
	}
}
