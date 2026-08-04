package interpreter

import "sync"

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
