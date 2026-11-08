package main

import "sort"

// lightAt is how often the taskbar's brightness is re-read; the bar can change
// under us when Windows swaps between a light and a dark theme.
const lightAt = 3000

// taskbarLight reports whether the surface under the lyrics is a light one, so
// the auto colour can pick dark type for it instead of white on white.
//
// The columns our own window covers are skipped: the answer must describe the
// bar, not the frame we painted on top of it last time.
func (a *app) taskbarLight() (bool, bool) {
	pix, w, h, ok := stripPixels(a.tb)
	if !ok {
		return false, false
	}
	skip0, skip1 := int32(-1), int32(-1)
	if a.visible {
		skip0 = a.winX - a.tb.left - 2
		skip1 = a.winX - a.tb.left + a.winW + 2
	}
	vals := make([]int32, 0, 1024)
	for y := int32(3); y < h-3; y += 4 {
		base := int(y) * int(w) * 4
		for x := int32(0); x < w; x += 4 {
			if x >= skip0 && x <= skip1 {
				continue
			}
			i := base + int(x)*4
			vals = append(vals, (299*int32(pix[i+2])+587*int32(pix[i+1])+114*int32(pix[i]))/1000)
		}
	}
	if len(vals) < 16 {
		return false, false
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	median := vals[len(vals)/2]
	if verbose {
		logf("luma: median=%d light=%v samples=%d", median, median >= 128, len(vals))
	}
	return median >= 128, true
}
