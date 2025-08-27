package plugins

import (
	"io"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/hashicorp/go-plugin"
)

var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BASIC_PLUGIN",
	MagicCookieValue: "hello",
}

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

	rpcClient, err := client.Client()
	if err != nil {
		return &optionalNotifierWrapper{}, err
	}

	raw, err := rpcClient.Dispense("notifier")
	if err != nil {
		return &optionalNotifierWrapper{}, err
	}

	return &optionalNotifierWrapper{
		client: client,
		plugin: raw.(shared.Notifier),
	}, nil
}
