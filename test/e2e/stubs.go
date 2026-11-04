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
	// OmitDeviceID serves a 200 whose configuration has no device_id - what
	// the Rewst config endpoint answers when its integration token has expired
	// or the IoT Hub daily quota is exhausted (sc-126322).
	OmitDeviceID bool
	srv          *httptest.Server
	mu           sync.Mutex
	requests     int
}

type servedConfiguration struct {
	DeviceID        string `json:"device_id,omitempty"`
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
		omit := c.OmitDeviceID
		c.mu.Unlock()
		if r.Header.Get("x-rewst-secret") != c.Secret {
			http.Error(w, "missing or wrong x-rewst-secret", http.StatusUnauthorized)
			return
		}
		deviceID := c.DeviceID
		if omit {
			deviceID = ""
		}
		body := map[string]any{"configuration": servedConfiguration{
			DeviceID:        deviceID,
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
	// prereleases are served by the releases list only (sc-129692), the way
	// GitHub's releases/latest never answers with a pre-release: the beta
	// channel sees them, the stable channel does not.
	prereleases []string
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
	release := func(id int, tag string, prerelease bool) map[string]any {
		assets := []map[string]any{}
		if s.assetName != "" {
			asset := map[string]any{"id": id, "name": s.assetName, "url": s.srv.URL + "/download"}
			if s.assetDigest != "" {
				asset["digest"] = s.assetDigest
			}
			assets = append(assets, asset)
		}
		return map[string]any{
			"id": id, "tag_name": tag, "assets": assets, "prerelease": prerelease, "draft": false,
		}
	}
	serving := func(w http.ResponseWriter, r *http.Request) bool {
		s.mu.Lock()
		ok := s.ok
		s.mu.Unlock()
		s.record(r.URL.Path, ok)
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		return ok
	}
	// The list the beta channel reads (agent.ReleasesListUrl): the stable
	// release and every pre-release, newest first like GitHub's.
	mux.HandleFunc("/releases", func(w http.ResponseWriter, r *http.Request) {
		if !serving(w, r) {
			return
		}
		s.mu.Lock()
		pre := append([]string(nil), s.prereleases...)
		s.mu.Unlock()
		list := []map[string]any{}
		for i, tag := range pre {
			list = append(list, release(100+i, tag, true))
		}
		list = append(list, release(1, s.tag, false))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !serving(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(release(1, s.tag, false))
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

// AddPrerelease publishes tag as a pre-release: in the releases list, never
// at releases/latest.
func (s *releaseStub) AddPrerelease(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prereleases = append(s.prereleases, tag)
}

// ListRequests is how many times the releases list was read.
func (s *releaseStub) ListRequests() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Path == "/releases" {
			n++
		}
	}
	return n
}

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

// SetOmitDeviceID switches the served configuration between complete and
// lacking its device_id.
func (c *configEndpoint) SetOmitDeviceID(omit bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.OmitDeviceID = omit
}
