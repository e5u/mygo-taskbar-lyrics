package main

import (
	"encoding/json"
	"math"
	"strings"
)

type config struct {
	Height           int
	FontSize         int
	FontFamily       string
	ShowTranslation  bool
	ShowNext         bool
	Color            string
	TranslationColor string
	Shadow           bool
	Halo             int
	Align            string
	Position         string
	MaxWidth         int
	OffsetX          int
	OffsetY          int
	ShowWhenPaused   bool
}

func defaultConfig() config {
	return config{
		Height:           34,
		FontSize:         18,
		ShowTranslation:  true,
		ShowNext:         false,
		Color:            "auto",
		TranslationColor: "auto",
		Shadow:           true,
		Halo:             65,
		Align:            "center",
		Position:         "center",
		MaxWidth:         1200,
		ShowWhenPaused:   true,
	}
}

// configPatch uses pointers so a repeated config message only changes the keys it carries.
type configPatch struct {
	Height           *int    `json:"height"`
	FontSize         *int    `json:"fontSize"`
	FontFamily       *string `json:"fontFamily"`
	ShowTranslation  *bool   `json:"showTranslation"`
	ShowNext         *bool   `json:"showNext"`
	Color            *string `json:"color"`
	TranslationColor *string `json:"translationColor"`
	Shadow           *bool   `json:"shadow"`
	Halo             *int    `json:"halo"`
	Align            *string `json:"align"`
	Position         *string `json:"position"`
	MaxWidth         *int    `json:"maxWidth"`
	OffsetX          *int    `json:"offsetX"`
	OffsetY          *int    `json:"offsetY"`
	ShowWhenPaused   *bool   `json:"showWhenPaused"`
}

func (p *configPatch) apply(c *config) {
	if p.Height != nil {
		c.Height = *p.Height
	}
	if p.FontSize != nil {
		c.FontSize = *p.FontSize
	}
	if p.FontFamily != nil {
		c.FontFamily = strings.TrimSpace(*p.FontFamily)
	}
	if p.ShowTranslation != nil {
		c.ShowTranslation = *p.ShowTranslation
	}
	if p.ShowNext != nil {
		c.ShowNext = *p.ShowNext
	}
	if p.Color != nil {
		c.Color = *p.Color
	}
	if p.TranslationColor != nil {
		c.TranslationColor = *p.TranslationColor
	}
	if p.Shadow != nil {
		c.Shadow = *p.Shadow
	}
	if p.Halo != nil {
		c.Halo = *p.Halo
	}
	if p.Align != nil {
		c.Align = *p.Align
	}
	if p.Position != nil {
		if n := normalizePosition(*p.Position); n != "" {
			c.Position = n
		}
	}
	if p.MaxWidth != nil {
		c.MaxWidth = *p.MaxWidth
	}
	if p.OffsetX != nil {
		c.OffsetX = *p.OffsetX
	}
	if p.OffsetY != nil {
		c.OffsetY = *p.OffsetY
	}
	if p.ShowWhenPaused != nil {
		c.ShowWhenPaused = *p.ShowWhenPaused
	}
	if c.Height < 8 {
		c.Height = 8
	}
	if c.FontSize < 6 {
		c.FontSize = 6
	}
	if c.Halo < 0 {
		c.Halo = 0
	} else if c.Halo > 200 {
		c.Halo = 200
	}
	if c.MaxWidth < 32 {
		c.MaxWidth = 32
	}
	if c.Position == "" {
		c.Position = "center"
	}
}

// normalizePosition maps the setting onto the placements the helper knows.
func normalizePosition(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "left", "free", "start", "左":
		return "left"
	case "center", "centre", "middle", "中":
		return "center"
	case "right", "end", "右":
		return "right"
	}
	return ""
}

type word struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type line struct {
	Start       float64 `json:"start"`
	End         float64 `json:"end"`
	Text        string  `json:"text"`
	Translation string  `json:"translation"`
	Words       []word  `json:"words"`
}

type snapshot struct {
	Playing  bool    `json:"playing"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	At       float64 `json:"at"`
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Artist   string  `json:"artist"`
	Album    string  `json:"album"`
	Lines    []line  `json:"lines"`
}

type message struct {
	Type   string           `json:"type"`
	Config *configPatch     `json:"config"`
	Now    *json.RawMessage `json:"now"`
}

// position estimates the playback position in ms. A paused player freezes at the snapshot.
func (s *snapshot) position(nowMs int64) float64 {
	pos := s.Position
	if s.Playing && s.At > 0 {
		pos += float64(nowMs) - s.At
	}
	if pos < 0 {
		pos = 0
	}
	return pos
}

// lineAt returns the index of the line holding pos. Gaps keep the previous line,
// which reads better than blanking the strip between lines.
func (s *snapshot) lineAt(pos float64) int {
	idx := 0
	for i := range s.Lines {
		if s.Lines[i].Start <= pos {
			idx = i
		} else {
			break
		}
	}
	return idx
}

// activeLine decides what should be on screen right now.
func activeLine(s *snapshot, cfg config, nowMs int64) (int, bool) {
	if s == nil || len(s.Lines) == 0 {
		return -1, false
	}
	if !s.Playing && !cfg.ShowWhenPaused {
		return -1, false
	}
	return s.lineAt(s.position(nowMs)), true
}

type rgba struct {
	a, r, g, b uint32
}

// parseColor accepts "#RRGGBB" and "#AARRGGBB".
func parseColor(s string, fallback rgba) rgba {
	if len(s) > 0 && s[0] == '#' {
		s = s[1:]
	}
	if len(s) == 6 {
		s = "FF" + s
	}
	if len(s) != 8 {
		return fallback
	}
	var v uint64
	for i := 0; i < 8; i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return fallback
		}
		v = v<<4 | d
	}
	return rgba{a: uint32(v >> 24), r: uint32(v>>16) & 0xFF, g: uint32(v>>8) & 0xFF, b: uint32(v) & 0xFF}
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func round32(f float64) int32 { return int32(math.Round(f)) }
