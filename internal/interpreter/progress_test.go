package interpreter

import (
	"strings"
	"testing"
	"time"
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

func TestLastLine_CountsProducedBytesPastTheCeilingAndBoundsTheLine(t *testing.T) {
	w := newBoundedWriter(8)
	_, _ = w.Write([]byte("kept\nmore than the ceiling allows\n"))
	if w.Produced() != 34 || w.Len() != 8 {
		t.Fatalf("produced %d kept %d", w.Produced(), w.Len())
	}
	if got := w.lastLine(); got != "mor" {
		t.Errorf("last line of the kept bytes %q, want the partial line %q", got, "mor")
	}
	long := newBoundedWriter(10_000)
	_, _ = long.Write([]byte(strings.Repeat("x", 1000) + "\n" + strings.Repeat("y", 500)))
	if got := long.lastLine(); len(got) != lastOutputLineMax || !strings.HasSuffix(got, "y") {
		t.Errorf("long last line not bounded: %d bytes", len(got))
	}
	empty := newBoundedWriter(10)
	_, _ = empty.Write([]byte("\r\n\n"))
	if got := empty.lastLine(); got != "" {
		t.Errorf("whitespace-only output gave %q", got)
	}
	crlf := newBoundedWriter(100)
	_, _ = crlf.Write([]byte("first\r\nsecond\r\n"))
	if got := crlf.lastLine(); got != "second" {
		t.Errorf("CRLF last line %q", got)
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
