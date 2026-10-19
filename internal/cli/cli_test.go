package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bep/simplecobra"
)

func TestExecute(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantIs  error
		wantMsg string
	}{
		{name: "no subcommand", args: nil, wantIs: errNoCommand},
		{name: "arguments from before subcommands", args: []string{"--config=/etc/am-autotask/config.yaml"}, wantMsg: "unknown flag: --config"},
		{name: "unknown subcommand", args: []string{"server"}, wantMsg: "unknown command"},
		{name: "serve loads its configuration", args: []string{"serve"}, wantMsg: "invalid configuration"},
		{name: "serve takes no arguments", args: []string{"serve", "extra"}, wantMsg: "unknown command"},
		{name: "version", args: []string{"--version"}},
		{name: "lookup without a subcommand", args: []string{"lookup"}, wantIs: errNoCommand},
		{name: "lookup companies needs a name", args: []string{"lookup", "companies"}, wantMsg: "accepts 1 arg"},
		{name: "lookup needs only Autotask settings", args: []string{"lookup", "picklists"}, wantMsg: "autotask.username"},
		{name: "lookup worktypes takes no arguments", args: []string{"lookup", "worktypes", "extra"}, wantMsg: "unknown command"},
		{name: "lookup worktypes needs only Autotask settings", args: []string{"lookup", "worktypes"}, wantMsg: "autotask.username"},
		{name: "lookup does not take receiver flags", args: []string{"lookup", "picklists", "--ticket.queue=8"}, wantMsg: "unknown flag: --ticket.queue"},
		{name: "templates takes no arguments", args: []string{"templates", "extra"}, wantMsg: "unknown command"},
		{name: "templates reports a missing file", args: []string{"templates", "--templates", "/nonexistent/templates.tmpl"}, wantMsg: "reading templates"},
		{name: "templates reports a missing notification", args: []string{"templates", "--notification", "/nonexistent/webhook.json"}, wantMsg: "reading notification"},
		{name: "templates does not take receiver flags", args: []string{"templates", "--ticket.queue=8"}, wantMsg: "unknown flag: --ticket.queue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AM_AUTOTASK_CONFIG", "")

			err := execute(context.Background(), tt.args, "v1.2.3")

			switch {
			case tt.wantIs != nil:
				if !errors.Is(err, tt.wantIs) {
					t.Errorf("err = %v, want %v", err, tt.wantIs)
				}
			case tt.wantMsg != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("err = %v, want one containing %q", err, tt.wantMsg)
				}
				if err != nil && tt.args[0] == "lookup" && strings.Contains(err.Error(), "company.catchall") {
					t.Errorf("lookup should not require receiver settings: %v", err)
				}
			case err != nil:
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestReport(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		want     string
	}{
		{"success", nil, 0, ""},
		{"error", errors.New("invalid configuration:\n  - a is required\n  - b is required"), 1, "Error: invalid configuration:\n  - a is required\n  - b is required\n"},
		{"command line", &simplecobra.CommandError{Err: errors.New("unknown flag: --x")}, 1, "Error: unknown flag: --x\n"},
		{"already logged", fmt.Errorf("starting: %w", &reportedError{errors.New("boom")}), 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder

			if code := report(&b, tt.err); code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if got := b.String(); got != tt.want {
				t.Errorf("printed %q, want %q", got, tt.want)
			}
		})
	}
}
