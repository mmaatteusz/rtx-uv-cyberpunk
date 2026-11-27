package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Runner struct {
	game, gameResults, gameSettings, settingsHash, folder string
	gpu                                                   *GPU
	fullSensors                                           bool
	mark                                                  func() error
	live                                                  func(Sample)
}

func (b Runner) settingsOK() error {
	hash, err := hashPath(b.gameSettings)
	if err != nil {
		return fmt.Errorf("nie można odczytać ustawień gry: %w", err)
	}
	if b.settingsHash != hash {
		return errors.New("ustawienia gry zmieniły się podczas sesji")
	}
	return nil
}

func (b Runner) run(ctx context.Context, r *Result, ceilingMV float64) (retErr error) {
	if err := b.settingsOK(); err != nil {
		return err
	}
	active, err := processExists("Cyberpunk2077.exe")
	if err != nil {
		return err
	}
	if active {
		return errors.New("zamknij uruchomionego Cyberpunka przed testem")
	}
	if err = os.Mkdir(r.Path, 0700); err != nil {
		return err
	}
	logOut, err := os.Create(filepath.Join(r.Path, "proces-gry.log"))
	if err != nil {
		return err
	}
	defer logOut.Close()
	before, err := records(b.gameResults)
	if err != nil {
		return err
	}
	testCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	job, err := newJob()
	if err != nil {
		return err
	}
	defer func() { job.close() }()
	command := exec.CommandContext(testCtx, b.game, "-benchmark")
	command.Dir = filepath.Dir(b.game)
	command.Stdout, command.Stderr = logOut, logOut
	started := time.Now()
	if err = command.Start(); err != nil {
		return err
	}
	if err = job.assign(command.Process.Pid); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return fmt.Errorf("nie udało się objąć gry nadzorem procesu: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	var values []Sample
	defer func() {
		if !waited {
			// End the game job, including child processes, before persisting logs.
			job.close()
			job = Job{}
			_ = command.Process.Kill()
			<-done
		}
		file, err := os.Create(filepath.Join(r.Path, "telemetria.csv"))
		if err == nil {
			err = recordTelemetry(file, values)
			_ = file.Close()
		}
		if retErr == nil && err != nil {
			retErr = err
		}
	}()

	eventPoll := time.NewTicker(2 * time.Second)
	defer eventPoll.Stop()
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	var end time.Time
	lowSince := time.Time{}
	hadLoad := false
runLoop:
	for {
		select {
		case err = <-done:
			waited = true
			end = time.Now()
			break runLoop
		case <-testCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("Cyberpunk przekroczył 4 minuty (możliwe zawieszenie)")
		case <-eventPoll.C:
			if watchErr := captureDriverEvent(filepath.Join(r.Path, "zdarzenia-GPU.xml"), started); watchErr != nil {
				return watchErr
			}
		case <-timer.C:
			if markErr := b.mark(); markErr != nil {
				return markErr
			}
			s, readErr := b.gpu.sample(b.fullSensors)
			values = append(values, s)
			b.live(s)
			if readErr != nil {
				return fmt.Errorf("utrata telemetrii sterownika: %v", readErr)
			}
			r.VoltageMax = max(r.VoltageMax, *s.Millivolts)
			if r.UV > 0 {
				if err := withinOCVoltage(s, ceilingMV); err != nil {
					return err
				}
			}
			if *s.CoreMHz < 100 || *s.CoreMHz > 3600 ||
				*s.Millivolts < 300 || *s.Millivolts > 1300 || *s.Watts < 0 || *s.Watts > 500 {
				return errors.New("nieprawidłowy odczyt zegara, napięcia, mocy lub temperatury")
			}
			if err := checkThermalStop(s); err != nil {
				return err
			}
			if *s.LoadPct >= 65 {
				hadLoad = true
				lowSince = time.Time{}
			} else if hadLoad && lowSince.IsZero() {
				lowSince = s.Time
			}
			if !lowSince.IsZero() && time.Since(lowSince) > 40*time.Second {
				return errors.New("gra nie wróciła do obciążenia GPU przez 40 sekund")
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if testCtx.Err() != nil {
		return errors.New("Cyberpunk przekroczył limit czasu")
	}
	if watchErr := captureDriverEvent(filepath.Join(r.Path, "zdarzenia-GPU.xml"), started); watchErr != nil {
		return watchErr
	}
	if err != nil {
		return fmt.Errorf("Cyberpunk zakończył się błędem: %v", err)
	}
	if err = b.settingsOK(); err != nil {
		return err
	}
	newFolder, err := latestNew(b.gameResults, before, started)
	if err != nil {
		return err
	}
	for _, name := range []string{"summary.json", "frames.csv"} {
		if err = copyPath(filepath.Join(newFolder, name), filepath.Join(r.Path, name)); err != nil {
			return err
		}
	}
	summary, err := summaryFrom(filepath.Join(r.Path, "summary.json"))
	if err != nil {
		return err
	}
	frames, err := os.Open(filepath.Join(r.Path, "frames.csv"))
	if err != nil {
		return err
	}
	defer frames.Close()
	if _, err = computeFrames(frames, summary, r); err != nil {
		return err
	}
	fileInfo, err := os.Stat(filepath.Join(newFolder, "frames.csv"))
	if err != nil {
		return err
	}
	exported := fileInfo.ModTime()
	if exported.Before(started.Add(time.Duration(r.Seconds*float64(time.Second)))) || exported.After(end) {
		return errors.New("znacznik czasu eksportu nie pozwala przypisać telemetrii do sceny")
	}
	return telemetryMetrics(values, exported, r)
}
