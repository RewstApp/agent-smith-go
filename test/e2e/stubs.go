//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// configEndpoint stands in for the Rewst config endpoint the agent fetches its
// configuration from in config mode. It serves a configuration whose hub host
// is the loopback address with nothing listening on the MQTT port: the agent
// installs, starts, and spends its life in the connect/backoff loop, which is
// all these scenarios need from it - the updater and the startup sweeps run
// regardless of the connection. A request without the shared secret is refused,
// the way the real endpoint refuses it.
type configEndpoint struct {
	Secret   string
	DeviceID string
	srv      *httptest.Server
	mu       sync.Mutex
	requests int
}

type servedConfiguration struct {
	DeviceID        string `json:"device_id"`
	RewstOrgID      string `json:"rewst_org_id"`
	RewstEngineHost string `json:"rewst_engine_host"`
	SharedAccessKey string `json:"shared_access_key"`
	AzureIotHubHost string `json:"azure_iot_hub_host"`
}

func newConfigEndpoint(t *testing.T, orgID string) *configEndpoint {
	t.Helper()
	c := &configEndpoint{
		Secret:   "e2e-config-secret",
		DeviceID: fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
	}
	key := base64.StdEncoding.EncodeToString([]byte("e2e shared access key 0123456789"))
	mux := http.NewServeMux()
	mux.HandleFunc("/configure", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		c.requests++
		c.mu.Unlock()
		if r.Header.Get("x-rewst-secret") != c.Secret {
			http.Error(w, "missing or wrong x-rewst-secret", http.StatusUnauthorized)
			return
		}
		body := map[string]any{"configuration": servedConfiguration{
			DeviceID:        c.DeviceID,
			RewstOrgID:      orgID,
			RewstEngineHost: "engine.e2e.invalid",
			SharedAccessKey: key,
			AzureIotHubHost: "127.0.0.1",
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	c.srv = httptest.NewServer(mux)
	t.Cleanup(c.srv.Close)
	return c
}

func (c *configEndpoint) URL() string { return c.srv.URL + "/configure" }

func (c *configEndpoint) Requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// releaseStub stands in for the GitHub releases API the auto-updater polls. It
// starts failing (503) and flips to serving a release on demand, per request,
// so an endpoint can recover in the middle of a retry sequence. When assetName
// is set the release carries one asset, downloadable from the stub itself, and
// assetDigest controls what the payload claims about it: a well-formed but
// wrong value is a tampered asset, an empty value is a release GitHub never
// computed a digest for. Every request is recorded with its arrival time.
type releaseStub struct {
	srv         *httptest.Server
	tag         string
	assetName   string
	assetDigest string
	mu          sync.Mutex
	ok          bool
	requests    []releaseRequest
}

type releaseRequest struct {
	At   time.Time
	Path string
	OK   bool
}

const stubAssetContent = "stub installer content\n"

func newReleaseStub(t *testing.T, tag, assetName, assetDigest string) *releaseStub {
	t.Helper()
	s := &releaseStub{tag: tag, assetName: assetName, assetDigest: assetDigest}
	mux := http.NewServeMux()
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		s.record(r.URL.Path, true)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(stubAssetContent))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.ok
		s.mu.Unlock()
		s.record(r.URL.Path, ok)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		assets := []map[string]any{}
		if s.assetName != "" {
			asset := map[string]any{"id": 1, "name": s.assetName, "url": s.srv.URL + "/download"}
			if s.assetDigest != "" {
				asset["digest"] = s.assetDigest
			}
			assets = append(assets, asset)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "tag_name": s.tag, "assets": assets})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *releaseStub) record(path string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, releaseRequest{At: time.Now(), Path: path, OK: ok})
}

// URL is the endpoint to point the agent at.
func (s *releaseStub) URL() string { return s.srv.URL + "/releases/latest" }

// SetOK flips the stub between serving the release (true) and failing (false).
func (s *releaseStub) SetOK(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ok = ok
}

// Requests returns a copy of the request record.
func (s *releaseStub) Requests() []releaseRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]releaseRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// Downloads is how many times the asset was fetched.
func (s *releaseStub) Downloads() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Path == "/download" {
			n++
		}
	}
	return n
}
