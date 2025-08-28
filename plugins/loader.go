package plugins

import (
	"errors"
	"io"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/google/uuid"
	"github.com/hashicorp/go-plugin"
)

const defaultProtocolVersion = 1
const defaultMagicCookieKey = "AGENT_SMITH"

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

type notifierSetWrapper struct {
	notifiers []*optionalNotifierWrapper
}

func (s *notifierSetWrapper) Kill() {
	for _, notifier := range s.notifiers {
		notifier.Kill()
	}
}

func (s *notifierSetWrapper) Notify(message string) error {
	var combinedErrors error

	for _, notifier := range s.notifiers {
		err := notifier.Notify(message)

		if err != nil {
			combinedErrors = errors.Join(combinedErrors, err)
		}
	}

	return combinedErrors
}

func LoadNotifer(plugins []agent.Plugin, logWriter io.Writer) (shared.Notifier, error) {
	set := &notifierSetWrapper{}
	var combinedErrors error

	for _, pluginInfo := range plugins {
		magicCookieValueUuid := uuid.New()

		handshakeConfig := plugin.HandshakeConfig{
			ProtocolVersion:  defaultProtocolVersion,
			MagicCookieKey:   defaultMagicCookieKey,
			MagicCookieValue: magicCookieValueUuid.String(),
		}

		client := plugin.NewClient(&plugin.ClientConfig{
			HandshakeConfig: handshakeConfig,
			Plugins:         pluginMap,
			Cmd:             exec.Command(pluginInfo.ExecutablePath, "--magic-cookie-key", handshakeConfig.MagicCookieKey, "--magic-cookie-value", handshakeConfig.MagicCookieValue),
			Stderr:          logWriter,
		})

		rpcClient, err := client.Client()
		if err != nil {
			combinedErrors = errors.Join(combinedErrors, err)
			continue
		}

		raw, err := rpcClient.Dispense("notifier")
		if err != nil {
			combinedErrors = errors.Join(combinedErrors, err)
			continue
		}

		set.notifiers = append(set.notifiers, &optionalNotifierWrapper{
			client: client,
			plugin: raw.(shared.Notifier),
		})
	}

	return set, combinedErrors
}
