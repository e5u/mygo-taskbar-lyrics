package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
)

var verbose bool
var trayClassPtr = utf16p("Shell_TrayWnd")

func logf(format string, args ...any) {
	if verbose {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}
}

// emit writes one protocol line to stdout and returns immediately; the plugin reads it live.
func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	b = append(b, '\n')
	os.Stdout.Write(b)
}

func main() {
	flag.BoolVar(&verbose, "verbose", false, "diagnostics on stderr")
	flag.Parse()

	// Ordinary and elevated app instances share this desktop, but not a window
	// station. Claim the global name before either can create a second strip.
	single, ok := acquireSingle()
	if !ok {
		emit(map[string]any{"type": "single", "active": false})
		return
	}
	defer procCloseHandle.Call(uintptr(single))

	runtime.LockOSThread()
	enableDPIAwareness()

	a := newApp()
	go a.readStdin()

	os.Exit(a.run())
}

func (a *app) readStdin() {
	dec := json.NewDecoder(os.Stdin)
	for {
		var m message
		if err := dec.Decode(&m); err != nil {
			logf("stdin closed: %v", err)
			a.mu.Lock()
			a.quit = true
			a.mu.Unlock()
			return
		}
		switch m.Type {
		case "config":
			if m.Config != nil {
				a.mu.Lock()
				m.Config.apply(&a.cfg)
				a.dirty = true
				cfg := a.cfg
				a.mu.Unlock()
				logf("config: %+v", cfg)
			}
		case "now":
			var s *snapshot
			if m.Now != nil {
				var v snapshot
				if err := json.Unmarshal(*m.Now, &v); err != nil {
					logf("now: %v", err)
					emit(map[string]any{"type": "error", "message": "bad now: " + err.Error()})
					continue
				}
				s = &v
			}
			a.mu.Lock()
			a.snap = s
			a.dirty = true
			a.mu.Unlock()
			if s != nil {
				logf("now: playing=%v position=%.0f lines=%d", s.Playing, s.Position, len(s.Lines))
			}
		case "quit":
			a.mu.Lock()
			a.quit = true
			a.mu.Unlock()
			return
		default:
			logf("unknown message type %q", m.Type)
		}
	}
}
