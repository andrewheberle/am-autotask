package autotask_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andrewheberle/am-autotask/internal/autotask"
	"github.com/andrewheberle/am-autotask/internal/lookup"
	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/selector"
)

const (
	selectorsUDF = "Alertmanager Selectors"
	recordedUDF  = "Alertmanager Recorded Alerts"
	appliedUDF   = "Alertmanager Applied Priority"
)

func newClient(t *testing.T, f *fakeServer) *autotask.Client {
	t.Helper()
	return newClientWith(t, f, autotask.Options{})
}

// newClientWith returns a client for f with the test credentials and UDF
// names set over opts.
func newClientWith(t *testing.T, f *fakeServer, opts autotask.Options) *autotask.Client {
	t.Helper()
	opts.Username = testUser
	opts.Secret = testSecret
	opts.IntegrationCode = testCode
	opts.ZoneInformationURL = f.zoneURL()
	opts.CompanySelectorsUDF = selectorsUDF
	opts.RecordedAlertsUDF = recordedUDF
	c, err := autotask.New(context.Background(), opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

var ctx = context.Background()

func TestNewDiscoversZoneAndAuthenticates(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)

	if _, err := c.OpenTickets(ctx, "am:x"); err != nil {
		t.Errorf("OpenTickets: %v", err)
	}
}

func TestNewFailsForUnknownUser(t *testing.T) {
	f := newFakeServer(t)

	_, err := autotask.New(ctx, autotask.Options{
		Username: "nobody@example.com", Secret: testSecret, IntegrationCode: testCode,
		ZoneInformationURL: f.zoneURL(),
	})
	if err == nil {
		t.Fatal("expected zone discovery to fail")
	}
}

func TestWrongCredentialsArePermanent(t *testing.T) {
	f := newFakeServer(t)
	c, err := autotask.New(ctx, autotask.Options{
		Username: testUser, Secret: "wrong", IntegrationCode: testCode,
		ZoneInformationURL: f.zoneURL(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.OpenTickets(ctx, "am:x"); !errors.Is(err, receiver.ErrPermanent) {
		t.Errorf("err = %v, want permanent", err)
	}
}

func TestCreatedTicketIsFoundAsOpen(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	due := time.Date(2026, 10, 17, 8, 0, 0, 0, time.UTC)

	created, err := c.CreateTicket(ctx, receiver.NewTicket{
		CompanyID: 100, Title: "HostDown", Description: "desc", ExternalID: "am:abc",
		Priority: 4, Status: 1, TicketType: 5, Source: 8, Category: 3, WorkType: 29682801,
		DueDateTime: &due, RecordedAlerts: "aaaa,bbbb",
	})
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	open, err := c.OpenTickets(ctx, "am:abc")
	if err != nil {
		t.Fatalf("OpenTickets: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("expected one open ticket, got %d", len(open))
	}
	got := open[0]
	if got.ID != created.ID || got.Number == "" || got.Status != 1 || got.RecordedAlerts != "aaaa,bbbb" {
		t.Errorf("open ticket = %+v, created %+v", got, created)
	}

	stored := f.get("Tickets", int(created.ID))
	for field, want := range map[string]any{
		"companyID": 100, "title": "HostDown", "description": "desc", "externalID": "am:abc",
		"priority": 4, "status": 1, "ticketType": 5, "source": 8, "ticketCategory": 3,
		"billingCodeID": 29682801, "dueDateTime": "2026-10-17T08:00:00Z",
	} {
		if stored[field] != want {
			t.Errorf("stored %s = %v, want %v", field, stored[field], want)
		}
	}
	for _, field := range []string{"queueID", "issueType", "subIssueType"} {
		if _, ok := stored[field]; ok {
			t.Errorf("unset optional field %s should not be sent", field)
		}
	}
}

func TestCompleteTicketIsNotOpenButIsPrevious(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	first, _ := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})
	second, _ := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})
	complete := 5
	for _, id := range []int64{first.ID, second.ID} {
		if err := c.UpdateTicket(ctx, id, receiver.TicketUpdate{Status: &complete}); err != nil {
			t.Fatalf("UpdateTicket: %v", err)
		}
	}

	if open, _ := c.OpenTickets(ctx, "am:abc"); len(open) != 0 {
		t.Errorf("expected no open tickets, got %d", len(open))
	}
	prev, err := c.PreviousTicket(ctx, "am:abc")
	if err != nil {
		t.Fatalf("PreviousTicket: %v", err)
	}
	if prev == nil || prev.ID != second.ID || prev.Number == "" {
		t.Errorf("PreviousTicket = %+v, want the most recent, %d", prev, second.ID)
	}
}

func TestPreviousTicketIsNilWhenThereIsNone(t *testing.T) {
	c := newClient(t, newFakeServer(t))

	prev, err := c.PreviousTicket(ctx, "am:none")
	if err != nil || prev != nil {
		t.Errorf("PreviousTicket = %+v, %v; want nil, nil", prev, err)
	}
}

func TestCreateTicketSendsCompanyID(t *testing.T) {
	tests := []struct {
		name      string
		companyID int64
	}{
		{"company", 100},
		// company 0 is the Autotask instance's own company
		{"own company", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeServer(t)
			c := newClient(t, f)

			created, err := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: tt.companyID, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})
			if err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}

			stored := f.get("Tickets", int(created.ID))
			if got, ok := stored["companyID"]; !ok || !equal(got, tt.companyID) {
				t.Errorf("stored companyID = %v (sent %t), want %d", got, ok, tt.companyID)
			}
		})
	}
}

func TestUpdateTicketChangesOnlyGivenFields(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	created, _ := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1, RecordedAlerts: "aaaa"})

	recorded := "aaaa,bbbb"
	if err := c.UpdateTicket(ctx, created.ID, receiver.TicketUpdate{RecordedAlerts: &recorded}); err != nil {
		t.Fatal(err)
	}

	stored := f.get("Tickets", int(created.ID))
	if udf(stored, recordedUDF) != "aaaa,bbbb" || stored["status"] != 1 || stored["externalID"] != "am:abc" || stored["companyID"] != 1 {
		t.Errorf("stored ticket = %v", stored)
	}

	externalID := "am:dup:abc"
	if err := c.UpdateTicket(ctx, created.ID, receiver.TicketUpdate{ExternalID: &externalID}); err != nil {
		t.Fatal(err)
	}
	if open, _ := c.OpenTickets(ctx, "am:abc"); len(open) != 0 {
		t.Error("ticket should no longer be found under its old external ID")
	}
}

func TestAppliedPriority(t *testing.T) {
	tests := []struct {
		name       string
		appliedUDF string
		applied    string
		// the applied priority after creation, and after an update setting
		// it to "override"
		wantCreated, wantUpdated string
	}{
		{"following severity", appliedUDF, "4", "4", "override"},
		{"not following severity", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeServer(t)
			c := newClientWith(t, f, autotask.Options{AppliedPriorityUDF: tt.appliedUDF})

			created, err := c.CreateTicket(ctx, receiver.NewTicket{
				CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 4, Status: 1,
				RecordedAlerts: "aaaa", AppliedPriority: tt.applied,
			})
			if err != nil {
				t.Fatal(err)
			}
			if created.Priority != 4 || created.AppliedPriority != tt.wantCreated {
				t.Errorf("created priority, applied = %d, %q; want 4, %q", created.Priority, created.AppliedPriority, tt.wantCreated)
			}
			stored := udf(f.get("Tickets", int(created.ID)), appliedUDF)
			if (tt.wantCreated == "" && stored != nil) || (tt.wantCreated != "" && stored != tt.wantCreated) {
				t.Errorf("stored applied priority = %v, want %q", stored, tt.wantCreated)
			}

			priority, recorded, override := 3, "aaaa,bbbb", "override"
			if err := c.UpdateTicket(ctx, created.ID, receiver.TicketUpdate{
				Priority: &priority, RecordedAlerts: &recorded, AppliedPriority: &override,
			}); err != nil {
				t.Fatal(err)
			}

			open, err := c.OpenTickets(ctx, "am:abc")
			if err != nil || len(open) != 1 {
				t.Fatalf("OpenTickets = %v, %v", open, err)
			}
			got := open[0]
			if got.Priority != 3 || got.RecordedAlerts != "aaaa,bbbb" || got.AppliedPriority != tt.wantUpdated {
				t.Errorf("updated ticket priority, recorded, applied = %d, %q, %q; want 3, %q, %q",
					got.Priority, got.RecordedAlerts, got.AppliedPriority, "aaaa,bbbb", tt.wantUpdated)
			}
		})
	}
}

func TestCreateNote(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	created, _ := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})

	if err := c.CreateNote(ctx, receiver.NewNote{TicketID: created.ID, Title: "1 firing, 0 resolved", Description: "body", NoteType: 1, Publish: 2}); err != nil {
		t.Fatalf("CreateNote: %v", err)
	}

	notes := f.notesFor(int(created.ID))
	if len(notes) != 1 {
		t.Fatalf("expected one note, got %d", len(notes))
	}
	n := notes[0]
	if n["title"] != "1 firing, 0 resolved" || n["description"] != "body" || n["noteType"] != float64(1) || n["publish"] != float64(2) {
		t.Errorf("stored note = %v", n)
	}
}

func TestOpenTicketsPagesThroughResultsOldestFirst(t *testing.T) {
	f := newFakeServer(t)
	f.pageSize = 2
	c := newClient(t, f)
	var want []int64
	for range 5 {
		created, _ := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})
		want = append(want, created.ID)
	}

	open, err := c.OpenTickets(ctx, "am:abc")
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, o := range open {
		got = append(got, o.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("open ticket IDs = %v, want %v", got, want)
	}
}

func TestCompaniesReturnsSelectorsOfActiveCompanies(t *testing.T) {
	f := newFakeServer(t)
	f.pageSize = 2
	sel := func(v string) []entity { return []entity{{"name": selectorsUDF, "value": v}} }
	f.add("Companies", entity{"id": 1, "isActive": true, "userDefinedFields": sel(`{tenant_id="a"}`)})
	f.add("Companies", entity{"id": 2, "isActive": true, "userDefinedFields": sel("")})
	f.add("Companies", entity{"id": 3, "isActive": false, "userDefinedFields": sel(`{tenant_id="c"}`)})
	f.add("Companies", entity{"id": 4, "isActive": true, "userDefinedFields": []entity{}})
	f.add("Companies", entity{"id": 5, "isActive": true, "userDefinedFields": sel(`{tenant_id="e"}`)})
	f.add("Companies", entity{"id": 6, "isActive": true, "userDefinedFields": sel(`{tenant_id="f"}`)})
	c := newClient(t, f)

	got, err := c.Companies(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := []selector.Company{{ID: 1, Selectors: `{tenant_id="a"}`}, {ID: 5, Selectors: `{tenant_id="e"}`}, {ID: 6, Selectors: `{tenant_id="f"}`}}
	if !slices.Equal(got, want) {
		t.Errorf("Companies = %v, want %v", got, want)
	}
}

func TestErrorsAreClassified(t *testing.T) {
	tests := []struct {
		status    int
		permanent bool
	}{
		{429, false},
		{500, false},
		{503, false},
		{400, true},
		{404, true},
	}
	for _, tt := range tests {
		f := newFakeServer(t)
		c := newClient(t, f)
		f.failNext(tt.status, `{"errors":["something went wrong"]}`)

		_, err := c.OpenTickets(ctx, "am:abc")
		if err == nil {
			t.Errorf("status %d: expected an error", tt.status)
			continue
		}
		if got := errors.Is(err, receiver.ErrPermanent); got != tt.permanent {
			t.Errorf("status %d: permanent = %v, want %v (err %v)", tt.status, got, tt.permanent, err)
		}
		if !strings.Contains(err.Error(), "something went wrong") {
			t.Errorf("status %d: error should include Autotask's message: %v", tt.status, err)
		}
	}
}

func TestUnreachableAutotaskIsNotPermanent(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	f.srv.Close()

	if _, err := c.OpenTickets(ctx, "am:abc"); err == nil || errors.Is(err, receiver.ErrPermanent) {
		t.Errorf("err = %v, want a retryable error", err)
	}
}

func TestConcurrentRequestsStayWithinThreadLimit(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)
	f.hold = make(chan struct{})

	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() { _, _ = c.OpenTickets(ctx, "am:abc") })
	}

	// wait for the first requests to arrive, then give any excess a chance
	deadline := time.Now().Add(2 * time.Second)
	for f.inflight.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(f.hold)
	wg.Wait()

	if got := f.maxInflight.Load(); got != 3 {
		t.Errorf("max concurrent requests = %d, want 3", got)
	}
}

func validationServer(t *testing.T) *fakeServer {
	f := newFakeServer(t)
	f.fields["Tickets"] = []entity{
		picklist("priority", pv(4, "P1 - Critical", true), pv(3, "P4 - Low", true), pv(9, "Old", false)),
		picklist("status", pv(1, "New", true), pv(5, "Complete", true)),
	}
	f.fields["TicketNotes"] = []entity{
		picklist("noteType", pv(1, "Task Summary", true)),
		picklist("publish", pv(2, "Internal Project Team", true)),
	}
	f.udfs["Tickets"] = []entity{{"name": recordedUDF}, {"name": appliedUDF}}
	f.udfs["Companies"] = []entity{{"name": selectorsUDF}}
	f.add("Companies", entity{"id": 1, "isActive": true})
	f.add("BillingCodes", entity{"id": 30, "name": "Remote Support", "useType": 1, "isActive": true})
	f.add("BillingCodes", entity{"id": 31, "name": "Retired Work", "useType": 1, "isActive": false})
	f.add("BillingCodes", entity{"id": 32, "name": "Travel Expenses", "useType": 2, "isActive": true})
	return f
}

func goodChecks() []autotask.PicklistCheck {
	return []autotask.PicklistCheck{
		{Entity: "Tickets", Field: "priority", Value: 4, Setting: "ticket.priorities.critical"},
		{Entity: "Tickets", Field: "priority", Value: 3, Setting: "ticket.defaultpriority"},
		{Entity: "Tickets", Field: "status", Value: 1, Setting: "ticket.status"},
		{Entity: "TicketNotes", Field: "noteType", Value: 1, Setting: "note.type"},
		{Entity: "TicketNotes", Field: "publish", Value: 2, Setting: "note.publish"},
	}
}

func TestValidateAcceptsGoodConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		opts     autotask.Options
		workType int
	}{
		{"not following severity", autotask.Options{}, 0},
		{"following severity", autotask.Options{AppliedPriorityUDF: appliedUDF}, 0},
		{"with a work type", autotask.Options{}, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newClientWith(t, validationServer(t), tt.opts)

			if err := c.Validate(ctx, goodChecks(), 1, tt.workType); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRejectsBadConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(f *fakeServer, checks []autotask.PicklistCheck) []autotask.PicklistCheck
		catchAll int64
		workType int
		want     string
	}{
		{"unknown picklist value", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			return append(c, autotask.PicklistCheck{Entity: "Tickets", Field: "priority", Value: 7, Setting: "ticket.priorities.warning"})
		}, 1, 0, "ticket.priorities.warning: Tickets priority has no value 7; active values are 4 (P1 - Critical), 3 (P4 - Low)"},
		{"inactive picklist value", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			return append(c, autotask.PicklistCheck{Entity: "Tickets", Field: "priority", Value: 9, Setting: "ticket.priorities.info"})
		}, 1, 0, "ticket.priorities.info: Tickets priority value 9 (Old) is inactive; active values are 4 (P1 - Critical), 3 (P4 - Low)"},
		{"picklist with no active values", func(f *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			f.fields["Tickets"] = append(f.fields["Tickets"], picklist("queueID", pv(7, "Closed", false)))
			return append(c, autotask.PicklistCheck{Entity: "Tickets", Field: "queueID", Value: 8, Setting: "ticket.queue"})
		}, 1, 0, "ticket.queue: Tickets queueID has no value 8; it has no active values"},
		{"missing recorded alerts UDF", func(f *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			f.udfs["Tickets"] = nil
			return c
		}, 1, 0, recordedUDF},
		{"missing company selectors UDF", func(f *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			f.udfs["Companies"] = nil
			return c
		}, 1, 0, selectorsUDF},
		{"unknown catch-all company", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck { return c }, 99, 0, "catch-all company 99"},
		{"missing applied priority UDF", func(f *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			f.udfs["Tickets"] = []entity{{"name": recordedUDF}}
			return c
		}, 1, 0, appliedUDF},
		{"unknown work type", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck { return c }, 1, 99, "work type 99 does not exist; active work types are 30 (Remote Support)"},
		{"inactive work type", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck { return c }, 1, 31, "work type 31 (Retired Work) is inactive; active work types are 30 (Remote Support)"},
		{"billing code that is not a work type", func(_ *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck { return c }, 1, 32, "work type 32 does not exist"},
		{"no active work types", func(f *fakeServer, c []autotask.PicklistCheck) []autotask.PicklistCheck {
			f.entities["BillingCodes"] = nil
			return c
		}, 1, 30, "work type 30 does not exist; there are no active work types"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validationServer(t)
			checks := tt.mutate(f, goodChecks())
			c := newClientWith(t, f, autotask.Options{AppliedPriorityUDF: appliedUDF})

			err := c.Validate(ctx, checks, tt.catchAll, tt.workType)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestCreateTicketReturnsItsNumber(t *testing.T) {
	f := newFakeServer(t)
	c := newClient(t, f)

	created, err := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: "am:abc", Priority: 1, Status: 1})
	if err != nil {
		t.Fatal(err)
	}

	stored := f.get("Tickets", int(created.ID))
	if want := stored["ticketNumber"]; created.Number == "" || created.Number != want {
		t.Errorf("Number = %q, want %v", created.Number, want)
	}
	if _, ok := stored["billingCodeID"]; ok {
		t.Error("unset work type should not be sent")
	}
}

func TestOpenAlertTicketsReturnsOpenTicketsOfEveryAlertGroup(t *testing.T) {
	f := newFakeServer(t)
	f.pageSize = 2
	c := newClient(t, f)
	create := func(externalID, recorded string) receiver.Ticket {
		created, err := c.CreateTicket(ctx, receiver.NewTicket{CompanyID: 1, Title: "a", ExternalID: externalID, Priority: 1, Status: 1, RecordedAlerts: recorded})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	a := create("am:aaa", "aaaa")
	done := create("am:bbb", "bbbb")
	create("INC-20910-2021", "cccc")
	dup := create("am:dup:ddd", "dddd")
	e := create("am:eee", "eeee,ffff")
	complete := 5
	if err := c.UpdateTicket(ctx, done.ID, receiver.TicketUpdate{Status: &complete}); err != nil {
		t.Fatal(err)
	}

	got, err := c.OpenAlertTickets(ctx)
	if err != nil {
		t.Fatal(err)
	}

	want := []receiver.Ticket{
		{ID: a.ID, Number: a.Number, Status: 1, ExternalID: "am:aaa", RecordedAlerts: "aaaa"},
		{ID: dup.ID, Number: dup.Number, Status: 1, ExternalID: "am:dup:ddd", RecordedAlerts: "dddd"},
		{ID: e.ID, Number: e.Number, Status: 1, ExternalID: "am:eee", RecordedAlerts: "eeee,ffff"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("OpenAlertTickets =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPicklists(t *testing.T) {
	f := newFakeServer(t)
	sub := pv(34, "Laptop", true)
	sub["parentValue"] = "12"
	f.fields["Tickets"] = []entity{
		{"name": "title", "isPickList": false},
		picklist("issueType", pv(12, "Hardware", true), pv(13, "Retired", false)),
		picklist("subIssueType", sub),
	}
	c := newClient(t, f)

	got, err := c.Picklists(ctx, "Tickets")
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]lookup.PicklistValue{
		"issueType":    {{ID: 12, Label: "Hardware", Active: true}, {ID: 13, Label: "Retired"}},
		"subIssueType": {{ID: 34, Label: "Laptop", Active: true, ParentID: 12}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Picklists = %+v, want %+v", got, want)
	}
}

func TestSearchCompanies(t *testing.T) {
	tests := []struct {
		name            string
		search          string
		includeInactive bool
		want            []int64
	}{
		{"active companies containing the name, ignoring case", "group", false, []int64{1, 3}},
		{"including inactive companies", "group", true, []int64{1, 2, 3}},
		{"no match", "nothing", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeServer(t)
			f.pageSize = 1
			f.add("Companies", entity{"id": 1, "companyName": "R-Group International", "isActive": true})
			f.add("Companies", entity{"id": 2, "companyName": "Old Group", "isActive": false})
			f.add("Companies", entity{"id": 3, "companyName": "GROUP Two", "isActive": true})
			f.add("Companies", entity{"id": 4, "companyName": "Other", "isActive": true})
			c := newClient(t, f)

			got, err := c.SearchCompanies(ctx, tt.search, tt.includeInactive)
			if err != nil {
				t.Fatal(err)
			}

			var ids []int64
			for _, company := range got {
				ids = append(ids, company.ID)
				if company.Name == "" {
					t.Errorf("company %d has no name", company.ID)
				}
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("IDs = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestWorkTypes(t *testing.T) {
	tests := []struct {
		name            string
		includeInactive bool
		want            []lookup.WorkType
	}{
		{"active work types", false, []lookup.WorkType{{ID: 30, Name: "Remote Support", Active: true}}},
		{"including inactive work types", true, []lookup.WorkType{{ID: 30, Name: "Remote Support", Active: true}, {ID: 31, Name: "Retired Work"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validationServer(t)
			f.pageSize = 1
			c := newClient(t, f)

			got, err := c.WorkTypes(ctx, tt.includeInactive)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("WorkTypes = %+v, want %+v", got, tt.want)
			}
		})
	}
}
