package autotask

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// PicklistCheck is a configured picklist ID that must exist and be active.
type PicklistCheck struct {
	Entity string // e.g. "Tickets"
	Field  string // e.g. "priority"
	Value  int
	// Setting names the configuration key the value came from, for errors.
	Setting string
}

type fieldInfo struct {
	Name           string `json:"name"`
	PicklistValues []struct {
		Value    string `json:"value"`
		Label    string `json:"label"`
		IsActive bool   `json:"isActive"`
	} `json:"picklistValues"`
}

// Validate checks that the configuration matches this Autotask instance: every
// picklist ID exists and is active, both UDFs exist, and the catch-all
// company exists and is active. All problems are reported together.
func (c *Client) Validate(ctx context.Context, checks []PicklistCheck, catchAllCompanyID int64) error {
	var errs []error

	fields := map[string][]fieldInfo{}
	for _, chk := range checks {
		if _, ok := fields[chk.Entity]; ok {
			continue
		}
		var resp struct {
			Fields []fieldInfo `json:"fields"`
		}
		if err := c.do(ctx, http.MethodGet, c.url("/"+chk.Entity+"/entityInformation/fields"), nil, &resp); err != nil {
			return fmt.Errorf("reading %s fields: %w", chk.Entity, err)
		}
		fields[chk.Entity] = resp.Fields
	}

	for _, chk := range checks {
		if err := checkPicklist(fields[chk.Entity], chk); err != nil {
			errs = append(errs, err)
		}
	}

	for entity, name := range map[string]string{
		"Tickets":   c.opts.RecordedAlertsUDF,
		"Companies": c.opts.CompanySelectorsUDF,
	} {
		if err := c.checkUDF(ctx, entity, name); err != nil {
			errs = append(errs, err)
		}
	}

	if err := c.checkCompany(ctx, catchAllCompanyID); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func checkPicklist(fields []fieldInfo, chk PicklistCheck) error {
	for _, f := range fields {
		if !strings.EqualFold(f.Name, chk.Field) {
			continue
		}
		for _, v := range f.PicklistValues {
			if v.Value != strconv.Itoa(chk.Value) {
				continue
			}
			if !v.IsActive {
				return fmt.Errorf("%s: %s %s value %d (%s) is inactive; %s", chk.Setting, chk.Entity, chk.Field, chk.Value, v.Label, activeValues(f))
			}
			return nil
		}
		return fmt.Errorf("%s: %s %s has no value %d; %s", chk.Setting, chk.Entity, chk.Field, chk.Value, activeValues(f))
	}
	return fmt.Errorf("%s: %s has no field %s", chk.Setting, chk.Entity, chk.Field)
}

// activeValues describes the values of a picklist field that may be used.
func activeValues(f fieldInfo) string {
	var values []string
	for _, v := range f.PicklistValues {
		if v.IsActive {
			values = append(values, fmt.Sprintf("%s (%s)", v.Value, v.Label))
		}
	}
	if len(values) == 0 {
		return "it has no active values"
	}
	return "active values are " + strings.Join(values, ", ")
}

func (c *Client) checkUDF(ctx context.Context, entity, name string) error {
	var resp struct {
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, c.url("/"+entity+"/entityInformation/userDefinedFields"), nil, &resp); err != nil {
		return fmt.Errorf("reading %s UDFs: %w", entity, err)
	}

	for _, f := range resp.Fields {
		if strings.EqualFold(f.Name, name) {
			return nil
		}
	}
	return fmt.Errorf("%s UDF %q does not exist", entity, name)
}

func (c *Client) checkCompany(ctx context.Context, id int64) error {
	var resp struct {
		Item *struct {
			IsActive bool `json:"isActive"`
		} `json:"item"`
	}
	if err := c.do(ctx, http.MethodGet, c.url("/Companies/"+strconv.FormatInt(id, 10)), nil, &resp); err != nil {
		return fmt.Errorf("reading catch-all company %d: %w", id, err)
	}

	switch {
	case resp.Item == nil:
		return fmt.Errorf("catch-all company %d does not exist", id)
	case !resp.Item.IsActive:
		return fmt.Errorf("catch-all company %d is inactive", id)
	}
	return nil
}
