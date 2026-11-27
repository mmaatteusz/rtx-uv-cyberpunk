package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type GPU struct {
	nv          *syscall.DLL
	ml          *syscall.DLL
	query       *syscall.Proc
	gpu, device uintptr
	id          Identity
	fns         map[uint32]uintptr
}

func zeroString(b []byte) string { return strings.TrimRight(string(b), "\x00") }

func openGPU() (*GPU, error) {
	sys := system32()
	nv, err := syscall.LoadDLL(filepath.Join(sys, "nvapi64.dll"))
	if err != nil {
		return nil, fmt.Errorf("nie znaleziono NVAPI x64 sterownika NVIDIA: %w", err)
	}
	g := &GPU{nv: nv, fns: map[uint32]uintptr{}}
	fail := func(err error) (*GPU, error) { g.close(); return nil, err }
	g.query, err = nv.FindProc("nvapi_QueryInterface")
	if err != nil {
		return fail(err)
	}
	if err = g.call(0x0150E828); err != nil {
		return fail(err)
	}
	var handles [64]uintptr
	var n int32
	if err = g.call(0xE5AC921F, uintptr(unsafe.Pointer(&handles)), uintptr(unsafe.Pointer(&n))); err != nil {
		return fail(err)
	}
	if n != 1 {
		return fail(fmt.Errorf("obsługiwany jest jeden fizyczny GPU NVIDIA, wykryto %d", n))
	}
	g.gpu = handles[0]
	var name, vbios, branch [64]byte
	var device, board, rev, ext, bus, drv uint32
	if err = g.call(0xCEEE8E9F, g.gpu, uintptr(unsafe.Pointer(&name))); err != nil {
		return fail(err)
	}
	if !strings.HasSuffix(strings.ToUpper(zeroString(name[:])), "RTX 4070 SUPER") {
		return fail(fmt.Errorf("wymagana RTX 4070 SUPER, wykryto %s", zeroString(name[:])))
	}
	if err = g.call(0x2DDFB66E, g.gpu, uintptr(unsafe.Pointer(&device)), uintptr(unsafe.Pointer(&board)), uintptr(unsafe.Pointer(&rev)), uintptr(unsafe.Pointer(&ext))); err != nil {
		return fail(err)
	}
	if err = g.call(0x1BE0B8E5, g.gpu, uintptr(unsafe.Pointer(&bus))); err != nil {
		return fail(err)
	}
	if err = g.call(0x2926AAAD, uintptr(unsafe.Pointer(&drv)), uintptr(unsafe.Pointer(&branch))); err != nil {
		return fail(err)
	}
	if err = g.call(0xA561FD7D, g.gpu, uintptr(unsafe.Pointer(&vbios))); err != nil {
		return fail(err)
	}
	for _, path := range []string{filepath.Join(sys, "nvml.dll"), filepath.Join(os.Getenv("ProgramFiles"), "NVIDIA Corporation", "NVSMI", "nvml.dll")} {
		if !filepath.IsAbs(path) {
			continue
		}
		g.ml, err = syscall.LoadDLL(path)
		if err == nil {
			break
		}
	}
	if g.ml == nil {
		return fail(errors.New("nie znaleziono NVIDIA NVML x64"))
	}
	if err = g.mlcall("nvmlInit_v2"); err != nil {
		return fail(err)
	}
	var count uint32
	if err = g.mlcall("nvmlDeviceGetCount_v2", uintptr(unsafe.Pointer(&count))); err != nil {
		return fail(err)
	}
	if count != 1 {
		return fail(fmt.Errorf("NVML wykrył %d urządzeń zamiast 1", count))
	}
	if err = g.mlcall("nvmlDeviceGetHandleByIndex_v2", 0, uintptr(unsafe.Pointer(&g.device))); err != nil {
		return fail(err)
	}
	var pci [68]byte // nvmlPciInfo_t
	if err = g.mlcall("nvmlDeviceGetPciInfo_v3", g.device, uintptr(unsafe.Pointer(&pci))); err != nil {
		return fail(err)
	}
	if bus != u32(pci[:], 20) || device != u32(pci[:], 28) || board != u32(pci[:], 32) {
		return fail(errors.New("NVAPI i NVML wskazują różne urządzenia PCI"))
	}
	var uuid [96]byte
	if err = g.mlcall("nvmlDeviceGetUUID", g.device, uintptr(unsafe.Pointer(&uuid)), uintptr(len(uuid))); err != nil {
		return fail(err)
	}
	if !strings.HasPrefix(zeroString(uuid[:]), "GPU-") {
		return fail(errors.New("nieprawidłowy UUID GPU"))
	}
	g.id = Identity{zeroString(name[:]), zeroString(uuid[:]), zeroString(vbios[:]), device, board, drv, bus}
	return g, nil
}

func (g *GPU) close() {
	if g.ml != nil {
		_ = g.mlcall("nvmlShutdown")
		_ = g.ml.Release()
	}
	if g.query != nil {
		_ = g.call(0xD22BDD7E)
	}
	if g.nv != nil {
		_ = g.nv.Release()
	}
}

func (g *GPU) address(id uint32) uintptr {
	addr, ok := g.fns[id]
	if !ok {
		addr, _, _ = g.query.Call(uintptr(id))
		g.fns[id] = addr
	}
	return addr
}

//go:uintptrescapes
func (g *GPU) call(id uint32, args ...uintptr) error {
	addr := g.address(id)
	if addr == 0 {
		return fmt.Errorf("brak funkcji NVAPI 0x%08X", id)
	}
	r, _, _ := syscall.SyscallN(addr, args...)
	if int32(r) != 0 {
		return fmt.Errorf("NVAPI 0x%08X zwróciło %d", id, int32(r))
	}
	return nil
}

//go:uintptrescapes
func (g *GPU) mlcall(name string, args ...uintptr) error {
	p, err := g.ml.FindProc(name)
	if err != nil {
		return err
	}
	r, _, _ := p.Call(args...)
	if int32(r) != 0 {
		return fmt.Errorf("%s zwróciło %d", name, int32(r))
	}
	return nil
}

func (g *GPU) nvbuf(id uint32, size, ver int, mask []byte) ([]byte, error) {
	buf := make([]byte, size)
	put(buf, 0, version(size, ver))
	copy(buf[4:], mask)
	err := g.call(id, g.gpu, uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(buf)
	return buf, err
}

func (g *GPU) snapshot() (Snapshot, error) {
	s := Snapshot{GPU: g.id, Time: time.Now()}
	var err error
	s.RawMask, err = g.nvbuf(getMask, maskSize, 1, nil)
	if err != nil {
		return s, err
	}
	mask := s.RawMask[4:36]
	for attempt := 0; ; attempt++ {
		s.RawCurve, err = g.nvbuf(getVF, curveSize, 1, mask)
		if err != nil {
			return s, err
		}
		s.RawTable, err = g.nvbuf(getTable, tableSize, 1, mask)
		if err != nil {
			return s, err
		}
		s.Points, err = decodeCurve(s.RawMask, s.RawCurve, s.RawTable)
		if err == nil {
			break
		}
		if attempt == 2 {
			return s, fmt.Errorf("sterownik NVIDIA %d.%02d: %w", g.id.Driver/100, g.id.Driver%100, err)
		}
		// A power-state transition may temporarily collapse the reported curve.
		// Repeat the status query without touching any GPU settings.
		time.Sleep(125 * time.Millisecond)
	}
	s.RawPstates, err = g.nvbuf(0x6FF81213, 7316, 1, nil)
	if err != nil {
		return s, err
	}
	s.GraphicsDeltas, s.MemoryDeltas = map[uint32]int{}, map[uint32]int{}
	s.MemoryLimits = map[uint32][2]int{}
	pstates, clocks := int(u32(s.RawPstates, 8)), int(u32(s.RawPstates, 12))
	if pstates < 1 || pstates > 16 || clocks < 1 || clocks > 8 {
		return s, errors.New("nieznany układ stanów P")
	}
	for i := 0; i < pstates; i++ {
		offset := 20 + i*456
		state := u32(s.RawPstates, offset)
		if state > 15 {
			return s, errors.New("nieznany identyfikator stanu P")
		}
		for j := 0; j < clocks; j++ {
			base := offset + 8 + j*44
			switch u32(s.RawPstates, base) {
			case 0:
				s.GraphicsDeltas[state] = i32(s.RawPstates, base+12)
				if state == 0 {
					s.MinDeltaKHz, s.MaxDeltaKHz = i32(s.RawPstates, base+16), i32(s.RawPstates, base+20)
				}
			case 4:
				s.MemoryDeltas[state] = i32(s.RawPstates, base+12)
				f := i32(s.RawPstates, base+24)
				if u32(s.RawPstates, base+4) == 0 {
					s.MemoryLimits[state] = [2]int{f, f}
				} else {
					s.MemoryLimits[state] = [2]int{f, i32(s.RawPstates, base+28)}
				}
			}
		}
	}
	if _, ok := s.MemoryDeltas[0]; !ok {
		return s, errors.New("brak odczytu offsetu pamięci w P0")
	}
	if clocks, e := g.nvbuf(0xDCB616C3, 264, 2, []byte{1, 0, 0, 0}); e == nil && u32(clocks, 40)&1 != 0 {
		s.FactoryMemoryKHz = i32(clocks, 44)
	}
	var p, def, minP, maxP uint32
	if err = g.mlcall("nvmlDeviceGetPowerManagementLimit", g.device, uintptr(unsafe.Pointer(&p))); err != nil {
		return s, err
	}
	if err = g.mlcall("nvmlDeviceGetPowerManagementDefaultLimit", g.device, uintptr(unsafe.Pointer(&def))); err != nil {
		return s, err
	}
	if err = g.mlcall("nvmlDeviceGetPowerManagementLimitConstraints", g.device, uintptr(unsafe.Pointer(&minP)), uintptr(unsafe.Pointer(&maxP))); err != nil {
		return s, err
	}
	s.PowerMW, s.FactoryPowerMW, s.MinPowerMW, s.MaxPowerMW = int(p), int(def), int(minP), int(maxP)
	if p == 0 || p < minP || p > maxP {
		return s, errors.New("nieprawidłowy limit mocy zwrócony przez NVML")
	}
	thermal, err := g.nvbuf(0x0E9C425A1, 56, 2, nil)
	if err != nil {
		return s, fmt.Errorf("nie można zweryfikować Temperature Limit: %w", err)
	}
	n := int(u32(thermal, 4))
	if n < 1 || n > 4 {
		return s, errors.New("sterownik nie podał poprawnej tabeli Temperature Limit")
	}
	s.ThermalPolicies = make([]uint32, 0, n*3)
	for i := 0; i < n*3; i++ {
		s.ThermalPolicies = append(s.ThermalPolicies, u32(thermal, 8+i*4))
	}
	s.FanPercent = g.mlNumber("nvmlDeviceGetFanSpeed", 1)
	if b, e := g.nvbuf(0x9DF23CA1, 40, 1, nil); e == nil && u32(b, 4) <= 100 {
		value := float64(u32(b, 4))
		s.VoltageBoostPercent = &value
	}
	return s, nil
}

func (g *GPU) apply(ctx context.Context, base Snapshot, target []Point, restore bool, permit func(Snapshot) error) (Snapshot, error) {
	live, err := g.snapshot()
	if err != nil {
		return live, err
	}
	payload, err := prepareCurveWrite(base, live, target, restore)
	if err != nil || len(payload) == 0 {
		return live, err
	}
	if !g.hasSetter() {
		return live, errors.New("sterownik nie udostępnia zapisu V/F")
	}
	if !restore {
		if permit == nil {
			return live, errors.New("brakuje zabezpieczenia zapisu przez watchdog")
		}
		if err = permit(live); err != nil {
			return live, err
		}
	}
	if err = ctx.Err(); err != nil {
		return live, err
	}

	// Change only selected graphics VF offsets through the NVIDIA driver.
	err = g.call(setTable, g.gpu, uintptr(unsafe.Pointer(&payload[0])))
	runtime.KeepAlive(payload)
	if err != nil {
		return live, err
	}
	time.Sleep(500 * time.Millisecond)
	newState, err := g.snapshot()
	if err != nil {
		return newState, err
	}
	return newState, verifyCurveResult(base, live, newState, target)

}

func (g *GPU) hasSetter() bool { return g.address(setTable) != 0 }

func (g *GPU) mlNumber(name string, scale float64, args ...uintptr) *float64 {
	var value uint32
	call := append([]uintptr{g.device}, args...)
	call = append(call, uintptr(unsafe.Pointer(&value)))
	var pinned runtime.Pinner
	pinned.Pin(&value)
	defer pinned.Unpin()
	if g.mlcall(name, call...) != nil {
		return nil
	}
	result := float64(value) / scale
	return &result
}

func (g *GPU) sample(requireFull bool) (Sample, error) {
	s := Sample{Time: time.Now()}
	clocks, err := g.nvbuf(0xDCB616C3, 264, 2, nil)
	if err != nil {
		return s, err
	}
	if u32(clocks, 8)&1 != 0 {
		v := float64(u32(clocks, 12)) / 1000
		s.CoreMHz = &v
	}
	if u32(clocks, 40)&1 != 0 {
		v := float64(u32(clocks, 44)) / 1000
		s.MemoryMHz = &v
	}
	volts, e := g.nvbuf(0x465F9BCF, 76, 1, nil)
	if e == nil && u32(volts, 40) >= 300000 && u32(volts, 40) <= 1300000 {
		v := float64(u32(volts, 40)) / 1000
		s.Millivolts = &v
	}
	var load [2]uint32
	if g.mlcall("nvmlDeviceGetUtilizationRates", g.device, uintptr(unsafe.Pointer(&load))) == nil && load[0] <= 100 {
		v := float64(load[0])
		s.LoadPct = &v
	}
	s.TempC = g.mlNumber("nvmlDeviceGetTemperature", 1, 0)
	s.Watts = g.mlNumber("nvmlDeviceGetPowerUsage", 1000)
	hot, e := g.nvbuf(0x65FE3AAD, 168, 2, []byte{3, 0, 0, 0})
	if e == nil {
		v := float64(i32(hot, 44)) / 256
		if v > 0 {
			s.HotSpotC = &v
		}
	}
	perf, perfErr := g.nvbuf(0x3D358A0C, 1360, 1, nil)
	if perfErr == nil {
		s.PerfCap = nvapiPerfCap(u32(perf, 16))
	} else {
		var reasons uint64
		e = g.mlcall("nvmlDeviceGetCurrentClocksEventReasons", g.device, uintptr(unsafe.Pointer(&reasons)))
		if e != nil {
			e = g.mlcall("nvmlDeviceGetCurrentClocksThrottleReasons", g.device, uintptr(unsafe.Pointer(&reasons)))
		}
		if e == nil {
			s.PerfCap = "NVML: " + clockReasons(reasons)
		}
	}
	if s.CoreMHz == nil || s.Millivolts == nil || s.LoadPct == nil || s.TempC == nil || s.Watts == nil {
		return s, errors.New("brak wymaganej telemetrii zegara, napięcia, obciążenia, temperatury lub mocy")
	}
	if requireFull {
		return s, verifyTuningSensors(s)
	}
	return s, nil
}

func nvapiPerfCap(mask uint32) string {
	if mask == 0 {
		return "Brak limitowania"
	}
	var active []string
	for i, name := range []string{"Moc", "Temperatura", "Napięcie", "Limit 0x08", "Małe obciążenie"} {
		if mask&(1<<uint(i)) != 0 {
			active = append(active, name)
		}
	}
	if unknown := mask &^ uint32(31); unknown != 0 {
		active = append(active, fmt.Sprintf("0x%x", unknown))
	}
	return strings.Join(active, " | ")
}

func clockReasons(mask uint64) string {
	names := []string{"Idle", "Application clocks", "SW Power Cap", "HW Slowdown", "Sync Boost", "SW Thermal", "HW Thermal", "HW Power Brake", "Display clocks"}
	if mask == 0 {
		return "Brak aktywnych zdarzeń"
	}
	var active []string
	for i, name := range names {
		if mask&(1<<uint(i)) != 0 {
			active = append(active, name)
		}
	}
	if unknown := mask &^ uint64(0x1ff); unknown != 0 {
		active = append(active, fmt.Sprintf("0x%x", unknown))
	}
	return strings.Join(active, " | ")
}
