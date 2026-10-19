// Package autotask is a client for the parts of the Autotask PSA REST API
// used by the receiver.
package autotask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andrewheberle/am-autotask/internal/receiver"
	"github.com/andrewheberle/am-autotask/internal/selector"
)

// DefaultZoneInformationURL is where the API zone for a user is looked up.
const DefaultZoneInformationURL = "https://webservices.autotask.net/atservicesrest/v1.0/zoneInformation"

// completeStatus is the system "Complete" ticket status, the only status
// Autotask treats as closed.
const completeStatus = 5

// Options configures a [Client].
type Options struct {
	Username        string
	Secret          string
	IntegrationCode string

	// CompanySelectorsUDF names the Company UDF holding company selectors.
	CompanySelectorsUDF string
	// RecordedAlertsUDF names the Ticket UDF holding recorded alerts.
	RecordedAlertsUDF string

	// ZoneInformationURL defaults to [DefaultZoneInformationURL].
	ZoneInformationURL string
	// HTTPClient defaults to a client with a 60 second timeout.
	HTTPClient *http.Client
	// MaxConcurrent bounds concurrent requests to stay within the Autotask
	// thread limit of three per endpoint. Default 3.
	MaxConcurrent int
}

// Client calls the Autotask REST API. It implements [receiver.Autotask].
type Client struct {
	opts Options
	base string
	http *http.Client
	sem  chan struct{}
}

var _ receiver.Autotask = (*Client)(nil)

// New discovers the API zone for the user and returns a client for it.
func New(ctx context.Context, opts Options) (*Client, error) {
	if opts.ZoneInformationURL == "" {
		opts.ZoneInformationURL = DefaultZoneInformationURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 60 * time.Second}
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 3
	}

	c := &Client{opts: opts, http: opts.HTTPClient, sem: make(chan struct{}, opts.MaxConcurrent)}

	var zone struct {
		URL string `json:"url"`
	}
	zoneURL := opts.ZoneInformationURL + "?user=" + url.QueryEscape(opts.Username)
	if err := c.do(ctx, http.MethodGet, zoneURL, nil, &zone); err != nil {
		return nil, fmt.Errorf("discovering Autotask zone: %w", err)
	}
	if zone.URL == "" {
		return nil, errors.New("discovering Autotask zone: no URL returned")
	}
	c.base = strings.TrimSuffix(zone.URL, "/") + "/v1.0"

	return c, nil
}

// apiError is a non-2xx response from Autotask.
type apiError struct {
	status   int
	messages []string
}

func (e *apiError) Error() string {
	if len(e.messages) == 0 {
		return fmt.Sprintf("autotask returned HTTP %d", e.status)
	}
	return fmt.Sprintf("autotask returned HTTP %d: %s", e.status, strings.Join(e.messages, "; "))
}

// Unwrap marks client errors other than rate limiting as permanent.
func (e *apiError) Unwrap() error {
	if e.status >= 400 && e.status < 500 && e.status != http.StatusTooManyRequests {
		return receiver.ErrPermanent
	}
	return nil
}

// IsRateLimited reports whether err is Autotask rejecting a request for
// exceeding its thread or request limits.
func IsRateLimited(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.status == http.StatusTooManyRequests
}

// do sends a request to an absolute URL and decodes a JSON response into out
// (if non-nil).
func (c *Client) do(ctx context.Context, method, rawURL string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("UserName", c.opts.Username)
	req.Header.Set("Secret", c.opts.Secret)
	req.Header.Set("ApiIntegrationcode", c.opts.IntegrationCode)

	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	resp, err := c.http.Do(req)
	<-c.sem
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &apiError{status: resp.StatusCode}
		var e struct {
			Errors []string `json:"errors"`
		}
		if json.Unmarshal(data, &e) == nil {
			apiErr.messages = e.Errors
		}
		return apiErr
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *Client) url(path string) string {
	return c.base + path
}

type filter struct {
	Op    string `json:"op"`
	Field string `json:"field"`
	UDF   bool   `json:"udf,omitempty"`
	Value any    `json:"value,omitempty"`
}

type udfValue struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

type udfValues []udfValue

func (u udfValues) get(name string) string {
	for _, v := range u {
		if strings.EqualFold(v.Name, name) && v.Value != nil {
			return *v.Value
		}
	}
	return ""
}

// query returns every entity matching the filters, following pagination.
// Autotask returns results ordered by id. When include is set only those
// fields are returned; Autotask then omits UDFs not named in it.
func query[T any](ctx context.Context, c *Client, entity string, filters []filter, include ...string) ([]T, error) {
	var page struct {
		Items       []T `json:"items"`
		PageDetails struct {
			NextPageURL *string `json:"nextPageUrl"`
		} `json:"pageDetails"`
	}

	body := map[string]any{"filter": filters}
	if len(include) > 0 {
		body["IncludeFields"] = include
	}
	if err := c.do(ctx, http.MethodPost, c.url("/"+entity+"/query"), body, &page); err != nil {
		return nil, fmt.Errorf("querying %s: %w", entity, err)
	}
	items := page.Items

	for page.PageDetails.NextPageURL != nil && *page.PageDetails.NextPageURL != "" {
		next := *page.PageDetails.NextPageURL
		page.Items, page.PageDetails.NextPageURL = nil, nil
		if err := c.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, fmt.Errorf("querying %s: %w", entity, err)
		}
		items = append(items, page.Items...)
	}

	return items, nil
}

type ticketItem struct {
	ID                int64     `json:"id"`
	TicketNumber      string    `json:"ticketNumber"`
	Status            int       `json:"status"`
	ExternalID        string    `json:"externalID"`
	UserDefinedFields udfValues `json:"userDefinedFields"`
}

func (c *Client) toTicket(t ticketItem) receiver.Ticket {
	return receiver.Ticket{
		ID:             t.ID,
		Number:         t.TicketNumber,
		Status:         t.Status,
		ExternalID:     t.ExternalID,
		RecordedAlerts: t.UserDefinedFields.get(c.opts.RecordedAlertsUDF),
	}
}

// OpenAlertTickets implements [receiver.Autotask]. Only the fields needed
// are requested, as every open alert ticket is returned.
func (c *Client) OpenAlertTickets(ctx context.Context) ([]receiver.Ticket, error) {
	items, err := query[ticketItem](ctx, c, "Tickets", []filter{
		{Op: "beginsWith", Field: "externalID", Value: "am:"},
		{Op: "noteq", Field: "status", Value: completeStatus},
	}, "id", "ticketNumber", "status", "externalID", c.opts.RecordedAlertsUDF)
	if err != nil {
		return nil, err
	}

	tickets := make([]receiver.Ticket, 0, len(items))
	for _, t := range items {
		tickets = append(tickets, c.toTicket(t))
	}
	return tickets, nil
}

// OpenTickets implements [receiver.Autotask].
func (c *Client) OpenTickets(ctx context.Context, externalID string) ([]receiver.Ticket, error) {
	items, err := query[ticketItem](ctx, c, "Tickets", []filter{
		{Op: "eq", Field: "externalID", Value: externalID},
		{Op: "noteq", Field: "status", Value: completeStatus},
	})
	if err != nil {
		return nil, err
	}

	tickets := make([]receiver.Ticket, 0, len(items))
	for _, t := range items {
		tickets = append(tickets, c.toTicket(t))
	}
	return tickets, nil
}

// PreviousTicket implements [receiver.Autotask].
func (c *Client) PreviousTicket(ctx context.Context, externalID string) (*receiver.Ticket, error) {
	items, err := query[ticketItem](ctx, c, "Tickets", []filter{
		{Op: "eq", Field: "externalID", Value: externalID},
		{Op: "eq", Field: "status", Value: completeStatus},
	})
	if err != nil || len(items) == 0 {
		return nil, err
	}

	latest := items[0]
	for _, t := range items[1:] {
		if t.ID > latest.ID {
			latest = t
		}
	}
	ticket := c.toTicket(latest)
	return &ticket, nil
}

type ticketBody struct {
	ID                int64      `json:"id,omitempty"`
	CompanyID         *int64     `json:"companyID,omitempty"` // a pointer as 0 is a valid company
	Title             string     `json:"title,omitempty"`
	Description       string     `json:"description,omitempty"`
	ExternalID        *string    `json:"externalID,omitempty"`
	Priority          int        `json:"priority,omitempty"`
	Status            int        `json:"status,omitempty"`
	TicketType        int        `json:"ticketType,omitempty"`
	Source            int        `json:"source,omitempty"`
	QueueID           int        `json:"queueID,omitempty"`
	IssueType         int        `json:"issueType,omitempty"`
	SubIssueType      int        `json:"subIssueType,omitempty"`
	TicketCategory    int        `json:"ticketCategory,omitempty"`
	DueDateTime       string     `json:"dueDateTime,omitempty"`
	UserDefinedFields []udfValue `json:"userDefinedFields,omitempty"`
}

type createResponse struct {
	ItemID int64 `json:"itemId"`
}

// CreateTicket implements [receiver.Autotask]. Autotask only returns the new
// ID, so the ticket is read back for its number.
func (c *Client) CreateTicket(ctx context.Context, t receiver.NewTicket) (receiver.Ticket, error) {
	body := ticketBody{
		CompanyID:      &t.CompanyID,
		Title:          t.Title,
		Description:    t.Description,
		ExternalID:     &t.ExternalID,
		Priority:       t.Priority,
		Status:         t.Status,
		TicketType:     t.TicketType,
		Source:         t.Source,
		QueueID:        t.QueueID,
		IssueType:      t.IssueType,
		SubIssueType:   t.SubIssueType,
		TicketCategory: t.Category,
		UserDefinedFields: []udfValue{
			{Name: c.opts.RecordedAlertsUDF, Value: &t.RecordedAlerts},
		},
	}
	if t.DueDateTime != nil {
		body.DueDateTime = t.DueDateTime.UTC().Format(time.RFC3339)
	}

	var resp createResponse
	if err := c.do(ctx, http.MethodPost, c.url("/Tickets"), body, &resp); err != nil {
		return receiver.Ticket{}, fmt.Errorf("creating ticket: %w", err)
	}

	var created struct {
		Item *ticketItem `json:"item"`
	}
	if err := c.do(ctx, http.MethodGet, c.url("/Tickets/"+strconv.FormatInt(resp.ItemID, 10)), nil, &created); err != nil {
		return receiver.Ticket{}, fmt.Errorf("reading created ticket %d: %w", resp.ItemID, err)
	}
	if created.Item == nil {
		return receiver.Ticket{}, fmt.Errorf("reading created ticket %d: not found", resp.ItemID)
	}

	return c.toTicket(*created.Item), nil
}

// UpdateTicket implements [receiver.Autotask].
func (c *Client) UpdateTicket(ctx context.Context, id int64, u receiver.TicketUpdate) error {
	body := ticketBody{ID: id, ExternalID: u.ExternalID}
	if u.Status != nil {
		body.Status = *u.Status
	}
	if u.RecordedAlerts != nil {
		body.UserDefinedFields = []udfValue{{Name: c.opts.RecordedAlertsUDF, Value: u.RecordedAlerts}}
	}

	if err := c.do(ctx, http.MethodPatch, c.url("/Tickets"), body, nil); err != nil {
		return fmt.Errorf("updating ticket %d: %w", id, err)
	}
	return nil
}

// CreateNote implements [receiver.Autotask].
func (c *Client) CreateNote(ctx context.Context, n receiver.NewNote) error {
	body := map[string]any{
		"ticketID":    n.TicketID,
		"title":       n.Title,
		"description": n.Description,
		"noteType":    n.NoteType,
		"publish":     n.Publish,
	}

	path := "/Tickets/" + strconv.FormatInt(n.TicketID, 10) + "/Notes"
	if err := c.do(ctx, http.MethodPost, c.url(path), body, nil); err != nil {
		return fmt.Errorf("creating note on ticket %d: %w", n.TicketID, err)
	}
	return nil
}

// Companies returns the company selectors of every active company that has
// them set. It is a [selector.Source].
func (c *Client) Companies(ctx context.Context) ([]selector.Company, error) {
	items, err := query[struct {
		ID                int64     `json:"id"`
		UserDefinedFields udfValues `json:"userDefinedFields"`
	}](ctx, c, "Companies", []filter{
		{Op: "exist", Field: c.opts.CompanySelectorsUDF, UDF: true},
		{Op: "eq", Field: "isActive", Value: true},
	})
	if err != nil {
		return nil, err
	}

	var companies []selector.Company
	for _, item := range items {
		if v := strings.TrimSpace(item.UserDefinedFields.get(c.opts.CompanySelectorsUDF)); v != "" {
			companies = append(companies, selector.Company{ID: item.ID, Selectors: v})
		}
	}
	return companies, nil
}
