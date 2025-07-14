package shared

import (
	"net/rpc"

	"github.com/hashicorp/go-plugin"
)

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
	return &NotifierRPC{client: c}, nil
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
}

func (c *NotifierRPC) Notify(message string) error {
	args := NotifyArgs{Message: message}
	var reply EmptyReply // not used
	return c.client.Call("Plugin.Notify", args, &reply)
}
