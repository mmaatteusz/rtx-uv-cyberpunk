package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"unsafe"
)

func color(red, green, blue uint32) uintptr { return uintptr(red | green<<8 | blue<<16) }

func fill(dc uintptr, r Rect, c uintptr) {
	brush, _, _ := gdi.NewProc("CreateSolidBrush").Call(c)
	user32.NewProc("FillRect").Call(dc, uintptr(unsafe.Pointer(&r)), brush)
	gdi.NewProc("DeleteObject").Call(brush)
}

func line(dc uintptr, x, y, x2, y2 int32) {
	gdi.NewProc("MoveToEx").Call(dc, uintptr(x), uintptr(y), 0)
	gdi.NewProc("LineTo").Call(dc, uintptr(x2), uintptr(y2))
}

func label(dc uintptr, x, y int32, text string) {
	chars := wide(text)
	gdi.NewProc("TextOutW").Call(dc, uintptr(x), uintptr(y), uintptr(unsafe.Pointer(chars)), uintptr(len([]rune(text))))
}

type plotValue struct {
	Number float64
	Valid  bool
}

func (d *Desktop) plot(metric int) ([]plotValue, string) {
	if metric == 6 {
		values := make([]plotValue, len(d.session.Runs))
		for i, r := range d.session.Runs {
			values[i] = plotValue{r.FPS, r.FPS > 0 && r.Error == ""}
		}
		return values, "Kolejne benchmarki"
	}
	if metric == 7 {
		for i := len(d.session.Runs) - 1; i >= 0; i-- {
			result := d.session.Runs[i]
			if result.Error != "" || result.FrameCount < 1 {
				continue
			}
			var summary reportedFrames
			summary, e := summaryFrom(filepath.Join(result.Path, "summary.json"))
			if e != nil {
				break
			}
			file, e := os.Open(filepath.Join(result.Path, "frames.csv"))
			if e != nil {
				break
			}
			frames, e := computeFrames(file, summary, &Result{})
			file.Close()
			if e != nil {
				break
			}
			width := max(1, len(frames)/400)
			bins := make([]plotValue, 0, 405)
			for j := 0; j < len(frames); j += width {
				// Keep frame-time spikes visible when downsampling for the plot.
				bins = append(bins, plotValue{slices.Max(frames[j:min(j+width, len(frames))]), true})
			}
			return bins, fmt.Sprintf("Benchmark %d · maksimum w grupach po %d klatek", i+1, width)
		}
		return nil, "Brak kompletnego pliku klatek"
	}
	values := make([]plotValue, len(d.series))
	for i, s := range d.series {
		var source *float64
		switch metric {
		case 0:
			source = s.CoreMHz
		case 1:
			source = s.Millivolts
		case 2:
			source = s.TempC
		case 3:
			source = s.HotSpotC
		case 4:
			source = s.Watts
		case 5:
			source = s.LoadPct
		}
		if source != nil {
			values[i] = plotValue{*source, true}
		}
	}
	return values, "Ostatnie 90 s telemetrii · próbkowanie co 0,5 s"
}

func (d *Desktop) draw(window uintptr) {
	var paint paintData
	dc, _, _ := user32.NewProc("BeginPaint").Call(window, uintptr(unsafe.Pointer(&paint)))
	defer user32.NewProc("EndPaint").Call(window, uintptr(unsafe.Pointer(&paint)))
	if d.chart.Right <= d.chart.Left || d.chart.Bottom <= d.chart.Top {
		return
	}
	selectObject := gdi.NewProc("SelectObject")
	deleteObject := gdi.NewProc("DeleteObject")
	width, height := d.chart.Right-d.chart.Left, d.chart.Bottom-d.chart.Top
	memory, _, _ := gdi.NewProc("CreateCompatibleDC").Call(dc)
	bitmap, _, _ := gdi.NewProc("CreateCompatibleBitmap").Call(dc, uintptr(width), uintptr(height))
	oldBitmap, _, _ := selectObject.Call(memory, bitmap)
	defer func() {
		selectObject.Call(memory, oldBitmap)
		deleteObject.Call(bitmap)
		gdi.NewProc("DeleteDC").Call(memory)
	}()
	font, _, _ := selectObject.Call(memory, d.font)
	defer selectObject.Call(memory, font)
	fill(memory, Rect{0, 0, width, height}, color(14, 25, 39))
	gdi.NewProc("SetBkMode").Call(memory, 1)
	gdi.NewProc("SetTextColor").Call(memory, color(181, 200, 221))
	metric := int(send(d.controls[metricPick], 0x147, 0, 0))
	values, caption := d.plot(metric)
	label(memory, d.px(15), d.px(10), caption)
	low, high := math.Inf(1), math.Inf(-1)
	for _, v := range values {
		if v.Valid {
			low = math.Min(low, v.Number)
			high = math.Max(high, v.Number)
		}
	}
	if math.IsInf(low, 1) {
		label(memory, d.px(15), d.px(44), "Brak danych dla tej metryki.")
	} else {
		minPadding := max(1, (high-low)*.1)
		low = max(0, low-minPadding)
		high += minPadding
		left, right, top, bottom := d.px(64), width-d.px(16), d.px(38), height-d.px(12)
		createPen := gdi.NewProc("CreatePen")
		grid, _, _ := createPen.Call(0, 1, color(43, 65, 88))
		defaultPen, _, _ := selectObject.Call(memory, grid)
		for i := int32(0); i < 4; i++ {
			y := top + (bottom-top)*i/3
			line(memory, left, y, right, y)
			n := high - (high-low)*float64(i)/3
			label(memory, d.px(11), y-d.px(8), fmt.Sprintf("%.1f", n))
		}
		series, _, _ := createPen.Call(0, uintptr(d.px(2)), color(96, 226, 206))
		selectObject.Call(memory, series)
		var lastX, lastY int32
		validLast := false
		for i, v := range values {
			if !v.Valid {
				validLast = false
				continue
			}
			x := left + (right-left)*int32(i)/int32(max(1, len(values)-1))
			y := bottom - int32((v.Number-low)/(high-low)*float64(bottom-top))
			if validLast {
				line(memory, lastX, lastY, x, y)
			} else {
				line(memory, x, y, x+2, y)
			}
			lastX, lastY, validLast = x, y, true
		}
		selectObject.Call(memory, defaultPen)
		deleteObject.Call(grid)
		deleteObject.Call(series)
	}
	gdi.NewProc("BitBlt").Call(dc, uintptr(d.chart.Left), uintptr(d.chart.Top), uintptr(width), uintptr(height), memory, 0, 0, 0x00CC0020)
}
