package selector_test

import (
	"slices"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/selector"
)

// companies from the design discussion: A owns two selectors, B one.
var exampleCompanies = []selector.Company{
	{ID: 100, Selectors: "{tenant_id=\"foo\", site=\"bar\"}\n{tenant_id=\"foo\", cluster=\"xyz\"}"},
	{ID: 200, Selectors: `{tenant_id="foo", site="baz"}`},
}

func TestMatch(t *testing.T) {
	set, errs := selector.Build(exampleCompanies)
	if len(errs) != 0 {
		t.Fatalf("unexpected build errors: %v", errs)
	}

	tests := []struct {
		name        string
		groupLabels map[string]string
		want        []int64
	}{
		{"every matcher of a selector matches", map[string]string{"tenant_id": "foo", "site": "bar"}, []int64{100}},
		{"any selector of a company matches", map[string]string{"tenant_id": "foo", "cluster": "xyz"}, []int64{100}},
		{"other company", map[string]string{"tenant_id": "foo", "site": "baz"}, []int64{200}},
		{"partial match is no match", map[string]string{"tenant_id": "foo"}, nil},
		{"no labels", map[string]string{}, nil},
		{"extra labels do not prevent a match", map[string]string{"tenant_id": "foo", "site": "baz", "alertname": "HostDown"}, []int64{200}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := set.Match(tt.groupLabels); !slices.Equal(got, tt.want) {
				t.Errorf("Match(%v) = %v, want %v", tt.groupLabels, got, tt.want)
			}
		})
	}
}

func TestMatchReportsEveryMatchingCompany(t *testing.T) {
	set, _ := selector.Build([]selector.Company{
		{ID: 300, Selectors: `{tenant_id="foo"}`},
		{ID: 100, Selectors: `{site="bar"}`},
		{ID: 200, Selectors: "{tenant_id=\"foo\"}\n{site=~\"b.*\"}"},
	})

	got := set.Match(map[string]string{"tenant_id": "foo", "site": "bar"})

	if want := []int64{100, 200, 300}; !slices.Equal(got, want) {
		t.Errorf("Match = %v, want %v (each company once, sorted)", got, want)
	}
}

func TestMatchOperators(t *testing.T) {
	set, errs := selector.Build([]selector.Company{
		{ID: 1, Selectors: `{env!="dev", region=~"au-.*", team!~"test.*"}`},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected build errors: %v", errs)
	}

	if got := set.Match(map[string]string{"env": "prod", "region": "au-perth", "team": "ops"}); !slices.Equal(got, []int64{1}) {
		t.Errorf("expected match, got %v", got)
	}
	if got := set.Match(map[string]string{"env": "dev", "region": "au-perth", "team": "ops"}); got != nil {
		t.Errorf("expected no match for env=dev, got %v", got)
	}
}

func TestBuildSkipsInvalidCompanies(t *testing.T) {
	set, errs := selector.Build([]selector.Company{
		{ID: 1, Selectors: `{tenant_id="foo"`},
		{ID: 2, Selectors: `{tenant_id="foo"}`},
	})

	if len(errs) != 1 {
		t.Fatalf("expected one error for company 1, got %v", errs)
	}
	if got := set.Match(map[string]string{"tenant_id": "foo"}); !slices.Equal(got, []int64{2}) {
		t.Errorf("valid company should still match, got %v", got)
	}
}

func TestBuildIgnoresBlankLines(t *testing.T) {
	set, errs := selector.Build([]selector.Company{
		{ID: 1, Selectors: "\r\n  {tenant_id=\"foo\"}  \r\n\r\n"},
	})
	if len(errs) != 0 {
		t.Fatalf("unexpected build errors: %v", errs)
	}

	if got := set.Match(map[string]string{"tenant_id": "foo"}); !slices.Equal(got, []int64{1}) {
		t.Errorf("Match = %v, want [1]", got)
	}
}
