package shared

import (
	"errors"
	"net/rpc"
	"time"

	"github.com/hashicorp/go-plugin"
)

// NotifyTimeout bounds how long the host waits for a single Notify RPC call to
// a plugin subprocess to return.
//
// net/rpc's Call blocks until a response arrives, with no deadline of its own.
// A plugin that accepts the call and then hangs internally — deadlocks on a
// channel, blocks forever on a downstream call — without exiting the process
// previously blocked the calling worker forever. That failure mode is invisible
// to the health check, which only detects a subprocess that has actually
// exited: the process is still alive, so nothing notices it. Because Notify
// fires on every message and status transition, this could silently exhaust
// the whole worker pool over time.
//
// Notify is a purely local subprocess round trip carrying a short string — no
// network I/O — so it normally completes in microseconds. 10s is generous
// slack for a plugin doing brief synchronous work in its Notify handler
// (writing to disk, a quick local API call) while still recovering a truly
// hung plugin in bounded time.
//
// It is a var rather than a const solely so tests can shrink it to exercise
// the timeout path without a real multi-second wait; production code never
// modifies it, and it is deliberately not exposed as a device config knob —
// unlike the MQTT timeouts, it bounds host-internal RPC to a subprocess the
// host itself launched, not a network endpoint an operator might need to tune.
var NotifyTimeout = 10 * time.Second

// ErrNotifyTimeout is returned by NotifierRPC.Notify when a plugin subprocess
// does not respond to a Notify call within NotifyTimeout. It is distinct from
// an error the plugin itself returned (net/rpc wraps those in rpc.ServerError)
// and from a broken transport (rpc.ErrShutdown, io.EOF): the process is still
// alive, it simply never answered. Callers that relaunch on a broken transport
// should treat this the same way, since a plugin that hangs once is liable to
// hang again.
var ErrNotifyTimeout = errors.New("notification plugin: RPC call timed out waiting for response")

// Notifier is the interface exposed to the host.
type Notifier interface {
	Notify(message string) error
}

// Args for Notify
type NotifyArgs struct {
	Message string
}

// EmptyReply placeholder (needed for net/rpc signature)
type EmptyReply struct{}

// Plugin wrapper for go-plugin
type NotifierPlugin struct {
	Impl Notifier
}

func (p *NotifierPlugin) Server(*plugin.MuxBroker) (interface{}, error) {
	return &NotifierRPCServer{Impl: p.Impl}, nil
}

func (p *NotifierPlugin) Client(b *plugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &NotifierRPC{client: c, timeout: NotifyTimeout}, nil
}

// Server-side implementation of the RPC
type NotifierRPCServer struct {
	Impl Notifier
}

func (s *NotifierRPCServer) Notify(args NotifyArgs, reply *EmptyReply) error {
	return s.Impl.Notify(args.Message)
}

// Client-side proxy
type NotifierRPC struct {
	client *rpc.Client

	// timeout bounds a single Notify call; zero falls back to NotifyTimeout. It
	// exists as a per-instance field (rather than always reading the package var
	// directly) so a value captured at dispense time cannot change mid-call.
	timeout time.Duration
}

// Notify makes the RPC call asynchronously via client.Go rather than the
// blocking client.Call so it can race the response against NotifyTimeout: a
// plugin that accepts the call and never replies must not block the caller
// forever. The done channel is buffered (required by client.Go) so an
// abandoned call's late reply, if the plugin ever answers, does not block or
// leak the goroutine that eventually delivers it.
func (c *NotifierRPC) Notify(message string) error {
	args := NotifyArgs{Message: message}
	var reply EmptyReply // not used

	timeout := c.timeout
	if timeout <= 0 {
		timeout = NotifyTimeout
	}

	call := c.client.Go("Plugin.Notify", args, &reply, make(chan *rpc.Call, 1))

	select {
	case <-call.Done:
		return call.Error
	case <-time.After(timeout):
		return ErrNotifyTimeout
	}
}
