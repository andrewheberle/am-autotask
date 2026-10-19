package leader_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/leader"
)

const leasePath = "/apis/coordination.k8s.io/v1/namespaces/monitoring/leases"

// fakeAPI serves one Lease the way the Kubernetes API server does.
type fakeAPI struct {
	mu      sync.Mutex
	stored  map[string]any
	version int
	tokens  []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))

	var body map[string]any
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == leasePath+"/am-autotask":
		if f.stored == nil {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","message":"leases.coordination.k8s.io \"am-autotask\" not found"}`))
			return
		}
	case r.Method == http.MethodPost && r.URL.Path == leasePath:
		if f.stored != nil {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.store(body)
	case r.Method == http.MethodPut && r.URL.Path == leasePath+"/am-autotask":
		meta := body["metadata"].(map[string]any)
		if meta["resourceVersion"] != f.stored["metadata"].(map[string]any)["resourceVersion"] {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.store(body)
	default:
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","message":"leases.coordination.k8s.io is forbidden"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(f.stored)
}

func (f *fakeAPI) store(body map[string]any) {
	f.version++
	body["metadata"].(map[string]any)["resourceVersion"] = string(rune('0' + f.version))
	f.stored = body
}

func TestKubeClient(t *testing.T) {
	api := &fakeAPI{}
	srv := httptest.NewServer(api)
	defer srv.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("token-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := leader.NewKubeClient(srv.URL, "monitoring", "am-autotask", tokenPath, srv.Client())
	ctx := t.Context()

	if _, err := c.Get(ctx); !errors.Is(err, leader.ErrNotFound) {
		t.Fatalf("Get of missing lease: err = %v, want ErrNotFound", err)
	}

	now := time.Date(2026, 10, 19, 1, 2, 3, 456789000, time.UTC)
	created, err := c.Create(ctx, leader.Lease{Holder: "pod-a", Address: "10.0.0.1:8080", Duration: 15 * time.Second, AcquireTime: now, RenewTime: now})
	if err != nil {
		t.Fatal(err)
	}
	if created.Holder != "pod-a" || created.Address != "10.0.0.1:8080" || created.Duration != 15*time.Second ||
		!created.RenewTime.Equal(now) || created.ResourceVersion == "" {
		t.Errorf("created = %+v", created)
	}
	spec := api.stored["spec"].(map[string]any)
	if spec["renewTime"] != "2026-10-19T01:02:03.456789Z" || spec["leaseDurationSeconds"] != 15.0 {
		t.Errorf("stored spec = %v, want microsecond times and whole seconds", spec)
	}

	if _, err := c.Create(ctx, created); !errors.Is(err, leader.ErrConflict) {
		t.Errorf("second Create: err = %v, want ErrConflict", err)
	}

	// the token is re-read, as Kubernetes rotates it
	if err := os.WriteFile(tokenPath, []byte("token-2"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != created {
		t.Errorf("Get = %+v, want %+v", got, created)
	}

	renewed := got
	renewed.RenewTime = now.Add(2 * time.Second)
	if _, err := c.Update(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Update(ctx, renewed); !errors.Is(err, leader.ErrConflict) {
		t.Errorf("Update with a stale version: err = %v, want ErrConflict", err)
	}

	if api.tokens[0] != "Bearer token-1" || api.tokens[len(api.tokens)-1] != "Bearer token-2" {
		t.Errorf("tokens sent = %v", api.tokens)
	}

	other := leader.NewKubeClient(srv.URL, "kube-system", "am-autotask", tokenPath, srv.Client())
	if _, err := other.Get(ctx); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("err = %v, want the API server's message", err)
	}
}

func TestInClusterRequiresKubernetes(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1", "KUBERNETES_SERVICE_PORT": "443"}
	getenv := func(k string) string { return env[k] }

	tests := []struct {
		name    string
		env     map[string]string
		files   []string
		wantErr string
	}{
		{name: "outside a cluster", env: map[string]string{}, wantErr: "not running in Kubernetes"},
		{name: "no service account token", env: env, wantErr: "token"},
		{name: "no CA", env: env, files: []string{"token"}, wantErr: "CA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(dir, strings.ReplaceAll(tt.name, " ", "-"))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, f := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := leader.InCluster{Dir: dir, Getenv: func(k string) string { return tt.env[k] }}.Client("monitoring", "am-autotask")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want one mentioning %q", err, tt.wantErr)
			}
		})
	}

	if err := os.WriteFile(filepath.Join(dir, "namespace"), []byte("monitoring\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ns, err := leader.InCluster{Dir: dir, Getenv: getenv}.Namespace()
	if err != nil || ns != "monitoring" {
		t.Errorf("Namespace = %q, %v, want monitoring", ns, err)
	}
}
