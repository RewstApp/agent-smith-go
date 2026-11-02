package utils

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

var timeZero time.Time

// DialTunnel opens a TCP connection to addr through proxy with HTTP CONNECT
// (sc-129691), or directly when proxy is nil. It is the one tunnel the agent's
// MQTT WebSocket transport and --diagnostic both use, so the diagnostic tests
// exactly what the agent does. An https:// proxy is reached over TLS first; a
// user in the proxy URL is sent as Basic Proxy-Authorization, with an empty
// password when none is given. A refusal is an error naming the status, never
// a panic, whatever the status line looks like.
func DialTunnel(ctx context.Context, proxy *url.URL, addr string) (net.Conn, error) {
	var d net.Dialer
	if proxy == nil {
		return d.DialContext(ctx, "tcp", addr)
	}
	proxyAddr := proxy.Host
	if proxy.Port() == "" {
		port := "80"
		if proxy.Scheme == "https" {
			port = "443"
		}
		proxyAddr = net.JoinHostPort(proxy.Hostname(), port)
	}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to reach the proxy %s: %w", DescribeProxy(proxy), err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if proxy.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: proxy.Hostname(),
			MinVersion: tls.VersionTLS12,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("TLS to the proxy %s failed: %w", DescribeProxy(proxy), err)
		}
		conn = tlsConn
	}
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: http.Header{},
	}
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		credentials := proxy.User.Username() + ":" + password
		req.Header.Set(
			"Proxy-Authorization",
			"Basic "+base64.StdEncoding.EncodeToString([]byte(credentials)),
		)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf(
			"failed to send CONNECT to the proxy %s: %w",
			DescribeProxy(proxy),
			err,
		)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("the proxy %s did not answer CONNECT: %w", DescribeProxy(proxy), err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf(
			"the proxy %s refused CONNECT %s with status %d",
			DescribeProxy(proxy), addr, resp.StatusCode,
		)
	}
	_ = conn.SetDeadline(timeZero)
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// bufferedConn hands out what the CONNECT reader already buffered before
// reading the connection itself.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
