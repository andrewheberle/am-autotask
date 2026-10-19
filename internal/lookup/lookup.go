// Package lookup prints the Autotask IDs that configuration settings take,
// so they can be found without the Autotask UI.
package lookup

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
)

// PicklistValue is one choice of an Autotask picklist field.
type PicklistValue struct {
	ID     int
	Label  string
	Active bool
	// ParentID is the value of the parent field this value belongs to, such
	// as the issue type of a sub-issue type, or zero.
	ParentID int
}

// Company is an Autotask company found by name.
type Company struct {
	ID     int64
	Name   string
	Active bool
}

// WorkType is an Autotask work type, the billing code a ticket's work is
// billed under.
type WorkType struct {
	ID     int
	Name   string
	Active bool
}

// Autotask is the part of the Autotask API lookups use.
type Autotask interface {
	// Picklists returns the values of every picklist field of entity, keyed
	// by field name.
	Picklists(ctx context.Context, entity string) (map[string][]PicklistValue, error)
	// SearchCompanies returns the companies whose name contains name,
	// ignoring case, including inactive companies if includeInactive is set.
	SearchCompanies(ctx context.Context, name string, includeInactive bool) ([]Company, error)
	// WorkTypes returns the work types, including inactive ones if
	// includeInactive is set.
	WorkTypes(ctx context.Context, includeInactive bool) ([]WorkType, error)
}

// picklist ties configuration settings to the Autotask picklist field they
// take an ID from.
type picklist struct {
	settings string
	entity   string
	field    string
	// parent, if set, names the column for the picklist that values are
	// grouped under, which is the one before this in picklists.
	parent string
}

var picklists = []picklist{
	{settings: "ticket.priorities, ticket.defaultpriority", entity: "Tickets", field: "priority"},
	{settings: "ticket.status, ticket.resolvedstatus", entity: "Tickets", field: "status"},
	{settings: "ticket.type", entity: "Tickets", field: "ticketType"},
	{settings: "ticket.source", entity: "Tickets", field: "source"},
	{settings: "ticket.queue", entity: "Tickets", field: "queueID"},
	{settings: "ticket.issuetype", entity: "Tickets", field: "issueType"},
	{settings: "ticket.subissuetype", entity: "Tickets", field: "subIssueType", parent: "ISSUE TYPE"},
	{settings: "ticket.category", entity: "Tickets", field: "ticketCategory"},
	{settings: "note.type", entity: "TicketNotes", field: "noteType"},
	{settings: "note.publish", entity: "TicketNotes", field: "publish"},
}

// Picklists writes the IDs and labels each picklist setting may take to w.
// Only active values are written unless all is set.
func Picklists(ctx context.Context, w io.Writer, at Autotask, all bool) error {
	fields := map[string]map[string][]PicklistValue{}
	values := make([][]PicklistValue, len(picklists))
	for i, p := range picklists {
		if _, ok := fields[p.entity]; !ok {
			f, err := at.Picklists(ctx, p.entity)
			if err != nil {
				return fmt.Errorf("reading %s picklists: %w", p.entity, err)
			}
			fields[p.entity] = f
		}
		values[i] = field(fields[p.entity], p.field)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	out := &errWriter{w: tw}
	for i, p := range picklists {
		if i > 0 {
			out.printf("\n")
		}
		out.printf("%s (%s %s)\n", p.settings, p.entity, p.field)

		shown := visible(values[i], all)
		var parents []PicklistValue
		if p.parent != "" {
			parents = values[i-1]
			shown = byParent(shown, parents, all)
		}
		if len(shown) == 0 {
			out.printf("  (none)\n")
			continue
		}

		header := []string{"ID", "LABEL"}
		if p.parent != "" {
			header = append([]string{p.parent}, header...)
		}
		if all {
			header = append(header, "ACTIVE")
		}
		out.printf("  %s\n", strings.Join(header, "\t"))
		for _, v := range shown {
			row := []string{fmt.Sprint(v.ID), v.Label}
			if p.parent != "" {
				row = append([]string{parentName(parents, v.ParentID)}, row...)
			}
			if all {
				row = append(row, yesNo(v.Active))
			}
			out.printf("  %s\n", strings.Join(row, "\t"))
		}
	}
	return out.flush(tw)
}

// Companies writes the IDs of the companies whose name contains name to w,
// ordered by name. Only active companies are written unless all is set.
func Companies(ctx context.Context, w io.Writer, at Autotask, name string, all bool) error {
	companies, err := at.SearchCompanies(ctx, name, all)
	if err != nil {
		return fmt.Errorf("searching companies: %w", err)
	}
	if len(companies) == 0 {
		_, err := fmt.Fprintf(w, "no companies match %q\n", name)
		return err
	}
	slices.SortStableFunc(companies, func(a, b Company) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	out := &errWriter{w: tw}
	if all {
		out.printf("ID\tNAME\tACTIVE\n")
	} else {
		out.printf("ID\tNAME\n")
	}
	for _, c := range companies {
		if all {
			out.printf("%d\t%s\t%s\n", c.ID, c.Name, yesNo(c.Active))
		} else {
			out.printf("%d\t%s\n", c.ID, c.Name)
		}
	}
	return out.flush(tw)
}

// WorkTypes writes the IDs ticket.worktype may take to w, ordered by name.
// Only active work types are written unless all is set.
func WorkTypes(ctx context.Context, w io.Writer, at Autotask, all bool) error {
	workTypes, err := at.WorkTypes(ctx, all)
	if err != nil {
		return fmt.Errorf("reading work types: %w", err)
	}
	if len(workTypes) == 0 {
		_, err := fmt.Fprintln(w, "no work types")
		return err
	}
	slices.SortStableFunc(workTypes, func(a, b WorkType) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	out := &errWriter{w: tw}
	if all {
		out.printf("ID\tNAME\tACTIVE\n")
	} else {
		out.printf("ID\tNAME\n")
	}
	for _, wt := range workTypes {
		if all {
			out.printf("%d\t%s\t%s\n", wt.ID, wt.Name, yesNo(wt.Active))
		} else {
			out.printf("%d\t%s\n", wt.ID, wt.Name)
		}
	}
	return out.flush(tw)
}

// field returns the values of the named field, matched ignoring case.
func field(fields map[string][]PicklistValue, name string) []PicklistValue {
	for n, v := range fields {
		if strings.EqualFold(n, name) {
			return v
		}
	}
	return nil
}

func visible(values []PicklistValue, all bool) []PicklistValue {
	if all {
		return values
	}
	return slices.DeleteFunc(slices.Clone(values), func(v PicklistValue) bool { return !v.Active })
}

// byParent orders values by the position of their parent in parents, keeping
// their order within a parent. Unless all is set, values of inactive or
// unknown parents are dropped, as they can't be used.
func byParent(values, parents []PicklistValue, all bool) []PicklistValue {
	pos := map[int]int{}
	for i, p := range parents {
		if all || p.Active {
			pos[p.ID] = i
		}
	}
	values = slices.Clone(values)
	if !all {
		values = slices.DeleteFunc(values, func(v PicklistValue) bool {
			_, ok := pos[v.ParentID]
			return !ok
		})
	}
	rank := func(v PicklistValue) int {
		if i, ok := pos[v.ParentID]; ok {
			return i
		}
		return len(parents)
	}
	slices.SortStableFunc(values, func(a, b PicklistValue) int { return rank(a) - rank(b) })
	return values
}

func parentName(parents []PicklistValue, id int) string {
	for _, p := range parents {
		if p.ID == id {
			return fmt.Sprintf("%d %s", p.ID, p.Label)
		}
	}
	return fmt.Sprint(id)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// errWriter keeps the first write error, so a table can be written without
// checking every write.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, a ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, a...)
	}
}

// flush flushes tw, returning the first write error if there was one.
func (e *errWriter) flush(tw *tabwriter.Writer) error {
	if e.err != nil {
		return e.err
	}
	return tw.Flush()
}
