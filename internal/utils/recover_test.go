package utils

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestRecoverContainsPanicAndStack(t *testing.T) {
	buf := bytes.Buffer{}
	logger := ConfigureLogger("test", &buf, Error)

	func() {
		defer Recover(logger, "worker", 3)
		panic("boom")
	}()

	output := buf.String()
	if !strings.Contains(output, "Recovered from panic") {
		t.Errorf("expected recovery log message, got %q", output)
	}
	if !strings.Contains(output, "boom") {
		t.Errorf("expected recovered value in log, got %q", output)
	}
	if !strings.Contains(output, "stack=") {
		t.Errorf("expected stack trace in log, got %q", output)
	}
	if !strings.Contains(output, "worker=3") {
		t.Errorf("expected keyvals in log, got %q", output)
	}
	if !strings.Contains(strings.ToLower(output), "[error]") {
		t.Errorf("expected error-level log, got %q", output)
	}
}

func TestRecoverNoPanicIsNoOp(t *testing.T) {
	buf := bytes.Buffer{}
	logger := ConfigureLogger("test", &buf, Error)

	func() {
		defer Recover(logger)
		// no panic
	}()

	if buf.Len() != 0 {
		t.Errorf("expected no log output when no panic, got %q", buf.String())
	}
}

func TestRecoverNilLoggerDoesNotPanic(t *testing.T) {
	// Must not itself panic when logger is nil.
	func() {
		defer Recover(nil)
		panic("boom")
	}()
}

func TestLogRecoveredPanicReturnsError(t *testing.T) {
	buf := bytes.Buffer{}
	logger := ConfigureLogger("test", &buf, Error)

	err := LogRecoveredPanic(logger, "kapow")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "kapow") {
		t.Errorf("expected recovered value in error, got %q", err.Error())
	}
}

// signalWriter is a concurrency-safe io.Writer that closes done after the
// first write, letting a test wait until the logger has emitted output from a
// SafeGo goroutine without racing on the underlying buffer.
type signalWriter struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	once sync.Once
	done chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	w.once.Do(func() { close(w.done) })
	return n, err
}

func (w *signalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestSafeGoRecoversAndContinues(t *testing.T) {
	w := &signalWriter{done: make(chan struct{})}
	logger := ConfigureLogger("test", w, Error)

	// A panic inside the SafeGo goroutine must be recovered (so the test
	// process survives) and logged.
	SafeGo(logger, func() {
		panic("goroutine boom")
	}, "scope", "test")

	<-w.done

	if !strings.Contains(w.String(), "goroutine boom") {
		t.Errorf("expected recovered goroutine panic to be logged, got %q", w.String())
	}
}
