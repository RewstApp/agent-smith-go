// Command notifier_plugin is a minimal Notifier plugin used by the loader tests
// to exercise real subprocess crash and restart handling. Every notification it
// receives is appended to the file named by the AGENT_SMITH_TEST_NOTIFY_LOG
// environment variable, so a test can assert that delivery survives the plugin
// process being killed and relaunched.
//
// It lives under testdata so it is skipped by ./... build and vet patterns; the
// test builds it explicitly by path.
package main

import (
	"flag"
	"os"

	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/hashicorp/go-plugin"
)

// notifyLogEnvVar names the file notifications are appended to. It is read from
// the environment because the host launches plugins with a fixed argument list.
const notifyLogEnvVar = "AGENT_SMITH_TEST_NOTIFY_LOG"

type fileNotifier struct {
	path string
}

func (n *fileNotifier) Notify(message string) error {
	file, err := os.OpenFile(n.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	defer func() {
		_ = file.Close()
	}()

	_, err = file.WriteString(message + "\n")

	return err
}

func main() {
	// The host passes the handshake secret on the command line, exactly as a real
	// Agent Smith notification plugin receives it.
	magicCookieKey := flag.String("magic-cookie-key", "", "Handshake magic cookie key")
	magicCookieValue := flag.String("magic-cookie-value", "", "Handshake magic cookie value")
	flag.Parse()

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: plugin.HandshakeConfig{
			ProtocolVersion:  1,
			MagicCookieKey:   *magicCookieKey,
			MagicCookieValue: *magicCookieValue,
		},
		Plugins: map[string]plugin.Plugin{
			"notifier": &shared.NotifierPlugin{
				Impl: &fileNotifier{path: os.Getenv(notifyLogEnvVar)},
			},
		},
	})
}
