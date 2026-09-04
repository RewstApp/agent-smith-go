//go:build integration

// Command stubconfig is a deliberately minimal stand-in for the Rewst config
// endpoint, used by the integration test suite to drive the config-fetch
// response size ceiling sc-112405 added: an endpoint that answers install-time
// config mode with a body far larger than any real configuration, so
// maxConfigResponseSize can be observed aborting the install rather than
// buffering whatever arrives.
//
// The real config endpoint cannot be made to serve an oversized body on demand,
// and an unroutable address produces a connection error rather than the
// 200-with-an-enormous-body the ticket describes. The stub answers on loopback
// over plain HTTP - no TLS, so unlike the stub broker it needs no CA in the host
// trust store - and the agent is pointed at it with --config-url.
//
// Whether a request is answered with an oversized body or a small valid
// configuration is read from -mode-file on every request, so the harness can
// flip the endpoint without restarting it. The default (and the behavior with a
// missing mode file) is oversized, so a harness that forgets to set a mode
// reproduces the scenario the fixture exists for rather than silently passing.
//
// The oversized body is streamed in chunks with no Content-Length (chunked
// transfer encoding), which is the shape that matters: the ceiling has to hold
// regardless of what a header claims, since a hostile endpoint can omit or lie
// about it. How many bytes were actually written before the client hung up is
// logged, and that count is the fixture's real assertion surface - an agent that
// honors the ceiling stops reading a few megabytes in, so the fixture reports
// far fewer bytes written than it was asked to serve, while an agent buffering
// the whole body would show the full amount.
//
// It sits behind the `integration` build tag so it stays out of
// `go test ./...`: as an untested CI fixture its statements would otherwise
// count against the repository coverage threshold. It is still linted, because
// the tag is listed in .golangci.yml. Build it with
// `go build -tags integration ./test/stubconfig`.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// modeOk is the -mode-file content that makes the endpoint answer with a small
// valid configuration. Any other content (including a missing file) serves the
// oversized body instead.
const modeOk = "ok"

// readTimeout bounds reading a single request so a stuck client cannot pin the
// fixture for the rest of the job. There is deliberately no write timeout: the
// oversized response is a long streaming write by design, and a deadline on it
// would cut the body off at a duration rather than letting the agent's own
// ceiling be what ends the exchange - which is the whole thing under test.
const readTimeout = 10 * time.Second

// chunkSize is how much of the oversized body is written per Write call. Small
// enough that the stream is genuinely incremental (so the agent's read is what
// stops it, mid-body), large enough not to spend the scenario in syscalls.
const chunkSize = 64 * 1024

// oversizedFiller is the byte the oversized body is padded with. The body is
// deliberately not valid JSON past its opening: nothing should ever parse it,
// and a fixture that served parseable content would leave "was it rejected for
// its size or its syntax?" open.
const oversizedFiller = 'x'

func main() {
	listen := flag.String("listen", "127.0.0.1:8766", "address to listen on")
	modeFile := flag.String(
		"mode-file",
		"",
		fmt.Sprintf("path to a file containing %q to answer with a small valid "+
			"configuration; any other content (or a missing file) serves the "+
			"oversized body", modeOk),
	)
	oversizedBytes := flag.Int64(
		"oversized-bytes",
		1024*1024*1024,
		"how many bytes to stream in oversized mode; must be far above the "+
			"agent's maxConfigResponseSize so a truncated read is unambiguous",
	)
	orgId := flag.String(
		"org-id",
		"",
		"rewst_org_id to report in the configuration; must match the --org-id "+
			"the agent is installed with or the install rejects the config",
	)
	deviceId := flag.String("device-id", "stub-device-id", "device_id to report")
	engineHost := flag.String(
		"engine-host",
		"engine.invalid",
		"rewst_engine_host to report; a .invalid host by default, since nothing "+
			"in this scenario posts results anywhere",
	)
	hubHost := flag.String(
		"hub-host",
		"hub.invalid",
		"azure_iot_hub_host to report; a .invalid host by default, since this "+
			"scenario asserts the install completes, not that it connects",
	)
	logPath := flag.String("log", "", "path to write this endpoint's log to (defaults to stderr)")
	flag.Parse()

	// Redirected here rather than by the launching shell for the same reason the
	// stub release endpoint does it: on Windows the fixture has to be launched
	// detached from the step that starts it, and a detached launch has nowhere to
	// attach a redirect. Set up first so a startup failure - a port already in
	// use, say - lands in the file the harness reports.
	if *logPath != "" {
		logFile, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", *logPath, err)
			os.Exit(1)
		}
		defer func() { _ = logFile.Close() }()
		log.SetOutput(logFile)
	}

	config := deviceConfig{
		DeviceId:        *deviceId,
		RewstOrgId:      *orgId,
		RewstEngineHost: *engineHost,
		// Valid base64 ("stub-shared-access-key"), not an arbitrary string: the
		// service the install starts derives its SAS token by base64-decoding
		// this, and a key that will not decode makes the agent exit with
		// GenericError on its first connection cycle rather than backing off -
		// which would look like the install having failed. It authenticates
		// against nothing; the control leg asserts the install completed, not
		// that it connected.
		SharedAccessKey: "c3R1Yi1zaGFyZWQtYWNjZXNzLWtleQ==",
		AzureIotHubHost: *hubHost,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, *modeFile, *oversizedBytes, config)
	})

	server := &http.Server{
		Addr:        *listen,
		Handler:     mux,
		ReadTimeout: readTimeout,
	}

	log.Printf(
		"stub config endpoint listening on %s (mode file %q, oversized body %d bytes)",
		*listen,
		*modeFile,
		*oversizedBytes,
	)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("failed to serve on %s: %v", *listen, err)
	}
}

// deviceConfig is the subset of agent.Device the install path requires: the four
// fields validateConfiguration insists on, plus the org id it cross-checks. It
// is declared here rather than imported so the fixture stays independent of the
// agent's own struct - a config the agent would reject has to be expressible.
type deviceConfig struct {
	DeviceId        string `json:"device_id"`
	RewstOrgId      string `json:"rewst_org_id"`
	RewstEngineHost string `json:"rewst_engine_host"`
	SharedAccessKey string `json:"shared_access_key"`
	AzureIotHubHost string `json:"azure_iot_hub_host"`
}

// configResponse mirrors the envelope config mode unmarshals into.
type configResponse struct {
	Configuration deviceConfig `json:"configuration"`
}

// handle answers a single request according to the current mode. The mode is
// read per request so the harness can flip the endpoint between the oversized
// and the valid response without restarting it.
func handle(
	w http.ResponseWriter,
	r *http.Request,
	modeFile string,
	oversizedBytes int64,
	config deviceConfig,
) {
	arrived := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	// Presence only, never the value: the config secret is a repository secret
	// and this log is uploaded with the job.
	haveSecret := r.Header.Get("x-rewst-secret") != ""

	if currentModeIsOk(modeFile) {
		serveConfig(w, r, arrived, haveSecret, config)
		return
	}
	serveOversized(w, r, arrived, haveSecret, oversizedBytes)
}

// serveConfig answers with the small valid configuration - the control leg,
// which has to keep working: a size ceiling that also rejected legitimate
// payloads would pass every assertion about the oversized body and still break
// every install.
func serveConfig(
	w http.ResponseWriter,
	r *http.Request,
	arrived string,
	haveSecret bool,
	config deviceConfig,
) {
	body, err := json.Marshal(configResponse{Configuration: config})
	if err != nil {
		// Unreachable for this fixed payload, but failing loudly beats serving a
		// truncated body the agent would report as a parse error.
		log.Printf("%s failed to marshal config payload: %v", arrived, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf(
		"%s %s %s -> 200 (mode=ok, secret_header=%t, config payload %d bytes)",
		arrived, r.Method, r.URL.Path, haveSecret, len(body),
	)
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		log.Printf("%s failed to write config payload: %v", arrived, err)
	}
}

// serveOversized streams total bytes of filler, or as many as the client will
// take before it hangs up, and logs which of those two happened. No
// Content-Length is set, so the response is chunked: the ceiling must hold on
// the bytes actually delivered, not on what a header promised.
func serveOversized(
	w http.ResponseWriter,
	r *http.Request,
	arrived string,
	haveSecret bool,
	total int64,
) {
	log.Printf(
		"%s %s %s -> 200 (mode=oversized, secret_header=%t, streaming %d bytes)",
		arrived, r.Method, r.URL.Path, haveSecret, total,
	)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	// A flusher is not strictly required for chunked writes to reach the client,
	// but flushing per chunk keeps the stream incremental rather than letting the
	// server buffer decide how much arrives at once - the agent has to be the
	// thing that stops the exchange.
	flusher, canFlush := w.(http.Flusher)

	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = oversizedFiller
	}

	var written int64
	for written < total {
		size := int64(len(chunk))
		if remaining := total - written; remaining < size {
			size = remaining
		}
		n, err := w.Write(chunk[:size])
		written += int64(n)
		if err != nil {
			// The expected outcome: the agent hit its ceiling and closed the body,
			// so this write failed. The byte count is the measurement the scenario
			// asserts on.
			log.Printf(
				"%s client hung up after %d bytes of %d (%v)",
				time.Now().Format("2006-01-02T15:04:05.000Z07:00"), written, total, err,
			)
			return
		}
		if canFlush {
			flusher.Flush()
		}
	}

	// Reaching here means the client read the entire oversized body, which is the
	// unbounded-read bug itself. Logged distinctly so the scenario fails on the
	// count rather than on a missing log line.
	log.Printf(
		"%s served the full oversized body: %d bytes written",
		time.Now().Format("2006-01-02T15:04:05.000Z07:00"), written,
	)
}

// currentModeIsOk reports whether the mode file currently asks for the valid
// configuration. A missing or unreadable file means oversized, so the fixture's
// default is the scenario it exists for.
func currentModeIsOk(modeFile string) bool {
	if modeFile == "" {
		return false
	}
	contents, err := os.ReadFile(modeFile)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(contents)), modeOk)
}
