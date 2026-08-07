//go:build integration

// Command stubbroker is a deliberately minimal MQTT 3.1.1 broker used by the
// integration test suite to reproduce the failure mode sc-106107 fixed: a broker
// that completes the TLS handshake, answers CONNECT with a normal CONNACK, and
// keeps answering PINGREQ so keepalive stays healthy — while never sending
// SUBACK for the client's SUBSCRIBE.
//
// That combination is what Azure IoT Hub produces when it throttles a device,
// and what a middlebox that half-opens a connection (stateful firewall or VPN
// idle-timeout, captive portal, some SD-WAN appliances) produces incidentally.
// It cannot be simulated by cutting the network — a severed connection is
// detected by keepalive and surfaces as a normal reconnect. It has to be a
// broker that stays alive and simply withholds the acknowledgement.
//
// The broker implements only the packet types the agent exercises and keeps no
// session state; it is a test fixture, not a usable MQTT server.
//
// It mints its own certificate chain on startup and writes the CA certificate to
// -ca-out so the caller can install it into the host trust store, since the agent
// verifies the broker's TLS certificate against the system roots like any other
// Azure IoT Hub connection. The certificate always carries the loopback
// addresses as SANs, so the agent can be pointed straight at 127.0.0.1 and no
// hosts-file entry (nor any name resolution) is involved.
//
// Whether a SUBSCRIBE is acknowledged is read from -mode-file on every packet,
// so the harness can flip the broker between withholding and acknowledging
// without restarting it or disturbing the live connection.
//
// It sits behind the `integration` build tag so it stays out of
// `go test ./...`: as an untested CI fixture its statements would otherwise
// count against the repository coverage threshold. It is still linted, because
// the tag is listed in .golangci.yml. Build it with
// `go build -tags integration ./test/stubbroker`.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"strings"
	"time"
)

// MQTT 3.1.1 control packet types, in the high nibble of the fixed header.
const (
	pktConnect     byte = 1
	pktConnack     byte = 2
	pktPublish     byte = 3
	pktPuback      byte = 4
	pktSubscribe   byte = 8
	pktSuback      byte = 9
	pktUnsubscribe byte = 10
	pktUnsuback    byte = 11
	pktPingreq     byte = 12
	pktPingresp    byte = 13
	pktDisconnect  byte = 14
)

// modeAck is the -mode-file content that makes the broker acknowledge
// subscriptions normally. Any other content (including a missing file) withholds
// the acknowledgement, so the default — and the failure mode on a misconfigured
// harness — is the one the scenario is built around.
const modeAck = "ack"

// certValidity is deliberately short. macOS rejects TLS server certificates with
// an excessive lifetime even when the issuing root is locally trusted, so a
// long-lived fixture certificate would fail verification on one of the three
// platforms only.
const certValidity = 30 * 24 * time.Hour

func main() {
	listen := flag.String("listen", ":8883", "address to listen on")
	host := flag.String("host", "", "DNS name to issue the server certificate for (required)")
	caOut := flag.String("ca-out", "", "path to write the CA certificate PEM to (required)")
	modeFile := flag.String(
		"mode-file",
		"",
		fmt.Sprintf("path to a file containing %q to acknowledge subscriptions; "+
			"any other content withholds SUBACK", modeAck),
	)
	flag.Parse()

	if *host == "" || *caOut == "" {
		log.Fatal("both -host and -ca-out are required")
	}

	tlsConfig, caPEM, err := newTLSConfig(*host)
	if err != nil {
		log.Fatalf("failed to mint certificate chain: %v", err)
	}
	if err := os.WriteFile(*caOut, caPEM, 0o644); err != nil {
		log.Fatalf("failed to write CA certificate: %v", err)
	}

	listener, err := tls.Listen("tcp", *listen, tlsConfig)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", *listen, err)
	}
	defer func() { _ = listener.Close() }()

	log.Printf("stub broker listening on %s for %s (mode file %q)", *listen, *host, *modeFile)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept failed: %v", err)
			continue
		}
		go serve(conn, *modeFile)
	}
}

// newTLSConfig mints a throwaway CA and a leaf certificate for host, returning a
// server TLS config using the leaf and the CA certificate in PEM form. A real
// two-level chain is used rather than a single self-signed certificate because
// the platform verifiers differ in how willingly they accept a certificate that
// is simultaneously its own root and the TLS leaf.
func newTLSConfig(host string) (*tls.Config, []byte, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Rewst Integration Test Stub Broker CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(
		rand.Reader,
		caTemplate,
		caTemplate,
		&caKey.PublicKey,
		caKey,
	)
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, nil, err
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// The loopback addresses are always present so the agent can be pointed
		// straight at 127.0.0.1, with no hosts-file entry and therefore no name
		// resolution involved at all. The verifiers on all three platforms match
		// against the SAN, not the common name, so an IP host has to land in
		// IPAddresses; putting an IP literal in DNSNames would simply never match.
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			leafTemplate.IPAddresses = append(leafTemplate.IPAddresses, ip)
		}
	} else {
		leafTemplate.DNSNames = []string{host}
	}
	leafDER, err := x509.CreateCertificate(
		rand.Reader,
		leafTemplate,
		caCert,
		&leafKey.PublicKey,
		caKey,
	)
	if err != nil {
		return nil, nil, err
	}

	cert := tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafKey,
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, caPEM, nil
}

// acknowledgesSubscriptions reports whether the broker should currently answer a
// SUBSCRIBE. It is re-read per packet so the harness can flip the behavior on a
// live connection.
func acknowledgesSubscriptions(modeFile string) bool {
	if modeFile == "" {
		return false
	}
	content, err := os.ReadFile(modeFile)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(content)) == modeAck
}

func serve(conn net.Conn, modeFile string) {
	defer func() { _ = conn.Close() }()

	remote := conn.RemoteAddr().String()
	log.Printf("[%s] connection accepted", remote)

	for {
		packetType, flags, payload, err := readPacket(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("[%s] read failed: %v", remote, err)
			}
			log.Printf("[%s] connection closed", remote)
			return
		}

		switch packetType {
		case pktConnect:
			log.Printf("[%s] CONNECT -> CONNACK", remote)
			// Variable header: session-present flag 0, return code 0 (accepted).
			if err := writePacket(conn, pktConnack, 0, []byte{0x00, 0x00}); err != nil {
				log.Printf("[%s] failed to send CONNACK: %v", remote, err)
				return
			}

		case pktPingreq:
			// Answered unconditionally, including while withholding SUBACK: a
			// healthy keepalive is what makes the reproduction faithful. Without
			// it the client would tear the connection down on ping timeout and
			// exercise the ordinary connection-lost path instead.
			log.Printf("[%s] PINGREQ -> PINGRESP", remote)
			if err := writePacket(conn, pktPingresp, 0, nil); err != nil {
				log.Printf("[%s] failed to send PINGRESP: %v", remote, err)
				return
			}

		case pktSubscribe:
			if len(payload) < 2 {
				log.Printf("[%s] malformed SUBSCRIBE", remote)
				return
			}
			packetID := payload[:2]
			if !acknowledgesSubscriptions(modeFile) {
				log.Printf("[%s] SUBSCRIBE -> withholding SUBACK", remote)
				break
			}
			log.Printf("[%s] SUBSCRIBE -> SUBACK", remote)
			// One granted-QoS byte per requested topic filter; the agent
			// subscribes to exactly one, and QoS 1 is granted regardless of what
			// it asked for (a broker may grant lower, never higher).
			if err := writePacket(
				conn,
				pktSuback,
				0,
				[]byte{packetID[0], packetID[1], 0x01},
			); err != nil {
				log.Printf("[%s] failed to send SUBACK: %v", remote, err)
				return
			}

		case pktUnsubscribe:
			if len(payload) < 2 {
				log.Printf("[%s] malformed UNSUBSCRIBE", remote)
				return
			}
			packetID := payload[:2]
			if !acknowledgesSubscriptions(modeFile) {
				log.Printf("[%s] UNSUBSCRIBE -> withholding UNSUBACK", remote)
				break
			}
			log.Printf("[%s] UNSUBSCRIBE -> UNSUBACK", remote)
			if err := writePacket(conn, pktUnsuback, 0, packetID); err != nil {
				log.Printf("[%s] failed to send UNSUBACK: %v", remote, err)
				return
			}

		case pktPublish:
			// The agent's device-twin reported-properties publish is QoS 0 and
			// needs no response. A QoS 1 publish is acknowledged so the fixture
			// stays correct if that ever changes; its packet id follows the topic.
			qos := (flags >> 1) & 0x03
			if qos != 1 {
				log.Printf("[%s] PUBLISH (qos %d) -> no response required", remote, qos)
				break
			}
			if len(payload) < 2 {
				log.Printf("[%s] malformed PUBLISH", remote)
				return
			}
			topicLen := int(payload[0])<<8 | int(payload[1])
			if len(payload) < 2+topicLen+2 {
				log.Printf("[%s] malformed PUBLISH", remote)
				return
			}
			packetID := payload[2+topicLen : 2+topicLen+2]
			log.Printf("[%s] PUBLISH (qos 1) -> PUBACK", remote)
			if err := writePacket(conn, pktPuback, 0, packetID); err != nil {
				log.Printf("[%s] failed to send PUBACK: %v", remote, err)
				return
			}

		case pktDisconnect:
			log.Printf("[%s] DISCONNECT", remote)
			return

		default:
			log.Printf("[%s] ignoring packet type %d", remote, packetType)
		}
	}
}

// readPacket reads one MQTT control packet, returning its type, its fixed-header
// flags, and the remaining bytes (variable header plus payload).
func readPacket(conn net.Conn) (byte, byte, []byte, error) {
	var header [1]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, 0, nil, err
	}

	remaining, err := readRemainingLength(conn)
	if err != nil {
		return 0, 0, nil, err
	}

	payload := make([]byte, remaining)
	if remaining > 0 {
		if _, err := io.ReadFull(conn, payload); err != nil {
			return 0, 0, nil, err
		}
	}

	return header[0] >> 4, header[0] & 0x0F, payload, nil
}

// readRemainingLength decodes MQTT's variable-length integer encoding: seven
// bits per byte, with the top bit signalling that another byte follows, up to
// four bytes.
func readRemainingLength(conn net.Conn) (int, error) {
	var (
		value      int
		multiplier = 1
	)
	for i := 0; i < 4; i++ {
		var b [1]byte
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			return 0, err
		}
		value += int(b[0]&0x7F) * multiplier
		if b[0]&0x80 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("malformed remaining length")
}

func writePacket(conn net.Conn, packetType, flags byte, payload []byte) error {
	packet := []byte{packetType<<4 | flags}
	packet = append(packet, encodeRemainingLength(len(payload))...)
	packet = append(packet, payload...)
	_, err := conn.Write(packet)
	return err
}

func encodeRemainingLength(length int) []byte {
	var encoded []byte
	for {
		b := byte(length % 128)
		length /= 128
		if length > 0 {
			b |= 0x80
		}
		encoded = append(encoded, b)
		if length == 0 {
			return encoded
		}
	}
}
