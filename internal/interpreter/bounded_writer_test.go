package interpreter

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestBoundedWriter_BelowLimitKeepsEverything(t *testing.T) {
	w := newBoundedWriter(1024)

	if _, err := io.WriteString(w, "hello "); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if _, err := io.WriteString(w, "world"); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if got := w.String(); got != "hello world" {
		t.Errorf("String() = %q, want %q", got, "hello world")
	}
	if got := w.Len(); got != len("hello world") {
		t.Errorf("Len() = %d, want %d", got, len("hello world"))
	}
	if got := w.Produced(); got != int64(len("hello world")) {
		t.Errorf("Produced() = %d, want %d", got, len("hello world"))
	}
	if w.Truncated() {
		t.Error("Truncated() = true for output below the limit")
	}
}

// TestBoundedWriter_ExactlyAtLimitIsNotTruncated guards the boundary: filling the
// ceiling precisely must not be reported as a loss of output.
func TestBoundedWriter_ExactlyAtLimitIsNotTruncated(t *testing.T) {
	w := newBoundedWriter(10)

	if _, err := io.WriteString(w, strings.Repeat("a", 10)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if w.Truncated() {
		t.Error("Truncated() = true when output exactly filled the limit")
	}
	if got := w.Len(); got != 10 {
		t.Errorf("Len() = %d, want 10", got)
	}
}

func TestBoundedWriter_AboveLimitTruncatesAndCounts(t *testing.T) {
	const limit = 8
	w := newBoundedWriter(limit)

	// Split across writes so the ceiling is crossed mid-write, not at a boundary.
	n, err := io.WriteString(w, "12345")
	if err != nil || n != 5 {
		t.Fatalf("first write = (%d, %v), want (5, nil)", n, err)
	}
	n, err = io.WriteString(w, "6789ABCDEF")
	if err != nil || n != 10 {
		t.Fatalf("second write = (%d, %v), want (10, nil)", n, err)
	}

	if got := w.String(); got != "12345678" {
		t.Errorf("String() = %q, want %q (the first %d bytes)", got, "12345678", limit)
	}
	if got := w.Len(); got != limit {
		t.Errorf("Len() = %d, want %d", got, limit)
	}
	if got := w.Produced(); got != 15 {
		t.Errorf("Produced() = %d, want 15", got)
	}
	if !w.Truncated() {
		t.Error("Truncated() = false after output exceeded the limit")
	}
}

// TestBoundedWriter_NeverShortWrites is the property that keeps a verbose command
// alive: io.Copy aborts with ErrShortWrite if a writer reports fewer bytes than it
// was given, which would surface to the command as a failed/severed stream. The
// writer must therefore claim every byte even while discarding most of them.
func TestBoundedWriter_NeverShortWrites(t *testing.T) {
	w := newBoundedWriter(16)

	// 1 MiB copied through a 16-byte ceiling: io.Copy must run to completion.
	src := bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20))
	copied, err := io.Copy(w, src)
	if err != nil {
		t.Fatalf("io.Copy through a bounded writer failed: %v", err)
	}
	if copied != 1<<20 {
		t.Errorf("io.Copy copied %d bytes, want %d", copied, 1<<20)
	}
	if got := w.Len(); got != 16 {
		t.Errorf("Len() = %d, want 16", got)
	}
	if got := w.Produced(); got != 1<<20 {
		t.Errorf("Produced() = %d, want %d", got, 1<<20)
	}
}

// TestBoundedWriter_MemoryIsBounded asserts the property the OOM fix rests on: the
// backing array never grows past the ceiling, so retained memory is a function of
// the limit rather than of how much the command wrote.
func TestBoundedWriter_MemoryIsBounded(t *testing.T) {
	const limit = 1000
	w := newBoundedWriter(limit)

	// Many writes whose total is orders of magnitude above the ceiling, sized so
	// several land mid-growth rather than on a power-of-two boundary.
	chunk := bytes.Repeat([]byte("y"), 333)
	for i := 0; i < 10000; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("unexpected write error: %v", err)
		}
	}

	if got := len(w.kept); got != limit {
		t.Errorf("kept %d bytes, want the %d-byte ceiling", got, limit)
	}
	if got := cap(w.kept); got > limit {
		t.Errorf("backing array grew to cap %d, past the %d-byte ceiling", got, limit)
	}
	if got, want := w.Produced(), int64(333*10000); got != want {
		t.Errorf("Produced() = %d, want %d", got, want)
	}
}

// TestBoundedWriter_ConcurrentAccessIsSafe exercises the mutex under -race: an
// expired cmd.WaitDelay lets cmd.Wait return while os/exec's pipe copier is still
// writing, so reads of the kept bytes can genuinely overlap writes.
func TestBoundedWriter_ConcurrentAccessIsSafe(t *testing.T) {
	w := newBoundedWriter(64)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_, _ = io.WriteString(w, "abcdefgh")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			_ = w.String()
			_ = w.Len()
			_ = w.Produced()
			_ = w.Truncated()
		}
	}()
	wg.Wait()
}

// TestTruncationOf_StreamsAreIndependent verifies stdout and stderr each get their
// own ceiling (one flooding stream does not steal the other's budget) while the
// reported counts are the totals across both.
func TestTruncationOf_StreamsAreIndependent(t *testing.T) {
	const limit = 4
	stdout := newBoundedWriter(limit)
	stderr := newBoundedWriter(limit)

	if _, err := io.WriteString(stdout, "out"); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	if _, err := io.WriteString(stderr, strings.Repeat("e", 100)); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	// stdout stayed under its own ceiling even though stderr blew through its.
	if stdout.Truncated() {
		t.Error("stdout was truncated by stderr's volume; ceilings are not independent")
	}
	if got := stdout.String(); got != "out" {
		t.Errorf("stdout String() = %q, want %q", got, "out")
	}
	if !stderr.Truncated() {
		t.Error("stderr was not truncated despite exceeding its ceiling")
	}
	if got := stderr.Len(); got != limit {
		t.Errorf("stderr kept %d bytes, want %d", got, limit)
	}

	trunc := truncationOf(stdout, stderr)
	if !trunc.Truncated {
		t.Error("truncationOf reported Truncated=false when stderr was truncated")
	}
	if want := int64(3 + 100); trunc.Produced != want {
		t.Errorf("Produced = %d, want %d (both streams)", trunc.Produced, want)
	}
	if want := int64(3 + limit); trunc.Kept != want {
		t.Errorf("Kept = %d, want %d (both streams)", trunc.Kept, want)
	}
}

func TestTruncationOf_NoTruncation(t *testing.T) {
	stdout := newBoundedWriter(1024)
	stderr := newBoundedWriter(1024)

	if _, err := io.WriteString(stdout, "fine"); err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	trunc := truncationOf(stdout, stderr)
	if trunc.Truncated {
		t.Error("truncationOf reported Truncated=true for complete output")
	}
	if trunc.Produced != 4 || trunc.Kept != 4 {
		t.Errorf("Produced/Kept = %d/%d, want 4/4", trunc.Produced, trunc.Kept)
	}
}
