package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const title = "RTX UV · Cyberpunk 2077"

var gdi = syscall.NewLazyDLL("gdi32.dll")
var comdlg = syscall.NewLazyDLL("comdlg32.dll")
var getClient = user32.NewProc("GetClientRect")
var desktop *Desktop

type Rect struct{ Left, Top, Right, Bottom int32 }

type windowClass struct {
	Size, Style                                               uint32
	WndProc                                                   uintptr
	ClsExtra, WndExtra                                        int32
	Instance, Icon, Cursor, Background, Menu, Name, SmallIcon uintptr
}

type message struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}

type paintData struct {
	DC                 uintptr
	Erase              int32
	Rect               Rect
	Restore, IncUpdate int32
	Reserved           [32]byte
}

type openFileName struct {
	Size                                             uint32
	Owner, Instance, Filter, CustomFilter            uintptr
	MaxCustomFilter, FilterIndex                     uint32
	File                                             uintptr
	MaxFile                                          uint32
	FileTitle                                        uintptr
	MaxFileTitle                                     uint32
	InitialDir, Title                                uintptr
	Flags                                            uint32
	FileOffset, FileExt                              uint16
	DefaultExt, CustomData, Hook, Template, Reserved uintptr
	ReservedWord, FlagsEx                            uint32
}

const (
	gameEdit    = 100
	msiEdit     = 101
	slotEdit    = 102
	browseGame  = 110
	browseMSI   = 111
	observe     = 113
	optimize    = 114
	stop        = 115
	restore     = 116
	folder      = 117
	winner      = 118
	pickSession = 119
	historyPage = 120
	rankingPage = 121
	metricPick  = 122
	previewPick = 123
	dryPick     = 124
	importPick  = 125
)

type Desktop struct {
	root                     string
	window, inst, font, mono uintptr
	controls                 map[int]uintptr
	dpi                      int32
	mu                       sync.Mutex
	pendingSession           *Session
	pendingSamples           []Sample
	pendingErr               error
	pendingStatus            string
	finished                 bool
	session                  Session
	cancel                   context.CancelFunc
	busy, closing            bool
	previewing               bool
	series                   []Sample
	sessions                 []string
	viewRank                 bool
	chart                    Rect
	historyText              string
	selected                 string
}

func appRoot() (string, error) {
	path := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(path) {
		return "", errors.New("Windows nie udostępnił folderu LocalAppData")
	}
	root := filepath.Join(path, "RTX-UV-Cyberpunk")
	return root, os.MkdirAll(filepath.Join(root, "sesje"), 0700)
}

func main() {
	root, err := appRoot()
	if err != nil {
		popup(0, err.Error(), 0x10)
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--watch":
			if len(os.Args) == 4 {
				pid, e := strconv.Atoi(os.Args[2])
				if e == nil {
					watchdog(root, pid, os.Args[3])
				}
			}
		case "--recover-at-logon":
			if err = restoreIndependent(root, true); err != nil {
				recoveryLog(root, "po zalogowaniu: "+err.Error())
			}
		}
		return
	}
	runtime.LockOSThread()
	desktop = &Desktop{root: root, controls: map[int]uintptr{}, dpi: 96}
	if err = desktop.open(); err != nil {
		popup(0, err.Error(), 0x10)
	}
}

func popup(parent uintptr, text string, kind uintptr) int {
	r, _, _ := user32.NewProc("MessageBoxW").Call(parent, uintptr(unsafe.Pointer(wide(text))), uintptr(unsafe.Pointer(wide(title))), kind)
	return int(r)
}

func (d *Desktop) open() error {
	_, _, _ = user32.NewProc("SetProcessDPIAware").Call()
	dpi, _, _ := user32.NewProc("GetDpiForSystem").Call()
	if dpi >= 96 && dpi <= 384 {
		d.dpi = int32(dpi)
	}
	d.inst, _, _ = kernel.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ := user32.NewProc("LoadIconW").Call(0, 32512)
	className := wide("RTXUVCyberpunkNativeWindow")
	var classPin runtime.Pinner
	classPin.Pin(className)
	defer classPin.Unpin()
	callback := syscall.NewCallback(windowProcedure)
	cl := windowClass{WndProc: callback, Size: uint32(unsafe.Sizeof(windowClass{})), Instance: d.inst,
		Cursor: cursor, Icon: icon, SmallIcon: icon, Background: 6, Name: uintptr(unsafe.Pointer(className))}
	if ret, _, err := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&cl))); ret == 0 {
		return err
	}
	d.window, _, _ = user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(wide(title))), 0x00CF0000|0x02000000,
		0x80000000, 0x80000000, uintptr(d.px(1110)), uintptr(d.px(795)), 0, 0, d.inst, 0)
	runtime.KeepAlive(className)
	if d.window == 0 {
		return errors.New("nie można utworzyć okna Windows")
	}
	d.font = d.makeFont(15, "Segoe UI")
	d.mono = d.makeFont(14, "Consolas")
	d.initControls()
	user32.NewProc("ShowWindow").Call(d.window, 5)
	user32.NewProc("UpdateWindow").Call(d.window)
	user32.NewProc("SetTimer").Call(d.window, 1, 350, 0)
	var msg message
	get := user32.NewProc("GetMessageW")
	isDialog := user32.NewProc("IsDialogMessageW")
	translate := user32.NewProc("TranslateMessage")
	dispatch := user32.NewProc("DispatchMessageW")
	for {
		status, _, err := get.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(status) < 0 {
			return err
		}
		if status == 0 {
			break
		}
		handled, _, _ := isDialog.Call(d.window, uintptr(unsafe.Pointer(&msg)))
		if handled == 0 {
			translate.Call(uintptr(unsafe.Pointer(&msg)))
			dispatch.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
	return nil
}

func (d *Desktop) px(v int32) int32 { return v * d.dpi / 96 }

func (d *Desktop) makeFont(size int32, name string) uintptr {
	h, _, _ := gdi.NewProc("CreateFontW").Call(uintptr(-d.px(size)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(wide(name))))
	return h
}

func windowProcedure(window, msg, wParam, lParam uintptr) uintptr {
	d := desktop
	switch msg {
	case 0x0005:
		if d != nil && d.window != 0 {
			d.layout()
		}
		return 0
	case 0x0024:
		if d != nil {
			minSize := [2]int32{d.px(960), d.px(670)}
			syscall.NewLazyDLL("ntdll.dll").NewProc("RtlMoveMemory").Call(lParam+24, uintptr(unsafe.Pointer(&minSize)), 8)
		}
		return 0
	case 0x000F:
		if d != nil {
			d.draw(window)
			return 0
		}
	case 0x0111:
		if d != nil {
			d.command(int(wParam&0xffff), int((wParam>>16)&0xffff))
		}
		return 0
	case 0x0113:
		if d != nil {
			d.tick()
		}
		return 0
	case 0x0010:
		if d != nil && d.busy {
			d.closing = true
			if d.cancel != nil {
				d.cancel()
			}
			setText(d.controls[203], "Zamykanie sesji i przywracanie krzywej…")
			return 0
		}
		user32.NewProc("DestroyWindow").Call(window)
		return 0
	case 0x0002:
		if d != nil {
			gdi.NewProc("DeleteObject").Call(d.font)
			gdi.NewProc("DeleteObject").Call(d.mono)
		}
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	ret, _, _ := user32.NewProc("DefWindowProcW").Call(window, msg, wParam, lParam)
	return ret
}

func (d *Desktop) control(id int, kind, text string, style, ex uintptr) uintptr {
	window, _, _ := user32.NewProc("CreateWindowExW").Call(ex, uintptr(unsafe.Pointer(wide(kind))), uintptr(unsafe.Pointer(wide(text))),
		0x50000000|style, 0, 0, 0, 0, d.window, uintptr(id), d.inst, 0)
	d.controls[id] = window
	send(window, 0x30, d.font, 1)
	return window
}

//go:uintptrescapes
func send(window, msg, a, b uintptr) uintptr {
	value, _, _ := user32.NewProc("SendMessageW").Call(window, msg, a, b)
	return value
}

func setText(window uintptr, text string) {
	user32.NewProc("SetWindowTextW").Call(window, uintptr(unsafe.Pointer(wide(text))))
}

func windowText(window uintptr) string {
	n, _, _ := user32.NewProc("GetWindowTextLengthW").Call(window)
	buf := make([]uint16, n+1)
	user32.NewProc("GetWindowTextW").Call(window, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func comboAdd(w uintptr, label string) { send(w, 0x143, 0, uintptr(unsafe.Pointer(wide(label)))) }

func (d *Desktop) initControls() {
	d.control(201, "STATIC", "Cyberpunk 2077", 0, 0)
	d.control(202, "STATIC", "MSI Afterburner", 0, 0)
	d.control(204, "STATIC", "Slot OC 1–5", 0, 0)
	d.control(203, "STATIC", "Gotowe do pomiaru. Wskaż grę i oryginalny slot MSI Afterburner.", 0, 0)
	d.control(205, "STATIC", "Telemetria GPU pojawi się podczas benchmarku.", 0, 0)
	d.control(206, "STATIC", "Historia sesji", 0, 0)
	d.control(gameEdit, "EDIT", "", 0x10080, 0x200)
	d.control(msiEdit, "EDIT", "", 0x10080, 0x200)
	d.control(slotEdit, "EDIT", "", 0x12000, 0x200)
	send(d.controls[slotEdit], 0xC5, 1, 0)
	for _, b := range []struct {
		id   int
		text string
	}{
		{browseGame, "Wybierz…"}, {browseMSI, "Wybierz…"}, {observe, "Zmierz obecny OC"}, {optimize, "Rozpocznij optymalizację"},
		{stop, "STOP"}, {restore, "Odzyskaj oryginał"}, {folder, "Otwórz wyniki"}, {winner, "Wynik i krzywa UV"},
		{historyPage, "Benchmarki"}, {rankingPage, "Ranking profili"}, {previewPick, "Wypróbuj UV przez 5 min"}, {dryPick, "Dry Run"}, {importPick, "Zapisz UV w MSI…"},
	} {
		d.control(b.id, "BUTTON", b.text, 0x10000, 0)
	}
	d.control(pickSession, "COMBOBOX", "", 0x10203|0x200000, 0)
	d.control(metricPick, "COMBOBOX", "", 0x10203|0x200000, 0)
	for _, metric := range []string{"Zegar GPU · MHz", "Napięcie rdzenia · mV", "Temperatura GPU · °C", "Hot Spot · °C", "Pobór mocy · W", "Obciążenie GPU · %", "FPS według benchmarków", "Frametime ostatniego pomiaru · ms"} {
		comboAdd(d.controls[metricPick], metric)
	}
	send(d.controls[metricPick], 0x14e, 0, 0)
	d.control(207, "EDIT", "", 0x10000|0x200000|0x100000|0x884, 0x200)
	send(d.controls[207], 0x30, d.mono, 1)
	send(d.controls[207], 0xC5, 2000000, 0)
	cfg := defaultConfig()
	_ = readJSON(filepath.Join(d.root, "konfiguracja.json"), &cfg)
	setText(d.controls[gameEdit], cfg.Game)
	setText(d.controls[msiEdit], cfg.MSI)
	if cfg.Slot > 0 {
		setText(d.controls[slotEdit], strconv.Itoa(cfg.Slot))
	}
	d.reloadSessions("")
	d.toggle(false)
	d.layout()
}

func defaultConfig() Config {
	program := os.Getenv("ProgramFiles(x86)")
	cfg := Config{MSI: filepath.Join(program, "MSI Afterburner", "MSIAfterburner.exe")}
	for _, path := range []string{
		filepath.Join(program, "Steam", "steamapps", "common", "Cyberpunk 2077", "bin", "x64", "Cyberpunk2077.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Epic Games", "Cyberpunk2077", "bin", "x64", "Cyberpunk2077.exe"),
		filepath.Join(program, "GOG Galaxy", "Games", "Cyberpunk 2077", "bin", "x64", "Cyberpunk2077.exe"),
	} {
		if _, err := os.Stat(path); err == nil {
			cfg.Game = path
			break
		}
	}
	return cfg
}

func (d *Desktop) layout() {
	var r Rect
	getClient.Call(d.window, uintptr(unsafe.Pointer(&r)))
	width, height := r.Right*96/d.dpi, r.Bottom*96/d.dpi
	if width < 100 || height < 100 {
		return
	}
	move := user32.NewProc("MoveWindow")
	place := func(id int, x, y, w, h int32) {
		move.Call(d.controls[id], uintptr(d.px(x)), uintptr(d.px(y)), uintptr(d.px(w)), uintptr(d.px(h)), 1)
	}
	place(201, 20, 22, 120, 20)
	place(gameEdit, 155, 18, width-273, 27)
	place(browseGame, width-108, 18, 88, 27)
	place(202, 20, 58, 120, 20)
	place(msiEdit, 155, 54, width-445, 27)
	place(browseMSI, width-279, 54, 88, 27)
	place(204, width-178, 58, 80, 20)
	place(slotEdit, width-83, 54, 63, 27)
	place(observe, 20, 93, 165, 32)
	place(optimize, 194, 93, 194, 32)
	place(stop, 397, 93, 100, 32)
	place(restore, 506, 93, 152, 32)
	place(winner, width-214, 93, 194, 32)
	place(203, 20, 139, width-40, 38)
	place(205, 20, 181, width-40, 25)
	place(metricPick, width-297, 211, 277, 270)
	place(previewPick, 20, 209, 210, 28)
	place(dryPick, 241, 209, 110, 28)
	place(importPick, 362, 209, 185, 28)
	plotHeight := (height - 232) / 2
	d.chart = Rect{d.px(20), d.px(242), d.px(width - 20), d.px(242 + plotHeight - 34)}
	top := 242 + plotHeight - 23
	place(206, 20, top+7, 105, 22)
	place(pickSession, 128, top+3, width-584, 265)
	place(historyPage, width-445, top+2, 108, 27)
	place(rankingPage, width-330, top+2, 126, 27)
	place(folder, width-197, top+2, 177, 27)
	place(207, 20, top+39, width-40, max(30, height-top-56))
	d.invalidate()
}

func (d *Desktop) invalidate() {
	user32.NewProc("InvalidateRect").Call(d.window, uintptr(unsafe.Pointer(&d.chart)), 0)
}

func (d *Desktop) browse(control int, basename string) {
	var path [2048]uint16
	current := windowText(d.controls[control])
	if current != "" {
		copy(path[:], syscall.StringToUTF16(current))
	}
	filter := syscall.StringToUTF16("Plik programu (*.exe)")
	filter = append(filter, syscall.StringToUTF16("*.exe")...)
	filter = append(filter, 0)
	label := wide("Wybierz " + basename)
	var dialogPin runtime.Pinner
	dialogPin.Pin(&path)
	dialogPin.Pin(&filter[0])
	dialogPin.Pin(label)
	defer dialogPin.Unpin()
	ofn := openFileName{Size: uint32(unsafe.Sizeof(openFileName{})), Owner: d.window, Filter: uintptr(unsafe.Pointer(&filter[0])),
		File: uintptr(unsafe.Pointer(&path)), MaxFile: uint32(len(path)), Title: uintptr(unsafe.Pointer(label)), Flags: 0x00081000}
	ok, _, _ := comdlg.NewProc("GetOpenFileNameW").Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(filter)
	runtime.KeepAlive(label)
	if ok != 0 {
		selected := syscall.UTF16ToString(path[:])
		if !strings.EqualFold(filepath.Base(selected), basename) {
			popup(d.window, "Oczekiwano pliku "+basename+".", 0x30)
			return
		}
		setText(d.controls[control], selected)
	}
}

func (d *Desktop) toggle(busy bool) {
	d.busy = busy
	for _, id := range []int{gameEdit, msiEdit, slotEdit, browseGame, browseMSI, observe, optimize, restore, pickSession, dryPick} {
		user32.NewProc("EnableWindow").Call(d.controls[id], boolArg(!busy))
	}
	user32.NewProc("EnableWindow").Call(d.controls[stop], boolArg(busy && d.cancel != nil))
	user32.NewProc("EnableWindow").Call(d.controls[winner], boolArg(!busy && d.session.Healthy && d.session.Winner > 0))
	user32.NewProc("EnableWindow").Call(d.controls[previewPick], boolArg(!busy && d.session.Healthy && d.session.Winner > 0))
	user32.NewProc("EnableWindow").Call(d.controls[importPick], boolArg(!busy && d.session.Healthy && d.session.Winner > 0))
}

func boolArg(v bool) uintptr {
	if v {
		return 1
	}
	return 0
}

func (d *Desktop) command(id, code int) {
	if id == pickSession && code == 1 {
		i := send(d.controls[pickSession], 0x147, 0, 0)
		if i < uintptr(len(d.sessions)) {
			d.loadSession(d.sessions[i])
		}
		return
	}
	if id == metricPick && code == 1 {
		d.invalidate()
		return
	}
	if code != 0 {
		return
	}
	switch id {
	case browseGame:
		d.browse(gameEdit, "Cyberpunk2077.exe")
	case browseMSI:
		d.browse(msiEdit, "MSIAfterburner.exe")
	case observe:
		d.begin("Pomiar OC")
	case optimize:
		if popup(d.window, "Wczytaj zapisany oryginalny slot MSI i pozostaw MSI Afterburner uruchomiony. Zakończ działanie innych programów strojących GPU i nie zmieniaj profilu MSI podczas sesji.\r\n\r\nPo pomiarze OC program wykona kontrolowaną próbę zapisu i odzyskania krzywej. Rozpocząć?", 0x124) == 6 {
			d.begin("Optymalizacja")
		}
	case stop:
		if d.cancel != nil {
			d.cancel()
			setText(d.controls[203], "Przerywanie benchmarku i przywracanie oryginału…")
		}
	case restore:
		d.recover()
	case folder:
		target := d.selected
		if target == "" {
			target = filepath.Join(d.root, "sesje")
		}
		shell.NewProc("ShellExecuteW").Call(d.window, uintptr(unsafe.Pointer(wide("open"))), uintptr(unsafe.Pointer(wide(target))), 0, 0, 1)
	case historyPage:
		d.viewRank = false
		d.details()
	case rankingPage:
		d.viewRank = true
		d.details()
	case winner:
		d.showWinner()
	case previewPick:
		d.beginPreview()
	case dryPick:
		d.begin("Dry Run")
	case importPick:
		d.importWinner()
	}
}

func (d *Desktop) begin(mode string) {
	if d.busy {
		return
	}
	slot, _ := strconv.Atoi(windowText(d.controls[slotEdit]))
	cfg := Config{strings.Trim(strings.TrimSpace(windowText(d.controls[gameEdit])), "\""),
		strings.Trim(strings.TrimSpace(windowText(d.controls[msiEdit])), "\""), slot}
	if err := writeJSON(filepath.Join(d.root, "konfiguracja.json"), cfg); err != nil {
		popup(d.window, err.Error(), 0x10)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.series = nil
	d.session = Session{}
	d.selected = ""
	d.historyText = ""
	d.details()
	d.invalidate()
	d.toggle(true)
	setText(d.controls[203], "Uruchamianie sesji…")
	go func() {
		e := Engine{root: d.root, cfg: cfg,
			update: func(s Session) {
				s.Runs = slices.Clone(s.Runs)
				s.Ranking = slices.Clone(s.Ranking)
				d.mu.Lock()
				d.pendingSession = &s
				d.mu.Unlock()
			},
			live: func(s Sample) { d.mu.Lock(); d.pendingSamples = append(d.pendingSamples, s); d.mu.Unlock() },
		}
		err := e.start(ctx, mode)
		if e.gpu != nil {
			e.gpu.close()
		}
		if e.armed {
			err = d.completeRecovery(err, func(status string) error { return e.save(status) })
		}
		d.mu.Lock()
		d.pendingErr = err
		d.finished = true
		d.mu.Unlock()
	}()
}

func (d *Desktop) completeRecovery(runErr error, report func(string) error) error {
	if runErr == nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(d.root, recoveryFile)); os.IsNotExist(err) {
		return runErr
	}
	_ = report("Sesja zatrzymana. Trwa wczytywanie i weryfikacja oryginalnego OC…")
	pending, restoreErr := recoverPending(d.root)
	if restoreErr != nil {
		return errors.Join(runErr, restoreErr, report("Sesja zatrzymana. Odzyskiwanie oczekuje na zgodny odczyt oryginalnego OC."))
	}
	if pending {
		status := "Sesja zatrzymana. Oryginalne ustawienia MSI odtworzone i zweryfikowane."
		return errors.Join(runErr, errors.New(status), report(status))
	}
	return runErr
}

func (d *Desktop) recover() {
	if d.busy {
		return
	}
	if _, err := os.Stat(filepath.Join(d.root, recoveryFile)); os.IsNotExist(err) {
		popup(d.window, "Brak oczekującej zmiany krzywej. Po zwykłej sesji program automatycznie potwierdza odtworzenie.", 0x40)
		return
	}
	d.toggle(true)
	setText(d.controls[203], "Weryfikacja i przywracanie oryginału. Może potrwać około minuty.")
	go func() {
		err := restoreIndependent(d.root, false)
		d.mu.Lock()
		d.pendingErr = err
		d.finished = true
		d.pendingSession = nil
		d.mu.Unlock()
	}()
}

func (d *Desktop) tick() {
	d.mu.Lock()
	session, samples, finished, err := d.pendingSession, d.pendingSamples, d.finished, d.pendingErr
	status := d.pendingStatus
	d.pendingStatus = ""
	d.pendingSession = nil
	d.pendingSamples = nil
	d.finished = false
	d.pendingErr = nil
	d.mu.Unlock()
	if session != nil {
		d.session = *session
		if len(session.Runs) > 0 {
			d.selected = filepath.Dir(session.Runs[len(session.Runs)-1].Path)
		}
		setText(d.controls[203], session.Status)
		d.details()
		d.invalidate()
	}
	if len(samples) > 0 {
		s := samples[len(samples)-1]
		d.series = append(d.series, samples...)
		if size := len(d.series); size > 180 {
			d.series = slices.Clone(d.series[size-180:])
		}
		details := fmt.Sprintf("Rdzeń %s MHz    VRAM %s MHz    %s mV    %s W    GPU %s °C    Hot Spot %s °C    GPU %s%%",
			showSample(s.CoreMHz, 0), showSample(s.MemoryMHz, 0), showSample(s.Millivolts, 1), showSample(s.Watts, 1), showSample(s.TempC, 0),
			showSample(s.HotSpotC, 0), showSample(s.LoadPct, 0))
		if s.PerfCap != "" {
			details += "    " + s.PerfCap
		}
		setText(d.controls[205], details)
		d.invalidate()
	}
	if status != "" {
		setText(d.controls[203], status)
	}
	if !finished {
		return
	}
	wasRecovery := d.cancel == nil
	if d.cancel != nil {
		d.cancel()
	}
	d.cancel = nil
	d.toggle(false)
	if d.previewing {
		d.previewing = false
		if err == nil {
			setText(d.controls[203], "Podgląd zakończony. Przywrócono oryginalny profil OC.")
		} else {
			setText(d.controls[203], err.Error())
		}
	} else if wasRecovery {
		if err == nil {
			setText(d.controls[203], "Oryginalny profil odtworzony i zweryfikowany.")
		} else {
			setText(d.controls[203], "Odzyskiwanie oczekuje. "+err.Error())
		}
	} else {
		d.reloadSessions(d.selected)
		if session != nil {
			setText(d.controls[203], session.Status)
		}
	}
	if err != nil && !d.closing {
		popup(d.window, err.Error(), 0x30)
	}
	if d.closing {
		user32.NewProc("DestroyWindow").Call(d.window)
	}
}

func showSample(p *float64, n int) string {
	if p == nil {
		return "—"
	}
	return number(p, n)
}

func (d *Desktop) reloadSessions(preferred string) {
	items, _ := os.ReadDir(filepath.Join(d.root, "sesje"))
	d.sessions = nil
	send(d.controls[pickSession], 0x14b, 0, 0)
	selected := 0
	for i := len(items) - 1; i >= 0; i-- {
		if !items[i].IsDir() {
			continue
		}
		dir := filepath.Join(d.root, "sesje", items[i].Name())
		var s Session
		if readJSON(filepath.Join(dir, "sesja.json"), &s) != nil {
			continue
		}
		if dir == preferred {
			selected = len(d.sessions)
		}
		d.sessions = append(d.sessions, dir)
		comboAdd(d.controls[pickSession], s.Started.Format("02.01.2006 15:04:05")+" · "+s.Mode)
	}
	if len(d.sessions) > 0 {
		send(d.controls[pickSession], 0x14e, uintptr(selected), 0)
		d.loadSession(d.sessions[selected])
	}
}

func (d *Desktop) loadSession(dir string) {
	var session Session
	if readJSON(filepath.Join(dir, "sesja.json"), &session) != nil {
		return
	}
	d.session, d.selected = session, dir
	setText(d.controls[203], session.Status)
	d.toggle(d.busy)
	d.details()
	d.invalidate()
}

func (d *Desktop) details() {
	var list strings.Builder
	if d.viewRank {
		fmt.Fprintf(&list, "%-16s %5s %8s %8s %8s %9s %8s %8s   %s\r\n", "Profil", "Próby", "Śr. FPS", "1% Low", "0,1% Low", "Zegar MHz", "Wah. MHz", "GPU mV", "Status")
		ranks := slices.Clone(d.session.Ranking)
		sort.SliceStable(ranks, func(i, j int) bool {
			if ranks[i].UV == d.session.Winner && d.session.Winner > 0 {
				return true
			}
			if ranks[j].UV == d.session.Winner && d.session.Winner > 0 {
				return false
			}
			if ranks[i].Accepted != ranks[j].Accepted {
				return ranks[i].Accepted
			}
			return ranks[i].FPS > ranks[j].FPS
		})
		for _, p := range ranks {
			note := p.Phase
			if !p.Accepted && p.Count > 0 {
				note += " · odrzucony"
			}
			fmt.Fprintf(&list, "%-16s %5d %8.2f %8.2f %8.2f %9.0f %8.0f %8.1f   %s\r\n", voltageLabel(p.UV), p.Count, p.FPS, p.Low1, p.Low01, p.CoreMHz, p.ClockSpread, p.Volts, note)
		}
	} else {
		fmt.Fprintf(&list, "%-3s %-18s %-15s %8s %8s %8s %8s %8s %7s %8s %8s %6s %7s\r\n",
			"Nr", "Etap", "Profil", "Śr. FPS", "1% Low", "0,1% Low", "P99 ms", "SD ms", "MHz", "GPU mV", "Wat", "GPU °C", "HS °C")
		for i, p := range d.session.Runs {
			fmt.Fprintf(&list, "%-3d %-18s %-15s %8.2f %8.2f %8.2f %8.2f %8.2f %7.0f %8.1f %8.1f %6.0f %7s\r\n",
				i+1, p.Stage, voltageLabel(p.UV), p.FPS, p.Low1, p.Low01, p.FrametimeP99, p.FrametimeSD, p.CoreMedian, p.VoltageMedian, p.WattsMedian, p.TempMax,
				func() string {
					if p.HotSpotMax == 0 {
						return "—"
					}
					return fmt.Sprintf("%.1f", p.HotSpotMax)
				}())
			if p.Error != "" {
				fmt.Fprintf(&list, "    %s\r\n", p.Error)
			}
		}
	}
	if text := list.String(); text != d.historyText {
		d.historyText = text
		setText(d.controls[207], text)
	}
}

func (d *Desktop) beginPreview() {
	if d.busy || !d.session.Healthy || d.session.Winner <= 0 {
		return
	}
	if popup(d.window, "Zastosować zwycięski profil na maksymalnie 5 minut? MSI pozostanie uruchomiony. Po upływie czasu lub kliknięciu STOP aplikacja przywróci oryginalny OC.", 0x124) != 6 {
		return
	}
	cfg := defaultConfig()
	if err := readJSON(filepath.Join(d.root, "konfiguracja.json"), &cfg); err != nil {
		popup(d.window, err.Error(), 0x30)
		return
	}
	session, dir := d.session, d.selected
	cfg.Slot = session.MSISlot
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.previewing = true
	d.toggle(true)
	setText(d.controls[203], "Sprawdzanie zgodności oryginalnego profilu z zapisem sesji…")
	go func() {
		armed := false
		err := previewWinner(ctx, d.root, dir, cfg, session,
			func(s Sample) { d.mu.Lock(); d.pendingSamples = append(d.pendingSamples, s); d.mu.Unlock() },
			func(status string) { d.mu.Lock(); d.pendingStatus = status; d.mu.Unlock() }, &armed,
		)
		if armed {
			err = d.completeRecovery(err, func(status string) error {
				d.mu.Lock()
				d.pendingStatus = status
				d.mu.Unlock()
				return nil
			})
		}
		d.mu.Lock()
		d.pendingErr = err
		d.finished = true
		d.mu.Unlock()
	}()
}

func (d *Desktop) showWinner() {
	if d.busy || !d.session.Healthy || d.session.Winner <= 0 {
		return
	}
	var base, best Rank
	for _, p := range d.session.Ranking {
		if p.UV == 0 {
			base = p
		}
		if p.UV == d.session.Winner {
			best = p
		}
	}
	question := fmt.Sprintf("Oryginalny OC: %.2f FPS, %.2f (1%% Low), %.2f (0,1%% Low).\r\nWybrany UV: %.2f FPS, %.2f (1%% Low), %.2f (0,1%% Low).\r\nMediana napięcia w scenie: %.1f → %.1f mV.\r\n\r\nOryginalny profil pozostaje włączony. Czy otworzyć folder z krzywą i wynikami?",
		base.FPS, base.Low1, base.Low01, best.FPS, best.Low1, best.Low01, base.Volts, best.Volts)
	if popup(d.window, question, 0x124) != 6 {
		return
	}
	target := d.selected
	shell.NewProc("ShellExecuteW").Call(d.window, uintptr(unsafe.Pointer(wide("open"))), uintptr(unsafe.Pointer(wide(target))), 0, 0, 1)
}

func (d *Desktop) importWinner() {
	if d.busy || !d.session.Healthy || d.session.Winner <= 0 {
		return
	}
	lease, err := singleInstance()
	if err != nil {
		popup(d.window, err.Error(), 0x30)
		return
	}
	defer closeHandle(lease)
	msi := strings.Trim(strings.TrimSpace(windowText(d.controls[msiEdit])), "\"")
	plan, err := prepareMSIImport(d.root, d.selected, msi, d.session)
	if err != nil {
		popup(d.window, err.Error(), 0x30)
		return
	}
	question := fmt.Sprintf("Zapisać zwycięską krzywą UV w wolnym slocie %d MSI Afterburner? Slot %d zachowa oryginalny OC. Zapis nie zastosuje UV i nie włączy go przy starcie Windows.", plan.slot, d.session.MSISlot)
	if popup(d.window, question, 0x124) != 6 {
		return
	}
	backup, err := plan.commit()
	if err != nil {
		body := err.Error()
		if backup != "" {
			body += "\r\n\r\nKopia sprzed zapisu\r\n" + backup
		}
		popup(d.window, body, 0x30)
		return
	}
	status := fmt.Sprintf("Zapisano UV w slocie MSI %d. Uruchom MSI i wybierz ten slot, gdy zechcesz zastosować UV.", plan.slot)
	setText(d.controls[203], status)
	popup(d.window, status, 0x40)
}
