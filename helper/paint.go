package main

import (
	"encoding/binary"
	"math"
	"strings"
	"syscall"
	"unsafe"
)

// canvas is a premultiplied BGRA bitmap ready for UpdateLayeredWindow.
type canvas struct {
	w, h int32
	pix  []byte
}

func newCanvas(w, h int32) *canvas {
	return &canvas{w: w, h: h, pix: make([]byte, int(w)*int(h)*4)}
}

// painter owns one 32bpp top-down DIB that is drawn into and then presented.
// GDI cannot write alpha, so each text run is drawn in a single colour channel
// and the coverage is turned into premultiplied pixels here.
type painter struct {
	dc, bmp, old syscall.Handle
	bits         []byte
	w, h         int32
}

func (p *painter) ensure(w, h int32) bool {
	if p.bmp != 0 && p.w == w && p.h == h {
		return true
	}
	p.release()
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return false
	}
	bi := bitmapInfoHeader{
		biSize:        40,
		biWidth:       w,
		biHeight:      -h,
		biPlanes:      1,
		biBitCount:    32,
		biCompression: 0,
	}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		procDeleteDC.Call(dc)
		return false
	}
	old, _, _ := procSelectObject.Call(dc, bmp)
	procSetBkMode.Call(dc, transparentBk)
	p.dc, p.bmp, p.old = syscall.Handle(dc), syscall.Handle(bmp), syscall.Handle(old)
	p.bits = unsafe.Slice((*byte)(bits), int(w)*int(h)*4)
	p.w, p.h = w, h
	return true
}

func (p *painter) release() {
	if p.dc == 0 {
		return
	}
	if p.old != 0 {
		procSelectObject.Call(uintptr(p.dc), uintptr(p.old))
	}
	if p.bmp != 0 {
		procDeleteObject.Call(uintptr(p.bmp))
	}
	procDeleteDC.Call(uintptr(p.dc))
	p.dc, p.bmp, p.old, p.bits, p.w, p.h = 0, 0, 0, nil, 0, 0
}

func (p *painter) text(s, face string, height, weight int32, r rect32, format uint32, color uint32) {
	if s == "" || p.dc == 0 || height <= 0 {
		return
	}
	font := newFont(face, height, weight)
	if font == 0 {
		return
	}
	textPtr := utf16p(s)
	if textPtr == nil {
		procDeleteObject.Call(font)
		return
	}
	prevFont, _, _ := procSelectObject.Call(uintptr(p.dc), font)
	prevColor, _, _ := procSetTextColor.Call(uintptr(p.dc), uintptr(color))
	procDrawTextW.Call(uintptr(p.dc), uintptr(unsafe.Pointer(textPtr)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)), uintptr(format))
	procSetTextColor.Call(uintptr(p.dc), prevColor)
	procSelectObject.Call(uintptr(p.dc), prevFont)
	procDeleteObject.Call(font)
}

// textFit is the two runs sized to the strip: the pixel heights of the fonts
// and the bands they get drawn in. A glyph's descender lives below the baseline,
// so a band has to be the font's full cell height or the tails of g, j and y are
// simply not on screen.
type textFit struct {
	main, trans        int32
	mainH, transH, gap int32
	top                int32
	showTrans          bool
}

// The reference project sets the lyric line in SemiBold and leaves the
// translation in the regular face: the pair reads as one line plus its footnote
// instead of two lines of equal weight fighting for the same attention.
const (
	mainWeight  = 600
	transWeight = 400
)

// fontCell is the height the face occupies at this size and weight, ascent and
// descent included, measured off the same face paint will draw with.
func (a *app) fontCell(s string, height, weight int32) int32 {
	if height <= 0 {
		return 0
	}
	if a.mdc == 0 {
		dc, _, _ := procCreateCompatibleDC.Call(0)
		if dc == 0 {
			return 0
		}
		a.mdc = dc
	}
	font := newFont(a.pickFace(a.cfg, s), height, weight)
	if font == 0 {
		return 0
	}
	prev, _, _ := procSelectObject.Call(a.mdc, font)
	// TEXTMETRICW is longs then words then bytes; reading the two leading longs
	// out of a raw buffer avoids trusting a struct layout we would have to spell.
	var buf [64]byte
	ok, _, _ := procGetTextMetricsW.Call(a.mdc, uintptr(unsafe.Pointer(&buf[0])))
	procSelectObject.Call(a.mdc, prev)
	procDeleteObject.Call(font)
	if ok == 0 {
		return 0
	}
	return int32(binary.LittleEndian.Uint32(buf[0:4]))
}

// fit decides how big the runs are and where they sit vertically. The font size
// is a wish; the strip height is the law, and the pair only survives if both
// cells fit, so shrinking is preferred to clipping every time.
func (a *app) fit(cfg config, mainText, transText string) textFit {
	scale := float64(a.dpi) / 96
	if scale <= 0 {
		scale = 1
	}
	fs := round32(float64(cfg.FontSize) * scale)
	if fs < 6 {
		fs = 6
	}
	if fs > a.winH*2 {
		fs = a.winH * 2
	}
	// One pixel of slack above and below: a cell that fills the strip exactly
	// would put the antialiased row of a descender on the canvas edge.
	budget := a.winH - 2
	if budget < 6 {
		budget = a.winH
	}
	showTrans := cfg.ShowTranslation && transText != ""
	if !showTrans {
		// One run owns the whole strip.
		for i := 0; i < 16 && fs > 6; i++ {
			if c := a.fontCell(mainText, fs, mainWeight); c <= budget {
				return textFit{main: fs, mainH: c}
			}
			fs = int32(float64(fs) * 0.94)
			if fs < 6 {
				fs = 6
			}
		}
		cell := a.fontCell(mainText, fs, mainWeight)
		if cell > a.winH {
			cell = a.winH
		}
		return textFit{main: fs, mainH: cell}
	}

	gap := maxInt32(1, round32(1.5*scale))
	for i := 0; i < 20; i++ {
		mc := a.fontCell(mainText, fs, mainWeight)
		t := fs * 62 / 100
		if t < 6 {
			t = 6
		}
		tc := a.fontCell(transText, t, transWeight)
		if mc+tc+gap <= budget {
			return textFit{
				main: fs, trans: t, mainH: mc, transH: tc, gap: gap,
				top: (a.winH - (mc + tc + gap)) / 2, showTrans: true,
			}
		}
		if fs <= 8 {
			break
		}
		fs = int32(float64(fs) * 0.94)
		if fs < 8 {
			fs = 8
		}
	}
	// Even the smallest pair will not fit: keep the line the listener reads and
	// let the translation go rather than squeezing both into unreadable slivers.
	cell := a.fontCell(mainText, fs, mainWeight)
	if cell > a.winH {
		cell = a.winH
	}
	return textFit{main: fs, mainH: cell, top: (a.winH - cell) / 2}
}

// newFont is the face at height pixels — the same one paint draws with, so the
// strip is exactly as wide as what lands in it.
func newFont(face string, height, weight int32) uintptr {
	if height <= 0 {
		return 0
	}
	facePtr := utf16p(face)
	if facePtr == nil {
		return 0
	}
	font, _, _ := procCreateFontW.Call(
		uintptr(uint32(int32(-height))), 0, 0, 0, uintptr(uint32(weight)),
		0, 0, 0, 1, 0, 0, antialiasedQ, 0,
		uintptr(unsafe.Pointer(facePtr)))
	return font
}

// runWidth is how wide paint will draw s, measured off-screen in the same face.
func (a *app) runWidth(s string, height, weight int32) int32 {
	if s == "" || height <= 0 {
		return 0
	}
	if a.mdc == 0 {
		dc, _, _ := procCreateCompatibleDC.Call(0)
		if dc == 0 {
			return 0
		}
		a.mdc = dc
	}
	font := newFont(a.pickFace(a.cfg, s), height, weight)
	if font == 0 {
		return 0
	}
	prev, _, _ := procSelectObject.Call(a.mdc, font)
	p := utf16p(s)
	if p == nil {
		procSelectObject.Call(a.mdc, prev)
		procDeleteObject.Call(font)
		return 0
	}
	var sz size32
	ok, _, _ := procGetTextExtentPoint32W.Call(a.mdc,
		uintptr(unsafe.Pointer(p)), uintptr(len([]rune(s))), uintptr(unsafe.Pointer(&sz)))
	procSelectObject.Call(a.mdc, prev)
	procDeleteObject.Call(font)
	if ok == 0 {
		return 0
	}
	return sz.cx
}

// textWidth is how wide the line draws, so a band is chosen for the text it has
// to hold rather than for the widest the strip is ever allowed to be.
func (a *app) textWidth(cfg config, mainText, transText string) int32 {
	f := a.fit(cfg, mainText, transText)
	w := a.runWidth(mainText, f.main, mainWeight)
	if f.showTrans {
		if tw := a.runWidth(transText, f.trans, transWeight); tw > w {
			w = tw
		}
	}
	return w
}

// effectiveAlign is what the placement implies: a line parked at an edge of the
// taskbar reads from that edge, not from the middle of a window it isn't in.
func effectiveAlign(cfg config) string {
	switch cfg.Position {
	case "left":
		return "left"
	case "right":
		return "right"
	}
	return cfg.Align
}

// A configured face is used as-is; GDI falls back for missing glyphs.
func (a *app) pickFace(cfg config, s string) string {
	if cfg.FontFamily != "" {
		return cfg.FontFamily
	}
	for _, r := range s {
		if r > 0x2E80 {
			return "Microsoft YaHei UI"
		}
	}
	return "Segoe UI"
}

// textPad is the inset an edge-anchored line keeps from the band it sits in.
func textPad(align string, scale float64) int32 {
	if align == "left" || align == "right" {
		return round32(10 * scale)
	}
	return 0
}

// lineX is the left edge of the main run as DrawText will place it.
func (a *app) lineX(align string, pad, lineW int32) int32 {
	switch align {
	case "left":
		return pad
	case "right":
		return a.winW - pad - lineW
	}
	return pad + (a.winW-2*pad-lineW)/2
}

// paint lays out the two text runs and composites them into a premultiplied
// canvas. prog is how much of the current line has been sung, 0..1, or -1 when
// the line has no timing worth wiping through.
func (a *app) paint(cfg config, ln line, prog float64) *canvas {
	return a.paintAt(cfg, ln, prog, 0)
}

// paintAt is paint shifted down by dy pixels, which is what the line change
// needs: the outgoing line rides up out of the strip while the incoming one
// arrives from below.
func (a *app) paintAt(cfg config, ln line, prog float64, dy int32) *canvas {
	mainText, transText := ln.Text, ln.Translation
	if a.winW <= 0 || a.winH <= 0 {
		return nil
	}
	p := &a.painter
	if !p.ensure(a.winW, a.winH) {
		return nil
	}
	clear(p.bits)

	scale := float64(a.dpi) / 96
	if scale <= 0 {
		scale = 1
	}
	f := a.fit(cfg, mainText, transText)
	if verbose && dy == 0 {
		logf("fit: fs=%d cell=%d trans=%d cell=%d gap=%d top=%d win=%dx%d",
			f.main, f.mainH, f.trans, f.transH, f.gap, f.top, a.winW, a.winH)
	}

	align := effectiveAlign(cfg)
	pad := textPad(align, scale)
	// One pixel of slack around each band: a rect exactly as tall as the cell
	// leaves no room for the antialiased row of a descender.
	slack := int32(1)
	mainRect := rect32{left: pad, top: f.top - slack + dy, right: a.winW - pad, bottom: f.top + f.mainH + slack + dy}
	transRect := rect32{
		left: pad, top: f.top + f.mainH + f.gap - slack + dy,
		right: a.winW - pad, bottom: f.top + f.mainH + f.gap + f.transH + slack + dy,
	}

	base := uint32(dtSingleLine | dtVCenter | dtEndEllipsis | dtNoPrefix)
	mainFmt := base | dtCenter
	switch align {
	case "left":
		mainFmt = base | dtLeft
	case "right":
		mainFmt = base | dtRight
	}

	// Two coverage planes: red carries the lyric line, blue the translation.
	p.text(mainText, a.pickFace(cfg, mainText), f.main, mainWeight, mainRect, mainFmt, rgb(255, 0, 0))
	if f.showTrans {
		p.text(transText, a.pickFace(cfg, transText), f.trans, transWeight, transRect, mainFmt, rgb(0, 0, 255))
	}

	c := newCanvas(a.winW, a.winH)
	mainC := a.mainColor(cfg)
	transC := a.translationColor(cfg, mainC)
	unsungC := mainC
	splitX := int32(-1)
	if prog >= 0 {
		unsungC = rgba{a: mainC.a * unsungAlphaPct / 100, r: mainC.r, g: mainC.g, b: mainC.b}
		if len(ln.Words) > 0 {
			splitX = a.wordSplit(align, pad, f.main, ln, prog)
		} else {
			splitX = a.sungSplit(align, pad, f.main, mainText, prog)
		}
	}

	var shadow *canvas
	if cfg.Shadow {
		shadow = a.shadowLayer(p.bits, scale, cfg.Halo)
	}
	composite(c.pix, p.bits, shadow, mainC, transC, oppositeLuminance(mainC), a.winW, splitX, unsungC)
	return c
}

// shadowLayer blurs the glyph coverage and colours it, so the halo sits under
// the strokes as a soft smudge.
//
// It used to be eight hard copies of the glyph one pixel out. That reads as a
// stroke, not a shadow: on a dense character the ring closes up and the inner
// counters fill in, which is exactly what made 影 muddy on a light taskbar.
// A gaussian-ish blur has no edge to close up.
func (a *app) shadowLayer(planes []byte, scale float64, strength int) *canvas {
	w, h := a.winW, a.winH
	n := int(w) * int(h)
	cov := make([]byte, n)
	for i, j := 0, 0; j < n; i, j = i+4, j+1 {
		m := planes[i]
		if r := planes[i+2]; r > m {
			m = r
		}
		cov[j] = m
	}
	r := maxInt32(1, round32(1.6*scale))
	blur := blurCoverage(cov, w, h, r)
	out := newCanvas(w, h)
	for j := 0; j < n; j++ {
		if blur[j] == 0 {
			continue
		}
		a := uint32(blur[j]) * uint32(shadowAlpha*strength) / (255 * 100)
		out.pix[j*4+0] = byte(a)
		out.pix[j*4+1] = byte(a)
		out.pix[j*4+2] = byte(a)
		out.pix[j*4+3] = byte(a)
	}
	return out
}

// blurCoverage runs three box passes per axis — close enough to a gaussian for
// a halo this soft, and cheap enough to run on every karaoke frame.
func blurCoverage(cov []byte, w, h, r int32) []byte {
	if r < 1 {
		r = 1
	}
	if w < 2*r+3 || h < 2*r+3 {
		return cov
	}
	a := make([]byte, len(cov))
	b := make([]byte, len(cov))
	copy(a, cov)
	for i := 0; i < 3; i++ {
		boxH(a, b, w, h, r)
		boxV(b, a, w, h, r)
	}
	return a
}

func boxH(src, dst []byte, w, h, r int32) {
	k := r*2 + 1
	for y := int32(0); y < h; y++ {
		row := int(y) * int(w)
		var sum int32
		for x := int32(0); x <= r; x++ {
			if x < w {
				sum += int32(src[row+int(x)])
			}
		}
		for x := int32(0); x < w; x++ {
			dst[row+int(x)] = byte(sum / k)
			if out := x - r; out >= 0 {
				sum -= int32(src[row+int(out)])
			}
			if in := x + r + 1; in < w {
				sum += int32(src[row+int(in)])
			}
		}
	}
}

func boxV(src, dst []byte, w, h, r int32) {
	k := r*2 + 1
	stride := int(w)
	for x := int32(0); x < w; x++ {
		col := int(x)
		var sum int32
		for y := int32(0); y <= r; y++ {
			if y < h {
				sum += int32(src[col+int(y)*stride])
			}
		}
		for y := int32(0); y < h; y++ {
			dst[col+int(y)*stride] = byte(sum / k)
			if out := y - r; out >= 0 {
				sum -= int32(src[col+int(out)*stride])
			}
			if in := y + r + 1; in < h {
				sum += int32(src[col+int(in)*stride])
			}
		}
	}
}

// composite turns the two colour planes and the shadow layer into one
// premultiplied BGRA image.
func composite(dst, planes []byte, shadow *canvas, mainC, transC, shadowC rgba, w, splitX int32, unsungC rgba) {
	for i := 0; i+3 < len(planes) && i+3 < len(dst); i += 4 {
		var dr, dg, db, da uint32
		x := int32(0)
		if w > 0 {
			x = int32(i/4) % w
		}
		// The part of the line not sung yet fades, so the wipe reads as progress
		// instead of as two words of different strength sitting side by side.
		mainHere, shadowScale := mainC, uint32(255)
		if splitX >= 0 && x >= splitX {
			mainHere, shadowScale = unsungC, uint32(unsungAlphaPct)*255/100
		}
		if shadow != nil {
			if sa := uint32(shadow.pix[i]) * shadowScale / 255; sa > 0 {
				inv := 255 - sa
				dr = shadowC.r*sa/255 + dr*inv/255
				dg = shadowC.g*sa/255 + dg*inv/255
				db = shadowC.b*sa/255 + db*inv/255
				da = sa + da*inv/255
			}
		}
		if mb := uint32(planes[i+2]); mb > 0 {
			if sa := mb * mainHere.a / 255; sa > 0 {
				inv := 255 - sa
				dr = mainHere.r*sa/255 + dr*inv/255
				dg = mainHere.g*sa/255 + dg*inv/255
				db = mainHere.b*sa/255 + db*inv/255
				da = sa + da*inv/255
			}
		}
		if tb := uint32(planes[i]); tb > 0 {
			if sa := tb * transC.a / 255; sa > 0 {
				inv := 255 - sa
				dr = transC.r*sa/255 + dr*inv/255
				dg = transC.g*sa/255 + dg*inv/255
				db = transC.b*sa/255 + db*inv/255
				da = sa + da*inv/255
			}
		}
		dst[i] = byte(db)
		dst[i+1] = byte(dg)
		dst[i+2] = byte(dr)
		dst[i+3] = byte(da)
	}
}

// shadowAlpha is how solid the halo is at its centre. Enough to lift white type
// off a pale taskbar, soft enough that its edge is a gradient and not a rim.
const shadowAlpha = 132

// unsungAlphaPct is how strongly the part of the line not sung yet still shows.
// Below roughly half it stops being readable at a glance; at full strength the
// wipe is invisible.
const unsungAlphaPct = 62

func luminance(c rgba) int32 {
	return (299*int32(c.r) + 587*int32(c.g) + 114*int32(c.b)) / 1000
}

// oppositeLuminance is the reference's rule: a bright glyph casts a dark shadow
// and a dark glyph a light one. A dark shadow under dark text only muddies it,
// which is exactly what a dense character like 影 looked like before.
func oppositeLuminance(text rgba) rgba {
	if luminance(text) >= 128 {
		return rgba{a: 255, r: 0, g: 0, b: 0}
	}
	return rgba{a: 255, r: 255, g: 255, b: 255}
}

// wantsAuto is "let the helper pick a colour that can actually be seen".
func wantsAuto(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "" || v == "auto"
}

// mainColor is the lyric colour. "auto" follows the taskbar under us: white on a
// dark bar, near-black on a light one, so the default is never white-on-white.
func (a *app) mainColor(cfg config) rgba {
	if wantsAuto(cfg.Color) {
		if a.light {
			return rgba{a: 255, r: 26, g: 26, b: 30}
		}
		return rgba{a: 255, r: 255, g: 255, b: 255}
	}
	return parseColor(cfg.Color, rgba{a: 255, r: 255, g: 255, b: 255})
}

// translationColor follows the main colour at reduced alpha unless it has one of
// its own, so a custom colour cannot leave the translation at an invisible one.
func (a *app) translationColor(cfg config, mainC rgba) rgba {
	if wantsAuto(cfg.TranslationColor) {
		return rgba{a: 190, r: mainC.r, g: mainC.g, b: mainC.b}
	}
	return parseColor(cfg.TranslationColor, rgba{a: 176, r: mainC.r, g: mainC.g, b: mainC.b})
}

// blendLayer draws src over dst at a constant alpha, shifted down by dy pixels.
// It is the line change: the outgoing block keeps sliding up while it fades, and
// the incoming one arrives from below, so the two never sit still on top of each
// other the way a plain crossfade does.
func blendLayer(dst, src *canvas, alpha uint32, dy int32) {
	if dst == nil || src == nil || alpha == 0 {
		return
	}
	keep := 255 - alpha
	for y := int32(0); y < src.h; y++ {
		ty := y + dy
		if ty < 0 || ty >= dst.h {
			continue
		}
		srow := int(y) * int(src.w) * 4
		drow := int(ty) * int(dst.w) * 4
		for x := int32(0); x < src.w && x < dst.w; x++ {
			si := srow + int(x)*4
			di := drow + int(x)*4
			for k := 0; k < 4; k++ {
				v := uint32(src.pix[si+k])*alpha/255 + uint32(dst.pix[di+k])*keep/255
				dst.pix[di+k] = byte(v)
			}
		}
	}
}

// frame is the scratch canvas the line change composites into.
func (a *app) frame(w, h int32) *canvas {
	if a.scratch == nil || a.scratch.w != w || a.scratch.h != h {
		a.scratch = newCanvas(w, h)
	}
	return a.scratch
}

// wordSplit puts the karaoke seam at the glyph boundary of the syllable being
// sung. wordProgress already interpolates inside that word, so its fractional
// part is the only intra-glyph movement we add here.
func (a *app) wordSplit(align string, pad, height int32, ln line, prog float64) int32 {
	if height <= 0 || ln.Text == "" {
		return -1
	}
	if prog >= 1 {
		return a.winW
	}
	runes := []rune(ln.Text)
	lineW := a.runWidth(ln.Text, height, mainWeight)
	startX := a.lineX(align, pad, lineW)
	if startX < 0 {
		startX = 0
	}
	pos := math.Max(0, prog) * float64(len(runes))
	full := int(pos)
	x := startX + a.runWidth(string(runes[:full]), height, mainWeight)
	if full < len(runes) {
		x += round32((pos - float64(full)) * float64(a.runWidth(string(runes[full:full+1]), height, mainWeight)))
	}
	return x
}

// sungSplit is the column where the wipe currently stands. It walks the line
// rune by rune and interpolates inside the rune being sung, so the edge glides
// instead of jumping a whole character at a time.
func (a *app) sungSplit(align string, pad, height int32, mainText string, prog float64) int32 {
	if height <= 0 || mainText == "" {
		return -1
	}
	runes := []rune(mainText)
	if prog >= 1 {
		return a.winW
	}
	if prog <= 0 {
		prog = 0
	}
	lineW := a.runWidth(mainText, height, mainWeight)
	startX := a.lineX(align, pad, lineW)
	if startX < 0 {
		startX = 0
	}
	pos := prog * float64(len(runes))
	full := int(pos)
	if full > len(runes) {
		full = len(runes)
	}
	frac := pos - float64(full)
	x := startX + a.runWidth(string(runes[:full]), height, mainWeight)
	if full < len(runes) && frac > 0 {
		x += round32(frac * float64(a.runWidth(string(runes[full:full+1]), height, mainWeight)))
	}
	return x
}
