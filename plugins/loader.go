package plugins

import (
	"io"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/hashicorp/go-plugin"
)

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BASIC_PLUGIN",
	MagicCookieValue: "hello",
}

// pluginMap is the map of plugins we can dispense.
var pluginMap = map[string]plugin.Plugin{
	"notifier": &shared.NotifierPlugin{},
}

type optionalNotifierWrapper struct {
	client *plugin.Client
	plugin shared.Notifier
}

func (p *optionalNotifierWrapper) Kill() {
	if p.client == nil {
		return
	}

	p.client.Kill()
}

func (p *optionalNotifierWrapper) Notify(message string) error {
	if p.plugin == nil {
		return nil
	}

	return p.plugin.Notify(message)
}

func LoadNotifer(path string, logWriter io.Writer) (shared.Notifier, error) {
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(path),
		Stderr:          logWriter,
	})

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		return &optionalNotifierWrapper{}, err
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("notifier")
	if err != nil {
		return &optionalNotifierWrapper{}, err
	}

	// We should have a Greeter now! This feels like a normal interface
	// implementation but is in fact over an RPC connection.
	return &optionalNotifierWrapper{
		client: client,
		plugin: raw.(shared.Notifier),
	}, nil
}
