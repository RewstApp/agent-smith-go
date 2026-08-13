//go:build integration

// Command stubrelease is a deliberately minimal stand-in for the GitHub releases
// API, used by the integration test suite to drive the auto-update retry path
// that sc-106110 capped and jittered: a release endpoint that fails on demand,
// so the agent enters retryWithBackoff and its schedule can be observed, and
// then recovers on demand so a retry can be seen succeeding.
//
// A real GitHub outage or rate-limit response is not something CI can arrange,
// and pointing the agent at an unroutable address produces connection errors
// rather than the HTTP failure the ticket describes. The stub answers on
// loopback over plain HTTP - no TLS, so unlike the stub broker it needs no CA in
// the host trust store - and the agent is pointed at it through the
// release-url override file the integration build honors (see
// agent.ResolveLatestReleaseUrl).
//
// Whether a request is failed or served is read from -mode-file on every
// request, so the harness can flip the endpoint mid-retry-sequence without
// restarting it. The default (and the behavior with a missing mode file) is to
// fail, so a harness that forgets to set a mode reproduces the failing endpoint
// the scenario is built around rather than silently passing.
//
// Every request is logged with a millisecond timestamp and the mode it was
// answered in, so the log doubles as the arrival-time record the scenario uses
// to show that retries arrive spread out rather than back to back.
//
// The release payload it serves carries -tag as tag_name and no assets. The
// scenario sets -tag to the running agent's own version, so a successful check
// ends at "No updates available" and the retry is seen succeeding without the
// agent downloading and executing anything - the recovery is what is under test,
// not the installer.
//
// It sits behind the `integration` build tag so it stays out of
// `go test ./...`: as an untested CI fixture its statements would otherwise
// count against the repository coverage threshold. It is still linted, because
// the tag is listed in .golangci.yml. Build it with
// `go build -tags integration ./test/stubrelease`.
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

// modeOk is the -mode-file content that makes the endpoint serve a release
// payload. Any other content (including a missing file) fails the request with
// the status below.
const modeOk = "ok"

// failStatus is what a request is failed with. 503 is what an unavailable
// releases API returns and is the status the ticket's reproduction uses; the
// agent treats any non-200 identically, so this stands in for a rate-limit
// response as well.
const failStatus = http.StatusServiceUnavailable

// readTimeout and writeTimeout bound a single request so a stuck client cannot
// pin the fixture for the rest of the job.
const (
	readTimeout  = 10 * time.Second
	writeTimeout = 10 * time.Second
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8765", "address to listen on")
	modeFile := flag.String(
		"mode-file",
		"",
		fmt.Sprintf("path to a file containing %q to serve a release payload; "+
			"any other content (or a missing file) fails the request with %d",
			modeOk, failStatus),
	)
	tag := flag.String(
		"tag",
		"v0.0.0-it",
		"tag_name to report in the release payload; set to the running agent's "+
			"own version so a successful check ends at \"No updates available\"",
	)
	logPath := flag.String("log", "", "path to write this endpoint's log to (defaults to stderr)")
	flag.Parse()

	// Redirected here rather than by the launching shell for the same reason the
	// stub broker does it: on Windows the fixture has to be launched detached
	// from the step that starts it, and a detached launch has nowhere to attach a
	// redirect. Set up first so a startup failure - a port already in use, say -
	// lands in the file the harness reports.
	if *logPath != "" {
		logFile, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", *logPath, err)
			os.Exit(1)
		}
		defer func() { _ = logFile.Close() }()
		log.SetOutput(logFile)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, *modeFile, *tag)
	})

	server := &http.Server{
		Addr:         *listen,
		Handler:      mux,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	log.Printf(
		"stub release endpoint listening on %s (mode file %q, tag %q)",
		*listen,
		*modeFile,
		*tag,
	)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("failed to serve on %s: %v", *listen, err)
	}
}

// handle answers a single request according to the current mode, logging the
// arrival time either way. The mode is read per request so the harness can
// recover the endpoint in the middle of a retry sequence.
func handle(w http.ResponseWriter, r *http.Request, modeFile string, tag string) {
	ok := currentModeIsOk(modeFile)
	// Millisecond precision, because the point of the log is the spacing between
	// arrivals: a busy-spinning retry loop would show requests in the same
	// millisecond, and a jittered schedule shows unequal gaps.
	arrived := time.Now().Format("2006-01-02T15:04:05.000Z07:00")

	if !ok {
		log.Printf("%s %s %s -> %d (mode=fail)", arrived, r.Method, r.URL.Path, failStatus)
		w.WriteHeader(failStatus)
		return
	}

	release := map[string]any{
		"id":       1,
		"tag_name": tag,
		"assets":   []any{},
	}
	body, err := json.Marshal(release)
	if err != nil {
		// Unreachable for this fixed payload, but failing loudly beats serving a
		// truncated body the agent would report as a parse error.
		log.Printf("%s failed to marshal release payload: %v", arrived, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	log.Printf("%s %s %s -> 200 (mode=ok, tag=%s)", arrived, r.Method, r.URL.Path, tag)
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		log.Printf("%s failed to write release payload: %v", arrived, err)
	}
}

// currentModeIsOk reports whether the mode file currently asks for a served
// release. A missing or unreadable file means fail, so the fixture's default is
// the failing endpoint the scenario needs.
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
