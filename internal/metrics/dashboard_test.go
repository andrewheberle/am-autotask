package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const dashboardPath = "../../examples/grafana/am-autotask.json"

var (
	descPattern     = regexp.MustCompile(`^Desc\{fqName: "([^"]+)", .*, constLabels: \{([^}]*)\}, variableLabels: \{([^}]*)\}\}$`)
	metricPattern   = regexp.MustCompile(`\b(am_autotask_\w+)(\{[^}]*\})?`)
	matcherPattern  = regexp.MustCompile(`(\w+)\s*(?:=~|!~|!=|=)`)
	groupingPattern = regexp.MustCompile(`\b(?:by|without)\s*\(([^)]*)\)`)
)

// registeredLabels returns the label names of each am_autotask metric
// family, keyed by the names its series are exposed under.
func registeredLabels(t *testing.T) map[string][]string {
	t.Helper()

	m := New(Options{
		SelectorsAge:   func() time.Duration { return 0 },
		SelectorCounts: func() (int, int) { return 0, 0 },
		Ready:          func() bool { return true },
	})

	ch := make(chan *prometheus.Desc)
	go func() {
		m.registry.Describe(ch)
		close(ch)
	}()

	labels := make(map[string][]string)
	for d := range ch {
		match := descPattern.FindStringSubmatch(d.String())
		if match == nil {
			t.Fatalf("cannot parse %s", d)
		}
		name := match[1]
		if !strings.HasPrefix(name, namespace+"_") {
			continue
		}
		var names []string
		for _, pair := range strings.Split(match[2], ",") {
			if label, _, ok := strings.Cut(pair, "="); ok {
				names = append(names, label)
			}
		}
		for _, label := range strings.Split(match[3], ",") {
			if label != "" {
				names = append(names, strings.TrimSuffix(strings.TrimPrefix(label, "c("), ")"))
			}
		}
		// the dashboard does not need to tell histograms from other types, so
		// every family accepts the histogram series names
		labels[name] = append(labels[name], names...)
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			labels[name+suffix] = append(labels[name+suffix], names...)
		}
		labels[name+"_bucket"] = append(labels[name+"_bucket"], "le")
	}

	return labels
}

// dashboardExprs returns every PromQL expression in the example dashboard,
// including those in its template variables.
func dashboardExprs(t *testing.T) []string {
	t.Helper()

	b, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatalf("reading dashboard: %v", err)
	}
	var dashboard any
	if err := json.Unmarshal(b, &dashboard); err != nil {
		t.Fatalf("parsing dashboard: %v", err)
	}

	var exprs []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				if s, ok := child.(string); ok && (k == "expr" || k == "definition") {
					exprs = append(exprs, s)
				}
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(dashboard)

	return exprs
}

// target labels Prometheus adds when scraping
var targetLabels = []string{"job", "instance"}

// checkExprs returns a problem for each metric, or label of one, used in
// exprs that is not in registered.
func checkExprs(registered map[string][]string, exprs []string) []string {
	var problems []string
	for _, expr := range exprs {
		var metrics []string
		for _, match := range metricPattern.FindAllStringSubmatch(expr, -1) {
			name, selector := match[1], match[2]
			known, ok := registered[name]
			if !ok {
				problems = append(problems, fmt.Sprintf("%q uses unregistered metric %s", expr, name))
				continue
			}
			metrics = append(metrics, name)
			for _, m := range matcherPattern.FindAllStringSubmatch(selector, -1) {
				if !slices.Contains(known, m[1]) && !slices.Contains(targetLabels, m[1]) {
					problems = append(problems, fmt.Sprintf("%q selects on label %s, which %s does not have", expr, m[1], name))
				}
			}
		}

		// every metric in an expression must carry the labels it groups by
		for _, grouping := range groupingPattern.FindAllStringSubmatch(expr, -1) {
			for label := range strings.SplitSeq(grouping[1], ",") {
				label = strings.TrimSpace(label)
				if label == "" || slices.Contains(targetLabels, label) {
					continue
				}
				for _, name := range metrics {
					if !slices.Contains(registered[name], label) {
						problems = append(problems, fmt.Sprintf("%q groups by label %s, which %s does not have", expr, label, name))
					}
				}
			}
		}
	}

	return problems
}

func TestCheckExprs(t *testing.T) {
	registered := registeredLabels(t)

	tests := []struct {
		name     string
		expr     string
		problems int
	}{
		{"registered metric and labels", `sum by (result) (rate(am_autotask_notifications_total{job="$job", result=~".*_failure"}[5m]))`, 0},
		{"histogram bucket", `histogram_quantile(0.95, sum by (operation, le) (rate(am_autotask_autotask_request_duration_seconds_bucket[5m])))`, 0},
		{"constant label", `sum by (state) (am_autotask_company_selectors_companies)`, 0},
		{"other exporter's metric", `sum by (integration) (rate(alertmanager_notifications_failed_total[5m]))`, 0},
		{"unregistered metric", `rate(am_autotask_tickets_closed_total[5m])`, 1},
		{"unknown selector label", `am_autotask_notifications_total{outcome="handled"}`, 1},
		{"unknown grouping label", `sum by (reason) (rate(am_autotask_notifications_total[5m]))`, 1},
		{"grouping label missing from one metric", `sum by (operation) (rate(am_autotask_autotask_errors_total[5m])) / sum by (operation) (rate(am_autotask_tickets_created_total[5m]))`, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkExprs(registered, []string{tt.expr}); len(got) != tt.problems {
				t.Errorf("got %d problems, want %d: %v", len(got), tt.problems, got)
			}
		})
	}
}

func TestDashboardMatchesMetrics(t *testing.T) {
	exprs := dashboardExprs(t)
	if !slices.ContainsFunc(exprs, metricPattern.MatchString) {
		t.Fatal("the dashboard uses no am_autotask metrics")
	}
	for _, problem := range checkExprs(registeredLabels(t), exprs) {
		t.Error(problem)
	}
}
