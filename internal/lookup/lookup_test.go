package lookup_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/andrewheberle/am-autotask/internal/lookup"
)

type fakeAutotask struct {
	picklists map[string]map[string][]lookup.PicklistValue
	companies []lookup.Company
	workTypes []lookup.WorkType
	err       error

	searched        string
	includeInactive bool
}

func (f *fakeAutotask) Picklists(_ context.Context, entity string) (map[string][]lookup.PicklistValue, error) {
	return f.picklists[entity], f.err
}

func (f *fakeAutotask) SearchCompanies(_ context.Context, name string, includeInactive bool) ([]lookup.Company, error) {
	f.searched, f.includeInactive = name, includeInactive
	return f.companies, f.err
}

func (f *fakeAutotask) WorkTypes(_ context.Context, includeInactive bool) ([]lookup.WorkType, error) {
	f.includeInactive = includeInactive
	return f.workTypes, f.err
}

func newFake() *fakeAutotask {
	return &fakeAutotask{picklists: map[string]map[string][]lookup.PicklistValue{
		"Tickets": {
			"priority": {{ID: 4, Label: "Critical", Active: true}, {ID: 1, Label: "High", Active: true}, {ID: 9, Label: "Old"}},
			"status":   {{ID: 1, Label: "New", Active: true}, {ID: 5, Label: "Complete", Active: true}},
			// field names are matched ignoring case
			"TicketType": {{ID: 5, Label: "Alert", Active: true}},
			"source":     {{ID: 8, Label: "Monitoring Alert", Active: true}},
			"queueID":    {{ID: 8, Label: "Service Desk", Active: true}},
			"issueType":  {{ID: 12, Label: "Hardware", Active: true}, {ID: 13, Label: "Retired"}, {ID: 11, Label: "Software", Active: true}},
			"subIssueType": {
				{ID: 31, Label: "Patching", Active: true, ParentID: 11},
				{ID: 34, Label: "Laptop", Active: true, ParentID: 12},
				{ID: 35, Label: "Old laptop", ParentID: 12},
				{ID: 36, Label: "Fax", Active: true, ParentID: 13},
			},
		},
		"TicketNotes": {
			"noteType": {{ID: 1, Label: "Task Summary", Active: true}},
			"publish":  {{ID: 2, Label: "Internal Project Team", Active: true}},
		},
	}}
}

func TestPicklists(t *testing.T) {
	tests := []struct {
		name string
		all  bool
		want string
	}{
		{"active values only", false, `ticket.priorities, ticket.defaultpriority (Tickets priority)
  ID  LABEL
  4   Critical
  1   High

ticket.status, ticket.resolvedstatus (Tickets status)
  ID  LABEL
  1   New
  5   Complete

ticket.type (Tickets ticketType)
  ID  LABEL
  5   Alert

ticket.source (Tickets source)
  ID  LABEL
  8   Monitoring Alert

ticket.queue (Tickets queueID)
  ID  LABEL
  8   Service Desk

ticket.issuetype (Tickets issueType)
  ID  LABEL
  12  Hardware
  11  Software

ticket.subissuetype (Tickets subIssueType)
  ISSUE TYPE   ID  LABEL
  12 Hardware  34  Laptop
  11 Software  31  Patching

ticket.category (Tickets ticketCategory)
  (none)

note.type (TicketNotes noteType)
  ID  LABEL
  1   Task Summary

note.publish (TicketNotes publish)
  ID  LABEL
  2   Internal Project Team
`},
		{"all values", true, `ticket.priorities, ticket.defaultpriority (Tickets priority)
  ID  LABEL     ACTIVE
  4   Critical  yes
  1   High      yes
  9   Old       no

ticket.status, ticket.resolvedstatus (Tickets status)
  ID  LABEL     ACTIVE
  1   New       yes
  5   Complete  yes

ticket.type (Tickets ticketType)
  ID  LABEL  ACTIVE
  5   Alert  yes

ticket.source (Tickets source)
  ID  LABEL             ACTIVE
  8   Monitoring Alert  yes

ticket.queue (Tickets queueID)
  ID  LABEL         ACTIVE
  8   Service Desk  yes

ticket.issuetype (Tickets issueType)
  ID  LABEL     ACTIVE
  12  Hardware  yes
  13  Retired   no
  11  Software  yes

ticket.subissuetype (Tickets subIssueType)
  ISSUE TYPE   ID  LABEL       ACTIVE
  12 Hardware  34  Laptop      yes
  12 Hardware  35  Old laptop  no
  13 Retired   36  Fax         yes
  11 Software  31  Patching    yes

ticket.category (Tickets ticketCategory)
  (none)

note.type (TicketNotes noteType)
  ID  LABEL         ACTIVE
  1   Task Summary  yes

note.publish (TicketNotes publish)
  ID  LABEL                  ACTIVE
  2   Internal Project Team  yes
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if err := lookup.Picklists(context.Background(), &b, newFake(), tt.all); err != nil {
				t.Fatal(err)
			}
			if got := b.String(); got != tt.want {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestCompanies(t *testing.T) {
	companies := []lookup.Company{
		{ID: 175, Name: "R-Group International", Active: true},
		{ID: 12, Name: "Old Group", Active: false},
		{ID: 3, Name: "group two", Active: true},
	}
	tests := []struct {
		name      string
		all       bool
		companies []lookup.Company
		want      string
	}{
		{"ordered by name", false, companies, `ID   NAME
3    group two
12   Old Group
175  R-Group International
`},
		{"all shows whether active", true, companies, `ID   NAME                   ACTIVE
3    group two              yes
12   Old Group              no
175  R-Group International  yes
`},
		{"no match", false, nil, `no companies match "group"
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAutotask{companies: tt.companies}
			var b strings.Builder
			if err := lookup.Companies(context.Background(), &b, f, "group", tt.all); err != nil {
				t.Fatal(err)
			}
			if got := b.String(); got != tt.want {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.want)
			}
			if f.searched != "group" || f.includeInactive != tt.all {
				t.Errorf("searched %q, includeInactive %v; want %q, %v", f.searched, f.includeInactive, "group", tt.all)
			}
		})
	}
}

func TestErrorsAreReturned(t *testing.T) {
	f := &fakeAutotask{err: errors.New("boom")}
	var b strings.Builder

	if err := lookup.Picklists(context.Background(), &b, f, false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Picklists err = %v", err)
	}
	if err := lookup.Companies(context.Background(), &b, f, "x", false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("Companies err = %v", err)
	}
	if err := lookup.WorkTypes(context.Background(), &b, f, false); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("WorkTypes err = %v", err)
	}
}

func TestWorkTypes(t *testing.T) {
	workTypes := []lookup.WorkType{
		{ID: 29682801, Name: "Remote Support", Active: true},
		{ID: 29682805, Name: "after hours", Active: true},
		{ID: 29682810, Name: "Old Work"},
	}
	tests := []struct {
		name      string
		all       bool
		workTypes []lookup.WorkType
		want      string
	}{
		{"ordered by name", false, workTypes, `ID        NAME
29682805  after hours
29682810  Old Work
29682801  Remote Support
`},
		{"all shows whether active", true, workTypes, `ID        NAME            ACTIVE
29682805  after hours     yes
29682810  Old Work        no
29682801  Remote Support  yes
`},
		{"none", false, nil, `no work types
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAutotask{workTypes: tt.workTypes}
			var b strings.Builder
			if err := lookup.WorkTypes(context.Background(), &b, f, tt.all); err != nil {
				t.Fatal(err)
			}
			if got := b.String(); got != tt.want {
				t.Errorf("output:\n%s\nwant:\n%s", got, tt.want)
			}
			if f.includeInactive != tt.all {
				t.Errorf("includeInactive = %v, want %v", f.includeInactive, tt.all)
			}
		})
	}
}
