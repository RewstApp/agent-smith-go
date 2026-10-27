package interpreter

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A running command is reported with its output so far and its last line;
// finished commands disappear; the reports are ordered oldest first.

func TestInFlightSet_TracksRunningCommandsOldestFirst(t *testing.T) {
	var set inFlightSet
	if got := set.snapshot(); len(got) != 0 {
		t.Fatalf("empty set reported %v", got)
	}
	out1, err1 := newBoundedWriter(100), newBoundedWriter(100)
	first := set.add("p-1", out1, err1)
	time.Sleep(2 * time.Millisecond) // sleep-ok: orders the two start times deterministically
	out2, err2 := newBoundedWriter(100), newBoundedWriter(100)
	second := set.add("p-2", out2, err2)
	_, _ = out1.Write([]byte("line one\nline two\n"))
	_, _ = err2.Write([]byte("only stderr here"))

	got := set.snapshot()
	if len(got) != 2 || got[0].PostID != "p-1" || got[1].PostID != "p-2" {
		t.Fatalf("snapshot %+v", got)
	}
	if got[0].StdoutBytes != 18 || got[0].StderrBytes != 0 || got[0].LastOutputLine != "line two" {
		t.Errorf("first %+v", got[0])
	}
	if got[1].StdoutBytes != 0 || got[1].StderrBytes != 16 ||
		got[1].LastOutputLine != "only stderr here" {
		t.Errorf("second falls back to stderr: %+v", got[1])
	}
	if got[0].StartedAt.After(got[1].StartedAt) {
		t.Error("not ordered oldest first")
	}
	set.remove(first)
	if got := set.snapshot(); len(got) != 1 || got[0].PostID != "p-2" {
		t.Errorf("after removing the first: %+v", got)
	}
	set.remove(second)
	if got := set.snapshot(); len(got) != 0 {
		t.Errorf("after removing both: %+v", got)
	}
}

func TestLastLine_KeepsMovingPastTheCeilingAndBoundsTheLine(t *testing.T) {
	w := newBoundedWriter(8)
	_, _ = w.Write([]byte("kept\nmore than the ceiling allows\n"))
	if w.Produced() != 34 || w.Len() != 8 {
		t.Fatalf("produced %d kept %d", w.Produced(), w.Len())
	}
	if got := w.LastLine(); got != "more than the ceiling allows" {
		t.Errorf("last line %q, want the whole line although only 8 bytes were kept", got)
	}
	// The line keeps moving after the ceiling: it is tracked from every byte
	// written, not from the kept prefix.
	_, _ = w.Write([]byte("step 2 done\nstep 3 done\n"))
	if got := w.LastLine(); got != "step 3 done" {
		t.Errorf("last line after the ceiling %q, want %q", got, "step 3 done")
	}
	if w.Len() != 8 {
		t.Errorf("tracking the line grew the kept bytes to %d", w.Len())
	}
	long := newBoundedWriter(10_000)
	_, _ = long.Write([]byte(strings.Repeat("x", 1000) + "\n" + strings.Repeat("y", 500)))
	if got := long.LastLine(); len(got) != lastOutputLineMax || !strings.HasSuffix(got, "y") {
		t.Errorf("long last line not bounded: %d bytes", len(got))
	}
	empty := newBoundedWriter(10)
	_, _ = empty.Write([]byte("\r\n\n"))
	if got := empty.LastLine(); got != "" {
		t.Errorf("whitespace-only output gave %q", got)
	}
	crlf := newBoundedWriter(100)
	_, _ = crlf.Write([]byte("first\r\nsecond\r\n"))
	if got := crlf.LastLine(); got != "second" {
		t.Errorf("CRLF last line %q", got)
	}
	// An empty line does not replace the last non-empty one.
	_, _ = crlf.Write([]byte("\n\n"))
	if got := crlf.LastLine(); got != "second" {
		t.Errorf("empty lines replaced the last line: %q", got)
	}
}

func TestLastLine_SplitsAcrossWritesAndOnBareCarriageReturns(t *testing.T) {
	w := newBoundedWriter(1 << 20)
	for _, chunk := range []string{"Installing ", "package 12", " of 40\nVerify", "ing..."} {
		_, _ = w.Write([]byte(chunk))
	}
	if got := w.LastLine(); got != "Verifying..." {
		t.Errorf("partial line across writes %q", got)
	}
	bar := newBoundedWriter(1 << 20)
	_, _ = bar.Write([]byte("10%\r20%\r99%\r"))
	if got := bar.LastLine(); got != "99%" {
		t.Errorf("CR-redrawn progress reported %q, want the latest frame", got)
	}
}

func TestLastLine_CutsOnARuneBoundary(t *testing.T) {
	w := newBoundedWriter(1 << 20)
	// 100 three-byte runes: 300 bytes, and 300-256 is not a multiple of 3, so a
	// byte cut would start on a continuation byte.
	_, _ = w.Write([]byte(strings.Repeat("\u2588", 100)))
	got := w.LastLine()
	if !utf8.ValidString(got) {
		t.Fatalf("last line is not valid UTF-8: %q", got)
	}
	if len(got) > lastOutputLineMax || len(got) < lastOutputLineMax-utf8.UTFMax {
		t.Errorf("last line is %d bytes", len(got))
	}
	// A line longer than the tail written in one piece is bounded too.
	one := newBoundedWriter(1 << 20)
	_, _ = one.Write([]byte(strings.Repeat("z", 5000)))
	if got := one.LastLine(); len(got) != lastOutputLineMax {
		t.Errorf("single long write kept %d bytes", len(got))
	}
}

func TestInFlightOf_MergesReportersAndSkipsOthers(t *testing.T) {
	a := &baseExecutor{}
	b := &baseExecutor{}
	ra := a.inFlight.add("a-1", newBoundedWriter(1), newBoundedWriter(1))
	time.Sleep(2 * time.Millisecond) // sleep-ok: orders the two start times deterministically
	rb := b.inFlight.add("b-1", newBoundedWriter(1), newBoundedWriter(1))
	defer a.inFlight.remove(ra)
	defer b.inFlight.remove(rb)
	type plain struct{ Executor }
	got := inFlightOf(b, &plain{}, a)
	if len(got) != 2 || got[0].PostID != "a-1" || got[1].PostID != "b-1" {
		t.Errorf("merged %+v", got)
	}
}
