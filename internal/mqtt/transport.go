package mqtt

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

// openConnection opens each broker connection paho attempts (sc-129691):
// MQTT over TLS directly, and MQTT over WebSocket through the proxy that
// applies to it, tunnelled with utils.DialTunnel - the same tunnel
// --diagnostic tests - so http:// and https:// proxies both work and a
// proxy's refusal is an error rather than a panic.
func openConnection(proxy utils.ProxyFunc) mqtt.OpenConnectionFunc {
	return func(broker *url.URL, o mqtt.ClientOptions) (net.Conn, error) {
		timeout := o.ConnectTimeout
		if timeout <= 0 {
			timeout = utils.DefaultMqttConnectTimeout
		}
		switch broker.Scheme {
		case "ws", "wss":
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return dialWebSocket(ctx, broker, o, BrokerProxy(proxy, broker))
		case "ssl", "tls", "mqtts", "mqtt+ssl", "tcps":
			return tls.DialWithDialer(
				&net.Dialer{Timeout: timeout},
				"tcp",
				broker.Host,
				o.TLSConfig,
			)
		default:
			return nil, fmt.Errorf("unsupported broker scheme %q", broker.Scheme)
		}
	}
}

func dialWebSocket(
	ctx context.Context,
	broker *url.URL,
	o mqtt.ClientOptions,
	proxy *url.URL,
) (net.Conn, error) {
	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return utils.DialTunnel(ctx, proxy, addr)
		},
		TLSClientConfig: o.TLSConfig,
		Subprotocols:    []string{"mqtt"},
	}
	if deadline, ok := ctx.Deadline(); ok {
		dialer.HandshakeTimeout = time.Until(deadline)
	}
	ws, resp, err := dialer.DialContext(ctx, broker.String(), o.HTTPHeaders)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return &wsConn{ws: ws}, nil
}

// wsConn presents a WebSocket as the byte stream MQTT expects: each write is
// one binary message, and reads run across messages.
type wsConn struct {
	ws  *websocket.Conn
	r   io.Reader
	rmu sync.Mutex
	wmu sync.Mutex
}

func (c *wsConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.r == nil {
			kind, r, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error         { return c.ws.Close() }
func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
