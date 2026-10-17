package server_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/server"
)

const token = "hook-token"

type fakeHandler struct {
	mu       sync.Mutex
	received []receiver.Notification
	err      error
}

func (f *fakeHandler) Handle(_ context.Context, n receiver.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.received = append(f.received, n)
	return f.err
}

const payload = `{
  "version": "4",
  "groupKey": "{}:{alertname=\"HostDown\"}",
  "truncatedAlerts": 0,
  "status": "firing",
  "receiver": "autotask",
  "groupLabels": {"alertname": "HostDown"},
  "commonLabels": {"alertname": "HostDown", "severity": "critical"},
  "commonAnnotations": {"summary": "Host is down"},
  "externalURL": "http://alertmanager:9093",
  "alerts": [{
    "status": "firing",
    "labels": {"alertname": "HostDown", "instance": "web1"},
    "annotations": {"summary": "web1 is down"},
    "startsAt": "2026-10-17T04:00:00Z",
    "endsAt": "0001-01-01T00:00:00Z",
    "generatorURL": "http://prometheus/graph",
    "fingerprint": "aaaa"
  }]
}`

type testServer struct {
	*httptest.Server
	handler *fakeHandler
	ready   bool
	logs    *bytes.Buffer
}

func newServer(t *testing.T) *testServer {
	t.Helper()
	ts := &testServer{handler: &fakeHandler{}, ready: true, logs: &bytes.Buffer{}}
	h := server.New(ts.handler, server.Options{
		Token:  token,
		Ready:  func() bool { return ts.ready },
		Logger: slog.New(slog.NewTextHandler(ts.logs, nil)),
	})
	ts.Server = httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) post(t *testing.T, auth, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/webhook", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp
}

func TestValidNotificationIsHandled(t *testing.T) {
	ts := newServer(t)

	resp := ts.post(t, "Bearer "+token, payload)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(ts.handler.received) != 1 {
		t.Fatalf("expected one notification, got %d", len(ts.handler.received))
	}
	n := ts.handler.received[0]
	if n.GroupKey != `{}:{alertname="HostDown"}` || len(n.Alerts) != 1 || n.Alerts[0].Fingerprint != "aaaa" || n.Alerts[0].StartsAt.IsZero() {
		t.Errorf("decoded notification = %+v", n)
	}
}

func TestMissingOrWrongTokenIsUnauthorized(t *testing.T) {
	for _, auth := range []string{"", "Bearer wrong", token, "Basic " + token} {
		ts := newServer(t)

		resp := ts.post(t, auth, payload)

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: status = %d, want 401", auth, resp.StatusCode)
		}
		if len(ts.handler.received) != 0 {
			t.Errorf("Authorization %q: notification should not be handled", auth)
		}
	}
}

func TestUnsupportedVersionIsRejectedAndLoggedOnce(t *testing.T) {
	ts := newServer(t)
	v3 := strings.Replace(payload, `"version": "4"`, `"version": "3"`, 1)

	for range 3 {
		if resp := ts.post(t, "Bearer "+token, v3); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", resp.StatusCode)
		}
	}

	if len(ts.handler.received) != 0 {
		t.Error("notification should not be handled")
	}
	if n := strings.Count(ts.logs.String(), "unsupported"); n != 1 {
		t.Errorf("expected the version to be logged once, logged %d times:\n%s", n, ts.logs)
	}
}

func TestMalformedPayloadIsBadRequest(t *testing.T) {
	ts := newServer(t)

	if resp := ts.post(t, "Bearer "+token, `{"version": `); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestTemporaryFailureAsksAlertmanagerToRetry(t *testing.T) {
	ts := newServer(t)
	ts.handler.err = errors.New("autotask returned HTTP 503")

	if resp := ts.post(t, "Bearer "+token, payload); resp.StatusCode < 500 {
		t.Errorf("status = %d, want 5xx", resp.StatusCode)
	}
}

func TestPermanentFailureIsNotRetriedAndLogsPayload(t *testing.T) {
	ts := newServer(t)
	ts.handler.err = fmt.Errorf("creating ticket: %w", receiver.ErrPermanent)

	if resp := ts.post(t, "Bearer "+token, payload); resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 so Alertmanager does not retry", resp.StatusCode)
	}
	if !strings.Contains(ts.logs.String(), "HostDown") {
		t.Errorf("permanent failure should be logged with the payload:\n%s", ts.logs)
	}
}

func TestWebhookOnlyAcceptsPost(t *testing.T) {
	ts := newServer(t)

	resp, err := ts.Client().Get(ts.URL + "/webhook")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestHealthAndReadiness(t *testing.T) {
	ts := newServer(t)
	get := func(path string) int {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	ts.ready = false
	if got := get("/healthz"); got != http.StatusOK {
		t.Errorf("/healthz = %d, want 200 even when not ready", got)
	}
	if got := get("/readyz"); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want 503 before ready", got)
	}

	ts.ready = true
	if got := get("/readyz"); got != http.StatusOK {
		t.Errorf("/readyz = %d, want 200 once ready", got)
	}
}
