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
// picklist ID exists and is active, every UDF exists, the catch-all company
// and the problem company, if set, exist and are active, and the work type,
// unless zero, exists and is active. All problems are reported together.
func (c *Client) Validate(ctx context.Context, checks []PicklistCheck, catchAllCompanyID int64, problemCompanyID *int64, workTypeID int) error {
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

	udfs := []struct{ entity, name string }{
		{"Tickets", c.opts.RecordedAlertsUDF},
		{"Companies", c.opts.CompanySelectorsUDF},
	}
	if c.opts.AppliedPriorityUDF != "" {
		udfs = append(udfs, struct{ entity, name string }{"Tickets", c.opts.AppliedPriorityUDF})
	}
	for _, udf := range udfs {
		if err := c.checkUDF(ctx, udf.entity, udf.name); err != nil {
			errs = append(errs, err)
		}
	}

	if err := c.checkCompany(ctx, "catch-all company", catchAllCompanyID); err != nil {
		errs = append(errs, err)
	}
	if problemCompanyID != nil {
		if err := c.checkCompany(ctx, "problem company", *problemCompanyID); err != nil {
			errs = append(errs, err)
		}
	}

	if workTypeID != 0 {
		if err := c.checkWorkType(ctx, workTypeID); err != nil {
			errs = append(errs, err)
		}
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

// checkCompany checks a configured company, described by what, exists and is
// active.
func (c *Client) checkCompany(ctx context.Context, what string, id int64) error {
	var resp struct {
		Item *struct {
			IsActive bool `json:"isActive"`
		} `json:"item"`
	}
	if err := c.do(ctx, http.MethodGet, c.url("/Companies/"+strconv.FormatInt(id, 10)), nil, &resp); err != nil {
		return fmt.Errorf("reading %s %d: %w", what, id, err)
	}

	switch {
	case resp.Item == nil:
		return fmt.Errorf("%s %d does not exist", what, id)
	case !resp.Item.IsActive:
		return fmt.Errorf("%s %d is inactive", what, id)
	}
	return nil
}

func (c *Client) checkWorkType(ctx context.Context, id int) error {
	workTypes, err := c.WorkTypes(ctx, true)
	if err != nil {
		return fmt.Errorf("reading work types: %w", err)
	}

	var active []string
	for _, wt := range workTypes {
		if wt.Active {
			active = append(active, fmt.Sprintf("%d (%s)", wt.ID, wt.Name))
		}
	}
	values := "there are no active work types"
	if len(active) > 0 {
		values = "active work types are " + strings.Join(active, ", ")
	}

	for _, wt := range workTypes {
		if wt.ID != id {
			continue
		}
		if !wt.Active {
			return fmt.Errorf("work type %d (%s) is inactive; %s", id, wt.Name, values)
		}
		return nil
	}
	return fmt.Errorf("work type %d does not exist; %s", id, values)
}
