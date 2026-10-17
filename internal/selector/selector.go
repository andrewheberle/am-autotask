// Package selector matches alert groups to companies using the company
// selectors held on each company in Autotask.
package selector

import (
	"fmt"
	"slices"
	"strings"

	"github.com/prometheus/alertmanager/matcher/parse"
	"github.com/prometheus/alertmanager/pkg/labels"
)

// Company is a company and the raw value of its company selector UDF: one
// Alertmanager label selector per line.
type Company struct {
	ID        int64
	Selectors string
}

// Set is an immutable, parsed collection of company selectors.
type Set struct {
	companies []parsedCompany
}

type parsedCompany struct {
	id        int64
	selectors []labels.Matchers
}

// Build parses the selectors of each company. A company with any invalid
// selector is skipped entirely and reported in the returned errors; the
// remaining companies are still usable.
func Build(companies []Company) (*Set, []error) {
	set := &Set{}
	var errs []error

	for _, c := range companies {
		parsed, err := parseSelectors(c.Selectors)
		if err != nil {
			errs = append(errs, fmt.Errorf("company %d: %w", c.ID, err))
			continue
		}
		if len(parsed) == 0 {
			continue
		}
		set.companies = append(set.companies, parsedCompany{id: c.ID, selectors: parsed})
	}

	return set, errs
}

func parseSelectors(value string) ([]labels.Matchers, error) {
	var selectors []labels.Matchers

	for i, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matchers, err := parse.Matchers(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		// an empty selector would match every alert group
		if len(matchers) == 0 {
			return nil, fmt.Errorf("line %d: selector has no matchers", i+1)
		}
		selectors = append(selectors, matchers)
	}

	return selectors, nil
}

// Match returns the IDs of every company with at least one selector whose
// matchers all match the given group labels, sorted and without duplicates.
// It returns nil when no company matches.
func (s *Set) Match(groupLabels map[string]string) []int64 {
	var ids []int64

	for _, c := range s.companies {
		for _, sel := range c.selectors {
			if matches(sel, groupLabels) {
				ids = append(ids, c.id)
				break
			}
		}
	}

	slices.Sort(ids)
	return slices.Compact(ids)
}

// Len returns the number of companies with at least one valid selector.
func (s *Set) Len() int {
	return len(s.companies)
}

func matches(sel labels.Matchers, groupLabels map[string]string) bool {
	for _, m := range sel {
		if !m.Matches(groupLabels[m.Name]) {
			return false
		}
	}
	return true
}
