package utils

import (
	"fmt"
	"runtime/debug"

	"github.com/hashicorp/go-hclog"
)

// LogRecoveredPanic logs an already-recovered panic value at Error level along
// with a stack trace, and returns an error describing it.
//
// Call it from inside a deferred closure that has itself called recover() when
// the caller needs to react to the panic (for example, to set a named error
// return so existing error-handling continues). When logging alone is enough,
// prefer Recover. The keyvals are attached to the log entry to identify the
// goroutine/context where the panic occurred (e.g. "worker", id).
func LogRecoveredPanic(logger hclog.Logger, recovered any, keyvals ...any) error {
	args := make([]any, 0, len(keyvals)+4)
	args = append(args, keyvals...)
	args = append(args, "panic", recovered, "stack", string(debug.Stack()))

	if logger != nil {
		logger.Error("Recovered from panic", args...)
	}

	return fmt.Errorf("recovered from panic: %v", recovered)
}

// Recover is a deferred panic handler: it recovers any in-flight panic in the
// current goroutine and logs it at Error level with a stack trace. It is a
// no-op when no panic is in flight, so deferring it unconditionally is safe and
// it never changes normal (non-panic) control flow.
//
// It must be invoked directly via defer (defer utils.Recover(logger, ...)) so
// the recover() runs in the panicking goroutine's deferred call.
func Recover(logger hclog.Logger, keyvals ...any) {
	if r := recover(); r != nil {
		_ = LogRecoveredPanic(logger, r, keyvals...)
	}
}

// SafeGo runs fn in a new goroutine guarded by Recover, so a panic in fn is
// recovered and logged instead of crashing the process. keyvals are attached to
// the log entry emitted if fn panics.
func SafeGo(logger hclog.Logger, fn func(), keyvals ...any) {
	go func() {
		defer Recover(logger, keyvals...)
		fn()
	}()
}
