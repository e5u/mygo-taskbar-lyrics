package main

import (
	"sort"
	"unsafe"
)

const (
	srcCopy    = 0x00CC0020
	spreadPx   = 4
	minGapPx   = 64
	spreadInk  = 22
	brightInk  = 55
	brightStep = 3
)

// band is a stretch of the taskbar with nothing drawn in it, in screen pixels.
type band struct {
	x0, x1 int32
}

// stripPixels copies the taskbar strip into a plain slice. The GDI objects a
// capture needs are released before it returns, so callers never hold a DC.
func stripPixels(tb rect32) ([]byte, int32, int32, bool) {
	w, h := tb.right-tb.left, tb.bottom-tb.top
	if w < 8 || h < 8 {
		return nil, 0, 0, false
	}
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return nil, 0, 0, false
	}
	defer procDeleteDC.Call(dc)
	bi := bitmapInfoHeader{biSize: 40, biWidth: w, biHeight: -h, biPlanes: 1, biBitCount: 32}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return nil, 0, 0, false
	}
	defer procDeleteObject.Call(bmp)
	procSelectObject.Call(dc, bmp)

	src, _, _ := procGetDC.Call(0)
	if src == 0 {
		return nil, 0, 0, false
	}
	ok, _, _ := procBitBlt.Call(dc, 0, 0, uintptr(w), uintptr(h), src,
		uintptr(tb.left), uintptr(tb.top), srcCopy)
	procReleaseDC.Call(0, src)
	if ok == 0 {
		return nil, 0, 0, false
	}
	view := unsafe.Slice((*byte)(bits), int(w)*int(h)*4)
	pix := make([]byte, len(view))
	copy(pix, view)
	return pix, w, h, true
}

// freeBands reads the taskbar strip and returns the stretches with nothing on
// them, left to right. It answers what the taskbar will not: where does nothing
// sit, so the lyrics do not cover a button.
func freeBands(tb rect32, skipX0, skipX1 int32) []band {
	w, h := tb.right-tb.left, tb.bottom-tb.top
	if w < minGapPx || h < 8 {
		return nil
	}
	pix, w, h, ok := stripPixels(tb)
	if !ok {
		return nil
	}
	spread := make([]int32, w)
	bright := make([]int32, w)
	rows := int32(0)
	for y := int32(4); y < h-4; y += brightStep {
		base := int(y) * int(w) * 4
		for x := int32(0); x < w; x++ {
			i := base + int(x)*4
			b, g, r := int32(pix[i]), int32(pix[i+1]), int32(pix[i+2])
			mx, mn := r, r
			if g > mx {
				mx = g
			}
			if b > mx {
				mx = b
			}
			if g < mn {
				mn = g
			}
			if b < mn {
				mn = b
			}
			spread[x] += mx - mn
			bright[x] += mx
		}
		rows++
	}
	if rows == 0 {
		return nil
	}
	for x := int32(0); x < w; x++ {
		spread[x] /= rows
		bright[x] /= rows
	}
	sorted := append([]int32(nil), bright...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	median := sorted[len(sorted)/2]

	// A column holds something if it is coloured or brighter than the bar's
	// background; the acrylic blur means single columns are never the whole story.
	ink := make([]bool, w)
	for x := int32(0); x < w; x++ {
		ink[x] = spread[x] > spreadInk || bright[x] > median+brightInk
	}
	// The strip's own window is in the bitmap while this runs. Its columns say
	// nothing about the taskbar, so they are dropped from the mask instead of the
	// window being hidden for a frame, which is a visible blink every scan.
	if skipX1 > skipX0 {
		x0, x1 := skipX0-tb.left, skipX1-tb.left
		if x0 < 0 {
			x0 = 0
		}
		if x1 > w {
			x1 = w
		}
		for x := x0; x < x1; x++ {
			ink[x] = false
		}
	}
	// Widen each hit so a button's padding and a glyph's edge count as occupied.
	// From a copy, or the widening feeds on itself and swallows the whole bar.
	busy := make([]bool, w)
	copy(busy, ink)
	for x := int32(0); x < w; x++ {
		if !ink[x] {
			continue
		}
		for k := x - spreadPx; k <= x+spreadPx; k++ {
			if k >= 0 && k < w {
				busy[k] = true
			}
		}
	}

	var bands []band
	for x := int32(0); x < w; {
		if busy[x] {
			x++
			continue
		}
		start := x
		for x < w && !busy[x] {
			x++
		}
		if x-start >= minGapPx {
			bands = append(bands, band{x0: tb.left + start, x1: tb.left + x})
		}
	}
	if verbose {
		n := 0
		for _, v := range busy {
			if v {
				n++
			}
		}
		logf("bands: busy=%d of %d, %d free: %v", n, w, len(bands), bands)
	}
	return bands
}
