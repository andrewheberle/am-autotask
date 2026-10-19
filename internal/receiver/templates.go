package receiver

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// Names of the ticket templates, each a block a templates file may define.
const (
	TemplateTicketTitle        = "ticket.title"
	TemplateTicketDescription  = "ticket.description"
	TemplateNoteTitle          = "note.title"
	TemplateNoteDescription    = "note.description"
	TemplateProblemTitle       = "problem.title"
	TemplateProblemDescription = "problem.description"
)

// TemplateNames lists every ticket template, in the order they are shown.
var TemplateNames = []string{
	TemplateTicketTitle, TemplateTicketDescription, TemplateNoteTitle, TemplateNoteDescription,
	TemplateProblemTitle, TemplateProblemDescription,
}

// defaultTimeLayout is the layout of the time template function when none is
// given.
const defaultTimeLayout = "2006-01-02 15:04:05 MST"

//go:embed templates/default.tmpl templates/sample.json
var templateFS embed.FS

// Group is the alert group data shared by every ticket template.
type Group struct {
	Receiver          string
	Status            string
	GroupLabels       map[string]string
	CommonLabels      map[string]string
	CommonAnnotations map[string]string
	ExternalURL       string
	// AlertmanagerURL links to the receiver's alerts in Alertmanager, or is
	// empty when Alertmanager has no external URL.
	AlertmanagerURL string
	// Severity is the alert group's severity when the template is executed;
	// for a ticket that is when it was raised.
	Severity string
}

// TicketData is the data the ticket title and description templates are
// executed with.
type TicketData struct {
	Group
	// Alerts are the firing alerts.
	Alerts []AlertData
}

// NoteData is the data the change note title and description templates are
// executed with.
type NoteData struct {
	Group
	Fired    []AlertData
	Resolved []AlertData
	// GroupResolved is set when the whole alert group has resolved.
	GroupResolved bool
	TicketNumber  string
}

// ProblemData is the data the problem ticket title and description
// templates are executed with: the new ticket's data, plus the tickets the
// problem ticket links.
type ProblemData struct {
	TicketData
	// TicketTitle is the new ticket's title.
	TicketTitle          string
	TicketNumber         string
	PreviousTicketNumber string
}

// AlertData is an alert as ticket templates see it.
type AlertData struct {
	// Name is the alert's alertname label.
	Name         string
	Status       string
	Labels       map[string]string
	Annotations  map[string]string
	StartsAt     time.Time
	EndsAt       time.Time
	GeneratorURL string
	Fingerprint  string
}

// Templates renders ticket titles and descriptions and change note titles
// and descriptions. Blocks a templates file does not define keep their
// built-in definitions.
type Templates struct {
	// loc is the time zone times are shown in
	loc     *time.Location
	builtin *template.Template
	// custom holds the templates file's blocks over the built-in ones; nil
	// when no file was given
	custom *template.Template
}

// ParseTemplates returns the built-in templates overridden by the blocks of
// the templates file at path, if path is not empty. The time template
// function formats times in loc, default the local time zone. Every block is
// executed against a sample notification, so a template that fails is
// reported now rather than when a ticket is raised.
func ParseTemplates(path string, loc *time.Location) (*Templates, error) {
	if loc == nil {
		loc = time.Local
	}
	funcs := templateFuncs(loc)

	// a missing label or annotation is an empty string rather than "<no
	// value>", and can be given to functions
	builtin, err := template.New("default.tmpl").Option("missingkey=zero").Funcs(funcs).ParseFS(templateFS, "templates/default.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parsing built-in templates: %w", err)
	}
	t := &Templates{loc: loc, builtin: builtin}
	if path == "" {
		return t, nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading templates: %w", err)
	}
	custom, err := template.Must(builtin.Clone()).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parsing templates %s: %w", path, err)
	}
	file, err := template.New(path).Funcs(funcs).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parsing templates %s: %w", path, err)
	}
	if !slices.ContainsFunc(TemplateNames, func(name string) bool { return file.Lookup(name) != nil }) {
		return nil, fmt.Errorf("templates %s defines none of %s", path, strings.Join(TemplateNames, ", "))
	}
	t.custom = custom

	n, err := SampleNotification()
	if err != nil {
		return nil, err
	}
	data := previewData(n, custom)
	var errs []error
	for _, name := range TemplateNames {
		if _, err := execute(custom, name, data.forTemplate(name)); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("executing templates %s against a sample notification: %w", path, errors.Join(errs...))
	}

	return t, nil
}

// render executes the named template. When a block from the templates file
// fails, the built-in block is used instead and the failure is returned
// alongside its output.
func (t *Templates) render(name string, data any) (string, error) {
	if t.custom != nil {
		out, err := execute(t.custom, name, data)
		if err == nil {
			return out, nil
		}
		builtin, builtinErr := execute(t.builtin, name, data)
		return builtin, errors.Join(err, builtinErr)
	}
	return execute(t.builtin, name, data)
}

// renderBuiltin executes the built-in definition of the named template.
func (t *Templates) renderBuiltin(name string, data any) (string, error) {
	return execute(t.builtin, name, data)
}

func execute(t *template.Template, name string, data any) (string, error) {
	var b strings.Builder
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// formatTime shows t in loc, laid out as times are throughout tickets and
// notes.
func formatTime(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(defaultTimeLayout)
}

func templateFuncs(loc *time.Location) template.FuncMap {
	return template.FuncMap{
		"labels": templateLabels,
		"upper":  strings.ToUpper,
		"lower":  strings.ToLower,
		"trim":   strings.TrimSpace,
		"time": func(t time.Time, layout ...string) (string, error) {
			switch len(layout) {
			case 0:
				return formatTime(t, loc), nil
			case 1:
				return t.In(loc).Format(layout[0]), nil
			}
			return "", errors.New("time takes at most one layout")
		},
		"default": func(def string, value any) string {
			if v := reflect.ValueOf(value); value == nil || v.IsZero() {
				return def
			}
			return fmt.Sprint(value)
		},
		"truncate": func(n int, s string) string {
			if r := []rune(s); n <= len([]rune(truncatedMarker)) && len(r) > n {
				// too short for the marker
				return string(r[:max(n, 0)])
			}
			return truncate(s, n)
		},
	}
}

// templateLabels renders labels in Prometheus selector syntax, leaving out
// the names given in exclude: each a label name, or a map whose keys are
// label names.
func templateLabels(labels map[string]string, exclude ...any) (string, error) {
	skip := map[string]string{}
	for _, e := range exclude {
		switch e := e.(type) {
		case string:
			skip[e] = ""
		case map[string]string:
			maps.Copy(skip, e)
		default:
			return "", fmt.Errorf("labels cannot exclude a %T; give label names or a map of labels", e)
		}
	}
	return formatLabels(labels, skip), nil
}

// group returns the template data common to a notification's templates.
func group(n Notification, severity string) Group {
	return Group{
		Receiver:          n.Receiver,
		Status:            n.Status,
		GroupLabels:       n.GroupLabels,
		CommonLabels:      n.CommonLabels,
		CommonAnnotations: n.CommonAnnotations,
		ExternalURL:       n.ExternalURL,
		AlertmanagerURL:   alertmanagerLink(n),
		Severity:          severity,
	}
}

func alertData(alerts []Alert) []AlertData {
	out := make([]AlertData, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, AlertData{
			Name:         a.Labels["alertname"],
			Status:       a.Status,
			Labels:       a.Labels,
			Annotations:  a.Annotations,
			StartsAt:     a.StartsAt,
			EndsAt:       a.EndsAt,
			GeneratorURL: a.GeneratorURL,
			Fingerprint:  a.Fingerprint,
		})
	}
	return out
}

// SampleNotification returns the notification ticket templates are checked
// against, and previewed with when no other is given.
func SampleNotification() (Notification, error) {
	b, err := templateFS.ReadFile("templates/sample.json")
	if err != nil {
		return Notification{}, fmt.Errorf("reading sample notification: %w", err)
	}
	var n Notification
	if err := json.Unmarshal(b, &n); err != nil {
		return Notification{}, fmt.Errorf("parsing sample notification: %w", err)
	}
	return n, nil
}

// templateData holds the data for each kind of ticket template.
type templateData struct {
	ticket  TicketData
	note    NoteData
	problem ProblemData
}

// forTemplate returns the data the named template is executed with.
func (d templateData) forTemplate(name string) any {
	switch {
	case strings.HasPrefix(name, "note."):
		return d.note
	case strings.HasPrefix(name, "problem."):
		return d.problem
	}
	return d.ticket
}

// previewData is the data templates are executed with to preview a
// notification: as if it raised a ticket from its firing alerts, as if its
// firing and resolved alerts were all changes for a change note, and as if
// its ticket, titled by tmpl, were a recurrence raising a problem ticket.
func previewData(n Notification, tmpl *template.Template) templateData {
	firing := alertData(alertsWithStatus(n.Alerts, statusFiring))
	g := group(n, n.CommonLabels["severity"])
	ticket := TicketData{Group: g, Alerts: firing}
	// a failing title is reported when the title template itself is run
	title, _ := execute(tmpl, TemplateTicketTitle, ticket)
	return templateData{
		ticket: ticket,
		note: NoteData{
			Group:         g,
			Fired:         firing,
			Resolved:      alertData(alertsWithStatus(n.Alerts, statusResolved)),
			GroupResolved: n.Status == statusResolved,
			TicketNumber:  "T20261019.0001",
		},
		problem: ProblemData{
			TicketData:           ticket,
			TicketTitle:          tidy(TemplateTicketTitle, title),
			TicketNumber:         "T20261019.0002",
			PreviousTicketNumber: "T20261019.0001",
		},
	}
}

// Preview renders every ticket template for n as the receiver would, but
// without system notices, returning the first error rather than falling
// back to the built-in templates.
func (t *Templates) Preview(n Notification) (map[string]string, error) {
	tmpl := t.builtin
	if t.custom != nil {
		tmpl = t.custom
	}
	data := previewData(n, tmpl)
	out := map[string]string{}
	for _, name := range TemplateNames {
		s, err := execute(tmpl, name, data.forTemplate(name))
		if err != nil {
			return nil, err
		}
		out[name] = tidy(name, s)
	}
	return out, nil
}

// tidy cleans up rendered output: a title becomes a single line, and a
// description loses trailing whitespace.
func tidy(name string, s string) string {
	if strings.HasSuffix(name, ".title") {
		return strings.Join(strings.Fields(s), " ")
	}
	return strings.TrimRightFunc(s, unicode.IsSpace)
}
