package cli

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/config"
)

func TestNewElector(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")

	tests := []struct {
		name    string
		cfg     config.LeaderElection
		wantErr string
	}{
		{name: "disabled", cfg: config.LeaderElection{Lease: "am-autotask"}},
		{
			name:    "fails outside Kubernetes",
			cfg:     config.LeaderElection{Enabled: true, Namespace: "monitoring", Lease: "am-autotask", Identity: "pod", Address: "10.0.0.1:8080"},
			wantErr: "not running in Kubernetes",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			el, err := newElector(tt.cfg, slog.New(slog.DiscardHandler), func(bool) {})
			if tt.wantErr == "" {
				if err != nil || el != nil {
					t.Errorf("newElector = %v, %v; want no elector", el, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}
