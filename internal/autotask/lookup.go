package autotask

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/andrewheberle/am-autotask/internal/lookup"
)

var _ lookup.Autotask = (*Client)(nil)

// Picklists implements [lookup.Autotask].
func (c *Client) Picklists(ctx context.Context, entity string) (map[string][]lookup.PicklistValue, error) {
	var resp struct {
		Fields []struct {
			Name           string `json:"name"`
			IsPickList     bool   `json:"isPickList"`
			PicklistValues []struct {
				Value       string `json:"value"`
				Label       string `json:"label"`
				IsActive    bool   `json:"isActive"`
				ParentValue string `json:"parentValue"`
			} `json:"picklistValues"`
		} `json:"fields"`
	}
	if err := c.do(ctx, http.MethodGet, c.url("/"+entity+"/entityInformation/fields"), nil, &resp); err != nil {
		return nil, fmt.Errorf("reading %s fields: %w", entity, err)
	}

	picklists := map[string][]lookup.PicklistValue{}
	for _, f := range resp.Fields {
		if !f.IsPickList {
			continue
		}
		values := make([]lookup.PicklistValue, 0, len(f.PicklistValues))
		for _, v := range f.PicklistValues {
			id, err := strconv.Atoi(v.Value)
			if err != nil {
				return nil, fmt.Errorf("%s %s value %q is not numeric", entity, f.Name, v.Value)
			}
			// parentValue is empty for values without a parent
			parent, _ := strconv.Atoi(v.ParentValue)
			values = append(values, lookup.PicklistValue{ID: id, Label: v.Label, Active: v.IsActive, ParentID: parent})
		}
		picklists[f.Name] = values
	}
	return picklists, nil
}

// SearchCompanies implements [lookup.Autotask].
func (c *Client) SearchCompanies(ctx context.Context, name string, includeInactive bool) ([]lookup.Company, error) {
	filters := []filter{{Op: "contains", Field: "companyName", Value: name}}
	if !includeInactive {
		filters = append(filters, filter{Op: "eq", Field: "isActive", Value: true})
	}
	items, err := query[struct {
		ID          int64  `json:"id"`
		CompanyName string `json:"companyName"`
		IsActive    bool   `json:"isActive"`
	}](ctx, c, "Companies", filters, "id", "companyName", "isActive")
	if err != nil {
		return nil, err
	}

	companies := make([]lookup.Company, 0, len(items))
	for _, item := range items {
		companies = append(companies, lookup.Company{ID: item.ID, Name: item.CompanyName, Active: item.IsActive})
	}
	return companies, nil
}

// workTypeUse is the BillingCodes useType of work types, which Autotask calls
// general allocation codes.
const workTypeUse = 1

// WorkTypes implements [lookup.Autotask].
func (c *Client) WorkTypes(ctx context.Context, includeInactive bool) ([]lookup.WorkType, error) {
	filters := []filter{{Op: "eq", Field: "useType", Value: workTypeUse}}
	if !includeInactive {
		filters = append(filters, filter{Op: "eq", Field: "isActive", Value: true})
	}
	items, err := query[struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		IsActive bool   `json:"isActive"`
	}](ctx, c, "BillingCodes", filters, "id", "name", "isActive")
	if err != nil {
		return nil, err
	}

	workTypes := make([]lookup.WorkType, 0, len(items))
	for _, item := range items {
		workTypes = append(workTypes, lookup.WorkType{ID: item.ID, Name: item.Name, Active: item.IsActive})
	}
	return workTypes, nil
}
