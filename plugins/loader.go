package plugins

import (
	"io"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/google/uuid"
	"github.com/hashicorp/go-plugin"
)

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
	magicCookieValueUuid := uuid.New()

	handshakeConfig := plugin.HandshakeConfig{
		ProtocolVersion:  1,
		MagicCookieKey:   "AGENT_SMITH",
		MagicCookieValue: magicCookieValueUuid.String(),
	}

	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(path, "--magic-cookie-key", handshakeConfig.MagicCookieKey, "--magic-cookie-value", handshakeConfig.MagicCookieValue),
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
