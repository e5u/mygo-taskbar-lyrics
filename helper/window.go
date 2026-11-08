package main

import (
	"math"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	timerID   = 1
	tickMs    = 50
	fadeMs    = 320
	checkMs   = 1000
	raiseMs   = 400
	gapMs     = 3000
	className = "MygoTaskbarLyrics"
	wndName   = "mygo taskbar lyrics"
)

var (
	classPtr    = utf16p(className)
	namePtr     = utf16p(wndName)
	wndProcAddr = syscall.NewCallback(wndProc)
	appRef      *app
)

type renderKey struct {
	idx   int
	prog  int32
	main  string
	trans string
	cfg   config
	w, h  int32
	dpi   int32
	light bool
}

type app struct {
	hwnd syscall.Handle

	mu    sync.Mutex
	cfg   config
	snap  *snapshot
	dirty bool
	quit  bool

	painter painter

	tb        rect32
	tbOK      bool
	bands     []band
	bandsFor  rect32
	bandsAt   int64
	mdc       uintptr
	dpi       int32
	light     bool
	lightOK   bool
	lightFor  rect32
	lightSeen int64
	lastCheck int64

	winX, winY, winW, winH int32
	visible                bool
	upToDate               bool

	raisedAt   int64
	cur, prev  *canvas
	scratch    *canvas
	lastKey    renderKey
	hasKey     bool
	shownLine  int
	blankSince int64
	fadeStart  int64
	fading     bool
}

func newApp() *app {
	return &app{cfg: defaultConfig(), dpi: 96, shownLine: -1}
}

func (a *app) run() int {
	appRef = a
	if err := a.create(); err != nil {
		emit(map[string]any{"type": "error", "message": err.Error()})
		return 1
	}
	a.locate()
	emit(map[string]any{"type": "ready", "taskbar": a.tbOK})
	procSetTimer.Call(uintptr(a.hwnd), timerID, tickMs, 0)

	var msg [64]byte
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg[0])), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg[0])))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg[0])))
	}
	return 0
}

func wndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	switch uint32(msg) {
	case wmTimer:
		if appRef != nil {
			appRef.tick()
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return r
}

func (a *app) create() error {
	hInst, _, _ := procGetModuleHandleW.Call(0)
	wc := wndClassExW{
		lpfnWndProc:   wndProcAddr,
		hInstance:     syscall.Handle(hInst),
		lpszClassName: classPtr,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if c, _, _ := procLoadCursorW.Call(0, 32512); c != 0 {
		wc.hCursor = syscall.Handle(c)
	}
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	ex := uintptr(wsExLayered | wsExTopmost | wsExNoActivate | wsExToolWindow | wsExTransparent)
	hwnd, _, err := procCreateWindowExW.Call(ex,
		uintptr(unsafe.Pointer(classPtr)),
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(wsPopup),
		0, 0, 0, 0, 0, 0, hInst, 0)
	if hwnd == 0 {
		return err
	}
	a.hwnd = syscall.Handle(hwnd)
	return nil
}

// locate re-reads the taskbar geometry; it moves with DPI, auto-hide and explorer restarts.
func (a *app) locate() {
	tray, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(trayClassPtr)), 0)
	if tray == 0 {
		a.tbOK = false
		return
	}
	var r rect32
	if ok, _, _ := procGetWindowRect.Call(tray, uintptr(unsafe.Pointer(&r))); ok == 0 {
		a.tbOK = false
		return
	}
	if r.right-r.left < 16 || r.bottom-r.top < 8 {
		a.tbOK = false
		return
	}
	a.tb = r
	a.dpi = dpiForWindow(tray)
	a.tbOK = true
}

// pickBand is the stretch of taskbar the line goes in: the one nearest the edge
// it is anchored to that has room for it, or, when none has, the one nearest
// that edge, whose width then trims the text. Room first, edge second.
func pickBand(bands []band, need int32, fromRight bool) (band, bool) {
	if len(bands) == 0 {
		return band{}, false
	}
	best, found := band{}, false
	for _, b := range bands {
		if b.x1-b.x0 < need {
			continue
		}
		if !found || (fromRight && b.x0 > best.x0) || (!fromRight && b.x0 < best.x0) {
			best, found = b, true
		}
	}
	if found {
		return best, true
	}
	// No band has room near that edge: the widest one wins, so the line stays
	// whole even if it has to sit somewhere else than asked.
	best, found = band{}, false
	for _, b := range bands {
		if !found || b.x1-b.x0 > best.x1-best.x0 {
			best, found = b, true
		}
	}
	return best, true
}

func (a *app) layout(cfg config, mainText, transText string) {
	scale := float64(a.dpi) / 96
	if scale <= 0 {
		scale = 1
	}
	tbW := a.tb.right - a.tb.left
	tbH := a.tb.bottom - a.tb.top
	margin := round32(8 * scale)

	h := round32(float64(cfg.Height) * scale)
	if h > tbH-2 {
		h = tbH - 2
	}
	if h < 8 {
		h = 8
	}
	// The height has to be known before anything is measured: the fonts, and so
	// the width of the line, follow from it.
	a.winH = h
	w := round32(float64(cfg.MaxWidth) * scale)
	if w > tbW-2*margin {
		w = tbW - 2*margin
	}
	if w < 16 {
		w = 16
	}
	// "left" and "right" anchor the strip to an edge of the taskbar: the left
	// edge of the first icon-free band, or the right edge of the last. The line
	// then reads from that edge (see effectiveAlign) instead of floating in the
	// middle of an empty stretch, which is what "left" looked like before.
	x := a.tb.left + (tbW-w)/2
	switch cfg.Position {
	case "left", "right":
		// Aim for the room the line needs, not for the widest strip it may have:
		// a 400px line belongs in a 400px band, not cut into a 156px one.
		textW := a.textWidth(cfg, mainText, transText)
		if textW <= 0 {
			// Nothing measured (no text yet, or a font that is not ready): keep
			// the room the strip is allowed, so a first frame cannot claim a
			// sliver of a band and then jump.
			textW = w
		}
		need := textW + 2*round32(10*scale) + 2*round32(scale) + 2*margin
		if b, ok := pickBand(a.bands, need, cfg.Position == "right"); ok {
			if verbose {
				logf("anchor: need=%d band=[%d,%d] w=%d", need, b.x0, b.x1, w)
			}
			if room := b.x1 - b.x0 - 2*margin; room >= 16 && w > room {
				w = room
			}
			if cfg.Position == "left" {
				x = b.x0 + margin
			} else {
				x = b.x1 - margin - w
			}
		} else if cfg.Position == "left" {
			x = a.tb.left + margin
		} else {
			x = a.tb.right - margin - w
		}
	}
	x += round32(float64(cfg.OffsetX) * scale)
	y := a.tb.top + (tbH-h)/2 + round32(float64(cfg.OffsetY)*scale)
	// Stay strictly inside the taskbar rect even when offsets push outwards.
	x = int32(math.Max(float64(a.tb.left), math.Min(float64(x), float64(a.tb.right-w))))
	y = int32(math.Max(float64(a.tb.top), math.Min(float64(y), float64(a.tb.bottom-h))))
	a.winX, a.winY, a.winW, a.winH = x, y, w, h
}

func (a *app) tick() {
	nowMs := time.Now().UnixMilli()

	a.mu.Lock()
	quit := a.quit
	dirty := a.dirty
	a.dirty = false
	cfg := a.cfg
	snap := a.snap
	a.mu.Unlock()

	if quit {
		procKillTimer.Call(uintptr(a.hwnd), timerID)
		procDestroyWindow.Call(uintptr(a.hwnd))
		return
	}

	if a.lastCheck == 0 || nowMs-a.lastCheck >= checkMs {
		a.lastCheck = nowMs
		a.locate()
	}
	if !a.tbOK {
		a.hide()
		return
	}

	if (cfg.Position == "left" || cfg.Position == "right") &&
		(a.bandsFor != a.tb || nowMs-a.bandsAt >= gapMs) {
		// Hand our own columns to the scan rather than hiding for it: a hide is one
		// composited frame without the line, which reads as a blink every 3s.
		skip0, skip1 := int32(0), int32(0)
		if a.visible {
			skip0, skip1 = a.winX-2, a.winX+a.winW+2
		}
		a.bands = freeBands(a.tb, skip0, skip1)
		a.bandsFor, a.bandsAt = a.tb, nowMs
		a.upToDate = false
	}

	if wantsAuto(cfg.Color) || wantsAuto(cfg.TranslationColor) {
		if !a.lightOK || a.lightFor != a.tb || nowMs-a.lightSeen >= lightAt {
			if light, ok := a.taskbarLight(); ok {
				a.light, a.lightOK = light, true
			}
			a.lightFor, a.lightSeen = a.tb, nowMs
		}
	}

	idx, show := activeLine(snap, cfg, nowMs)
	mainText, transText := "", ""
	prog := float64(-1)
	if show && idx >= 0 && snap != nil && idx < len(snap.Lines) {
		mainText = snap.Lines[idx].Text
		if cfg.ShowNext && idx+1 < len(snap.Lines) {
			transText = snap.Lines[idx+1].Text
		} else {
			transText = snap.Lines[idx].Translation
		}
		if len(snap.Lines[idx].Words) > 0 {
			prog = wordProgress(snap, idx, nowMs)
		} else {
			prog = lineProgress(snap, idx, nowMs)
		}
	}

	oldX, oldY, oldW, oldH := a.winX, a.winY, a.winW, a.winH
	a.layout(cfg, mainText, transText)
	if oldX != a.winX || oldY != a.winY || oldW != a.winW || oldH != a.winH {
		a.upToDate = false
		logf("rect: taskbar=[%d,%d %dx%d] dpi=%d window=(%d,%d) %dx%d",
			a.tb.left, a.tb.top, a.tb.right-a.tb.left, a.tb.bottom-a.tb.top, a.dpi,
			a.winX, a.winY, a.winW, a.winH)
	}

	if !show || idx < 0 {
		// A track that is still playing but has no timed line yet — the seconds
		// before the first line, or a new track whose lyrics are still loading —
		// must not blink the strip. Hold the last frame, then let go.
		if snap != nil && snap.Playing && a.hasKey {
			if a.blankSince == 0 {
				a.blankSince = nowMs
			}
			if nowMs-a.blankSince < holdBlankMs {
				a.present(nowMs)
				return
			}
		}
		a.blankSince = 0
		a.hide()
		return
	}
	a.blankSince = 0
	// A twelfth of a pixel of wipe is finer than the tick that produced it, so
	// the key only changes when the wipe has actually moved on screen.
	key := renderKey{idx: idx, prog: int32(math.Round(prog * 4096)), main: mainText, trans: transText, cfg: cfg, w: a.winW, h: a.winH, dpi: a.dpi, light: a.light}
	if dirty || !a.hasKey || key != a.lastKey {
		c := a.paint(cfg, snap.Lines[idx], prog)
		dumpCanvas(c, dumpPath)
		if c != nil {
			if a.hasKey && key.idx != a.lastKey.idx {
				a.prev = a.cur
				a.fading = a.prev != nil && a.lastKey.idx >= 0
				a.fadeStart = nowMs
			} else {
				a.prev = nil
				a.fading = false
			}
			a.cur = c
			a.lastKey = key
			a.hasKey = true
			a.upToDate = false
		}
	}

	if idx != a.shownLine {
		a.shownLine = idx
		emit(map[string]any{"type": "line", "index": idx})
	}

	a.present(nowMs)
	a.show()
	a.raise(nowMs)
}

func (a *app) present(nowMs int64) {
	if a.cur == nil {
		return
	}
	if a.fading {
		q := float64(nowMs-a.fadeStart) / fadeMs
		if q >= 1 {
			a.fading = false
			a.prev = nil
		} else {
			a.upload(a.slideFrame(q))
			return
		}
	}
	if a.upToDate {
		return
	}
	a.upload(a.cur)
}

func (a *app) upload(c *canvas) {
	if c == nil || !a.painter.ensure(c.w, c.h) {
		return
	}
	logf("t=%d upload %dx%d at (%d,%d)", time.Now().UnixMilli(), c.w, c.h, a.winX, a.winY)
	copy(a.painter.bits, c.pix)
	dst := point32{x: a.winX, y: a.winY}
	size := size32{cx: c.w, cy: c.h}
	src := point32{}
	bf := blend32{op: 0, flags: 0, alpha: 255, format: 1}
	procUpdateLayeredWindow.Call(uintptr(a.hwnd), 0,
		uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)),
		uintptr(a.painter.dc), uintptr(unsafe.Pointer(&src)),
		0, uintptr(unsafe.Pointer(&bf)), ulwAlpha)
	a.upToDate = true

}

// raise puts the strip back above the taskbar. The taskbar is topmost too, and
// it comes forward whenever it repaints — a notification, a clock tick, a
// hover — after which an update to our layered surface is composited *under*
// it and the line silently disappears until something shows the window again.
// Re-asserting the z-order is the only way to stay on top of it.
func (a *app) raise(nowMs int64) {
	if !a.visible || nowMs-a.raisedAt < raiseMs {
		return
	}
	a.raisedAt = nowMs
	procSetWindowPos.Call(uintptr(a.hwnd), ^uintptr(0), 0, 0, 0, 0, 0x0001|0x0002|0x0010)
}

func (a *app) show() {
	if a.visible {
		return
	}
	logf("t=%d show", time.Now().UnixMilli())
	procSetWindowPos.Call(uintptr(a.hwnd), ^uintptr(0), 0, 0, 0, 0, 0x0001|0x0002|0x0010)
	procShowWindow.Call(uintptr(a.hwnd), swShowNoActivate)
	a.visible = true
	a.upToDate = false
}

func (a *app) hide() {
	if !a.visible {
		return
	}
	logf("t=%d hide", time.Now().UnixMilli())
	procShowWindow.Call(uintptr(a.hwnd), swHide)
	a.visible = false
	a.upToDate = false
}

// slideFrame is the line change: the incoming line rises from below while the
// outgoing one keeps moving up and fades out, both on a sine in-out curve.
//
// The fade is tied to the travel (q² leaving, 1-(1-q)² arriving) rather than to
// plain progress, because two blocks each at 50% half-way through read as the
// whole strip dimming for a moment. With these curves both still hold about
// three quarters when they cross, and the change of direction at both ends is
// where a dropped frame would otherwise show.
func (a *app) slideFrame(q float64) *canvas {
	cw, ch := a.cur.w, a.cur.h
	if a.prev != nil {
		if a.prev.w > cw {
			cw = a.prev.w
		}
		if a.prev.h > ch {
			ch = a.prev.h
		}
	}
	frame := a.frame(cw, ch)
	clear(frame.pix)
	e := 0.5 - 0.5*math.Cos(math.Pi*math.Max(0, math.Min(1, q)))
	dist := float64(a.winH) * 0.86
	// Entering a² + leaving (1-e)² = 1, so as one line fades the other fills
	// exactly the gap it leaves. With both on the same eased curve the pair never
	// dips below full strength; an early in-frame still has the old line whole.
	aIn := uint32(e * e * 255)
	aOut := uint32((1 - e) * (1 - e) * 255)
	blendLayer(frame, a.cur, aIn, round32((1-e)*dist))
	blendLayer(frame, a.prev, aOut, -round32(e*dist))
	return frame
}

// lineProgress is how much of the line has been sung, 0..1, or -1 when the line
// has no timing worth wiping through: karaoke over eighty milliseconds is noise
// and those lines read better at full strength.
func lineProgress(s *snapshot, idx int, nowMs int64) float64 {
	if s == nil || idx < 0 || idx >= len(s.Lines) {
		return -1
	}
	ln := s.Lines[idx]
	if ln.End <= ln.Start || ln.End-ln.Start < minKaraokeMs {
		return -1
	}
	p := (s.position(nowMs) - ln.Start) / (ln.End - ln.Start)
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// wordProgress maps playback onto the line's text rather than its duration:
// a long English syllable and a one-kana syllable occupy different widths.
// Between words the seam stays at the end of the preceding word, as AMLL does.
func wordProgress(s *snapshot, idx int, nowMs int64) float64 {
	ln := s.Lines[idx]
	if len(ln.Words) == 0 || ln.Text == "" {
		return -1
	}
	pos := s.position(nowMs)
	runes := []rune(ln.Text)
	at := 0
	for _, w := range ln.Words {
		n := len([]rune(w.Text))
		// TTML carries words, not the spaces between them. Treat a whitespace
		// gap in the display text as sung with the preceding word.
		if at < len(runes) && n > 0 && string(runes[at:at+1]) != string([]rune(w.Text)[0]) {
			for at < len(runes) && (runes[at] == ' ' || runes[at] == '\t') {
				at++
			}
		}
		if n == 0 || at+n > len(runes) || string(runes[at:at+n]) != w.Text {
			return -1
		}
		if pos < w.Start {
			return float64(at) / float64(len(runes))
		}
		frac := float64(1)
		if pos < w.End && w.End > w.Start {
			frac = (pos - w.Start) / (w.End - w.Start)
		}
		if pos < w.End {
			return (float64(at) + frac*float64(n)) / float64(len(runes))
		}
		at += n
	}
	if at != len(runes) {
		return -1
	}
	return 1
}

// minKaraokeMs skips lines that flash past: a wipe over eighty milliseconds is
// noise, and those lines read better at full strength.
const minKaraokeMs = 400.0

// holdBlankMs is how long the last line stays on screen after the lyrics run out
// while the track is still playing.
const holdBlankMs = 2500
