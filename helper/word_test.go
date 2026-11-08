package main

import "testing"

// AMLL word timing controls the wipe instead of spreading a line uniformly.
func TestWordProgressFollowsSyllableTiming(t *testing.T) {
	s := &snapshot{Position: 1500, Lines: []line{{Text: "ab", Words: []word{
		{Start: 1000, End: 2000, Text: "a"},
		{Start: 3000, End: 3100, Text: "b"},
	}}}}
	if got := wordProgress(s, 0, 0); got != 0.25 {
		t.Fatalf("half of first syllable = %v, want 0.25", got)
	}
	s.Position = 2500
	if got := wordProgress(s, 0, 0); got != 0.5 {
		t.Fatalf("gap after first syllable = %v, want 0.5", got)
	}
	s.Position = 3050
	if got := wordProgress(s, 0, 0); got != 0.75 {
		t.Fatalf("half of second syllable = %v, want 0.75", got)
	}
}

// English word spans omit spaces but their timing still reaches the right glyph.
func TestWordProgressSkipsInterWordSpace(t *testing.T) {
	s := &snapshot{Position: 1500, Lines: []line{{Text: "hello world", Words: []word{
		{Start: 1000, End: 2000, Text: "hello"}, {Start: 2000, End: 3000, Text: "world"},
	}}}}
	if got := wordProgress(s, 0, 0); got != 2.5/11 {
		t.Fatalf("half of hello = %v, want 2.5/11", got)
	}
	s.Position = 2500
	if got := wordProgress(s, 0, 0); got != 8.5/11 {
		t.Fatalf("half of world = %v, want 8.5/11", got)
	}
}

// Ordinary player lyrics keep their existing whole-line wipe.
func TestLineProgressWithoutWords(t *testing.T) {
	s := &snapshot{Position: 1500, Lines: []line{{Start: 1000, End: 2000, Text: "ab"}}}
	if got := lineProgress(s, 0, 0); got != 0.5 {
		t.Fatalf("line progress = %v, want 0.5", got)
	}
}
