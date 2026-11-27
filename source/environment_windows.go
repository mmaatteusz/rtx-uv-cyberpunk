package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type systemCounters struct{ Idle, Kernel, User uint64 }

func counters() (systemCounters, error) {
	var v systemCounters
	ok, _, err := kernel.NewProc("GetSystemTimes").Call(
		uintptr(unsafe.Pointer(&v.Idle)), uintptr(unsafe.Pointer(&v.Kernel)), uintptr(unsafe.Pointer(&v.User)))
	if ok == 0 {
		return v, err
	}
	return v, nil
}

func busyFraction(before, after systemCounters) float64 {
	total := (after.Kernel - before.Kernel) + (after.User - before.User)
	idle := after.Idle - before.Idle
	if total == 0 || idle > total {
		return 1
	}
	return 1 - float64(idle)/float64(total)
}

func checkBenchmarkInterference() error {
	match, err := firstRunningProcess([]string{"GPUTweakIII.exe", "GPUTweakII.exe", "PrecisionX1.exe", "PrecisionX_x64.exe",
		"OCCT.exe", "FurMark.exe", "FurMark_GUI.exe", "nvidiaProfileInspector.exe"})
	if err != nil {
		return err
	}
	if match != "" {
		return fmt.Errorf("wykryto program wpływający na pomiar: %s", match)
	}
	return nil
}

func (e *Engine) waitComparable(ctx context.Context, r *Result) error {
	active, err := processExists("Cyberpunk2077.exe")
	if err != nil {
		return err
	}
	if active {
		return errors.New("zamknij Cyberpunka, zanim aplikacja zacznie stabilizować temperaturę")
	}
	prev, err := counters()
	if err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	timeout := time.NewTimer(7 * time.Minute)
	defer timeout.Stop()
	lastStatus := time.Time{}
	var recentTemp []float64
	var settleUntil time.Time
	if r.Stage != warmupStage && e.startTemp == 0 && e.session.Mode != "Dry Run" {
		settleUntil = time.Now().Add(45 * time.Second)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			if e.startTemp == 0 {
				return errors.New("przez 7 min nie uzyskano stabilnej temperatury i bezczynności CPU oraz GPU")
			}
			return fmt.Errorf("przez 7 min nie uzyskano stabilnych warunków startowych wokół %.1f °C (GPU i CPU w bezczynności)", e.startTemp)
		case at := <-tick.C:
			if err := e.guard(false); err != nil {
				return err
			}
			s, err := e.gpu.sample(e.armed && !e.recovery.ReadOnly)
			if err != nil {
				return fmt.Errorf("błąd telemetrii przed testem: %w", err)
			}
			if err := checkThermalStop(s); err != nil {
				return err
			}

			if r.UV > 0 {
				if err := withinOCVoltage(s, e.session.OCCeilingMV); err != nil {
					return err
				}
			}
			e.live(s)
			now, err := counters()
			if err != nil {
				return err
			}
			cpu := busyFraction(prev, now) * 100
			prev = now
			temp, load := *s.TempC, *s.LoadPct
			if s.CoreMHz == nil || temp < 0 || temp > 95 || load < 0 || load > 100 {
				return errors.New("niespójna telemetria temperatury lub obciążenia")
			}
			if time.Since(lastStatus) > 4*time.Second {
				message := fmt.Sprintf("%s · oczekiwanie na bezczynność · %.0f °C · CPU %.0f%% · GPU %.0f%%", r.Stage, temp, cpu, load)
				if e.startTemp > 0 {
					message += fmt.Sprintf(" · cel %.1f ±2 °C", e.startTemp)
				} else if !settleUntil.IsZero() {
					message += " · ustalanie temperatury po rozgrzewce"
				}
				e.session.Status = message
				e.update(e.session)
				lastStatus = time.Now()
			}
			if load > 12 || cpu > 12 || at.Before(settleUntil) {
				recentTemp = nil
				continue
			}
			recentTemp = append(recentTemp, temp)
			if len(recentTemp) > 5 {
				recentTemp = recentTemp[1:]
			}
			target, ok := benchmarkStartTemperature(recentTemp, e.startTemp)
			if !ok {
				continue
			}
			if r.Stage != warmupStage {
				e.startTemp = target
			}
			r.StartTempC, r.StartCPUPct, r.StartGPUPct = temp, cpu, load
			return nil
		}
	}
}

var eventAPI = syscall.NewLazyDLL("wevtapi.dll")

func driverEvent(since time.Time) (string, error) {
	ms := time.Since(since).Milliseconds()
	if ms <= 0 {
		return "", nil
	}
	query := fmt.Sprintf("*[System[TimeCreated[timediff(@SystemTime) <= %d] and ((Provider[@Name='Display'] and EventID=4101) or (Provider[@Name='nvlddmkm'] and (Level=1 or Level=2 or Level=3 or EventID=13 or EventID=14 or EventID=153)))]]", ms)
	closeEvent := eventAPI.NewProc("EvtClose")
	h, _, err := eventAPI.NewProc("EvtQuery").Call(0, uintptr(unsafe.Pointer(wide("System"))), uintptr(unsafe.Pointer(wide(query))), 0x1|0x200)
	if h == 0 {
		return "", err
	}
	defer closeEvent.Call(h)
	var event uintptr
	var count uint32
	ok, _, err := eventAPI.NewProc("EvtNext").Call(h, 1, uintptr(unsafe.Pointer(&event)), 500, 0, uintptr(unsafe.Pointer(&count)))
	if ok == 0 {
		if err == syscall.Errno(259) {
			return "", nil
		}
		return "", err
	}
	if count != 1 || event == 0 {
		return "", errors.New("niejednoznaczny odczyt zdarzeń sterownika")
	}
	defer closeEvent.Call(event)
	buf := make([]uint16, 32768)
	var used, properties uint32
	ok, _, err = eventAPI.NewProc("EvtRender").Call(0, event, 1, uintptr(len(buf)*2), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&properties)))
	if ok == 0 {
		return "", err
	}
	return syscall.UTF16ToString(buf), nil
}

func captureDriverEvent(dir string, since time.Time) error {
	xml, err := driverEvent(since)
	if err != nil {
		return fmt.Errorf("nie udało się sprawdzić dziennika zdarzeń sterownika: %w", err)
	}
	if strings.TrimSpace(xml) == "" {
		return nil
	}
	_ = os.WriteFile(dir, []byte(xml), 0600)
	return errors.New("dziennik Windows zgłosił błąd nvlddmkm lub TDR")
}
