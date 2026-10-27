package interpreter

import (
	"bytes"
	"sync"
	"unicode/utf8"
)

// boundedWriter is an io.Writer that keeps at most limit bytes of what is written
// to it, discards the rest, and counts every byte the writer produced either way.
// It backs a command's stdout and stderr so that a script writing an unbounded
// volume of output cannot grow the agent's heap until the service is OOM-killed:
// the bytes retained for one stream never exceed limit (plus the transient copy
// made while the backing array grows), no matter how much the command writes.
//
// Write never reports an error or a short write, so a command is never killed —
// nor handed a broken pipe — merely for being verbose: it runs to completion (or
// to its execution deadline) with the excess output dropped on the floor, and
// whatever it produced up to the ceiling is still delivered.
//
// Writes arrive on the goroutine os/exec starts to drain the command's output
// pipe, while the kept bytes are read on the goroutine that called cmd.Run.
// cmd.Wait normally joins that copier before returning, but an expired
// cmd.WaitDelay lets Wait return while the copier is still draining, so every
// access is guarded by the mutex.
type boundedWriter struct {
	mu       sync.Mutex
	limit    int
	kept     []byte
	produced int64
	// lastLine and partial track the last non-empty line for the progress
	// heartbeat (sc-129689) from every byte written, kept or not, so the line
	// keeps moving after the ceiling is reached. partial is the line in
	// progress since the last terminator; lastLine the last one completed.
	// Both are bounded to the lastOutputLineMax-byte tail.
	lastLine []byte
	partial  []byte
}

// newBoundedWriter returns a writer that keeps at most limit bytes. Callers pass
// a resolved, positive limit (see agent.Device.ResolvedMaxOutputBytes); a
// non-positive limit degenerates to keeping nothing.
func newBoundedWriter(limit int) *boundedWriter {
	return &boundedWriter{limit: limit}
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// n is captured before p is trimmed: the caller must always be told the whole
	// write was accepted, otherwise io.Copy stops draining the pipe with
	// ErrShortWrite and the command sees its output fail.
	n := len(p)
	w.produced += int64(n)
	w.trackLinesLocked(p)

	if remaining := w.limit - len(w.kept); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.appendLocked(p)
	}

	return n, nil
}

// appendLocked appends p to the kept bytes, growing the backing array by doubling
// but never past limit. Capping the capacity — rather than letting append double
// past the limit on the write that fills it — is what keeps both the retained
// bytes and the transient copy made while growing a small constant multiple of
// the limit. p is always short enough that the total stays within limit.
func (w *boundedWriter) appendLocked(p []byte) {
	if need := len(w.kept) + len(p); cap(w.kept) < need {
		grown := 2 * cap(w.kept)
		if grown < need {
			grown = need
		}
		if grown > w.limit {
			grown = w.limit
		}
		next := make([]byte, len(w.kept), grown)
		copy(next, w.kept)
		w.kept = next
	}
	w.kept = append(w.kept, p...)
}

// trackLinesLocked folds p into the last-line tail. Both '\n' and '\r' end a
// line, so a progress bar redrawn with bare carriage returns reports its
// latest frame, and CRLF endings leave no stray '\r'; an empty line never
// replaces the last non-empty one. The work is linear in the write and keeps
// at most lastOutputLineMax bytes, however long the line.
func (w *boundedWriter) trackLinesLocked(p []byte) {
	for len(p) > 0 {
		i := bytes.IndexAny(p, "\r\n")
		if i < 0 {
			w.partial = appendTail(w.partial, p)
			return
		}
		w.partial = appendTail(w.partial, p[:i])
		if len(w.partial) > 0 {
			w.lastLine, w.partial = w.partial, w.lastLine[:0]
		}
		p = p[i+1:]
	}
}

// appendTail appends p to tail, keeping only the last lastOutputLineMax bytes.
func appendTail(tail, p []byte) []byte {
	if len(p) >= lastOutputLineMax {
		return append(tail[:0], p[len(p)-lastOutputLineMax:]...)
	}
	tail = append(tail, p...)
	if excess := len(tail) - lastOutputLineMax; excess > 0 {
		n := copy(tail, tail[excess:])
		tail = tail[:n]
	}
	return tail
}

// LastLine returns the last non-empty line written so far - the line in
// progress when the output does not end in a terminator - truncated to its
// last lastOutputLineMax bytes and cut on a rune boundary, so a long line of
// multi-byte characters never starts with a continuation byte.
func (w *boundedWriter) LastLine() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	line := w.partial
	if len(line) == 0 {
		line = w.lastLine
	}
	for len(line) > 0 && !utf8.RuneStart(line[0]) {
		line = line[1:]
	}
	return string(line)
}

// String returns the bytes that were kept, which for output below the ceiling is
// everything the command wrote.
func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.kept)
}

// Len returns how many bytes were kept.
func (w *boundedWriter) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.kept)
}

// Produced returns the total number of bytes written to the stream, including the
// bytes that were discarded once the ceiling was reached.
func (w *boundedWriter) Produced() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.produced
}

// Truncated reports whether any output was discarded.
func (w *boundedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.produced > int64(len(w.kept))
}

// truncationOf summarizes the truncation state of a command's two output streams.
// The byte counts are totals across stdout and stderr (each of which is bounded
// independently) so a single pair of numbers describes how much output the
// command produced and how much of it survived into the result.
func truncationOf(stdout, stderr *boundedWriter) outputTruncation {
	return outputTruncation{
		Truncated: stdout.Truncated() || stderr.Truncated(),
		Produced:  stdout.Produced() + stderr.Produced(),
		Kept:      int64(stdout.Len() + stderr.Len()),
	}
}
