package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDriverTableDecoder(t *testing.T) {
	mask, curve, table := make([]byte, maskSize), make([]byte, curveSize), make([]byte, tableSize)
	put(mask, 0, version(maskSize, 1))
	put(curve, 0, version(curveSize, 1))
	put(table, 0, version(tableSize, 1))
	for i := 0; i < 70; i++ {
		mask[4+i/8] |= 1 << uint(i%8)
		base := 0x44 + i*28
		put(curve, base+4, uint32(1700000+i*16000))
		put(curve, base+8, uint32(600000+i*6250))
		put(table, tableDelta(i), uint32(120000+i*300))
	}
	core, err := decodeCurve(mask, curve, table)
	if err != nil || len(core) != 70 || core[50].DeltaKHz != 135000 {
		t.Fatalf("curve: %d points, %v", len(core), err)
	}
	mask[4+32/8] &^= 1 << uint(32%8)
	if _, err = decodeCurve(mask, curve, table); err == nil {
		t.Fatal("accepted a gap in an otherwise continuous curve")
	}
}

func TestOnlyFreshCompleteBenchmarkIsAccepted(t *testing.T) {
	root := t.TempDir()
	makeDir := func(name string, complete bool) {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"Data":{"averageFps":90,"frameNumber":5400,"time":60}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if complete {
			if err := os.WriteFile(filepath.Join(dir, "frames.csv"), []byte("Frame,FrameTime\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	makeDir("benchmark_old", true)
	before, err := records(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	makeDir("benchmark_partial", false)
	if _, err := latestNew(root, before, now); err == nil {
		t.Fatal("accepted incomplete export")
	}
	makeDir("benchmark_current", true)
	dir, err := latestNew(root, before, now)
	if err != nil || filepath.Base(dir) != "benchmark_current" {
		t.Fatalf("unexpected result %q / %v", dir, err)
	}
	s, err := summaryFrom(filepath.Join(dir, "summary.json"))
	if err != nil || s.Count != 5400 || s.FPS != 90 {
		t.Fatalf("summary: %+v / %v", s, err)
	}
}
