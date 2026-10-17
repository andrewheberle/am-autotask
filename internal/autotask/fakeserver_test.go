package autotask_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	testUser   = "api@example.com"
	testSecret = "s3cret"
	testCode   = "INTEGRATION"
)

type entity map[string]any

// fakeServer emulates the parts of the Autotask REST API the client uses:
// zone discovery, entity query with filters and paging, create, patch, child
// notes and entity information.
type fakeServer struct {
	t        *testing.T
	srv      *httptest.Server
	pageSize int

	mu       sync.Mutex
	nextID   int
	entities map[string][]entity // keyed by entity name, e.g. "Tickets"
	notes    []entity
	fields   map[string][]entity // entityInformation/fields by entity
	udfs     map[string][]entity // entityInformation/userDefinedFields by entity

	// respond overrides the next response with this status and body
	respond []fakeResponse

	inflight, maxInflight atomic.Int32
	hold                  chan struct{} // when set, requests block until closed
}

type fakeResponse struct {
	status int
	body   string
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{
		t:        t,
		pageSize: 500,
		nextID:   1000,
		entities: map[string][]entity{},
		fields:   map[string][]entity{},
		udfs:     map[string][]entity{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) zoneURL() string {
	return f.srv.URL + "/atservicesrest/v1.0/zoneInformation"
}

func (f *fakeServer) add(name string, e entity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entities[name] = append(f.entities[name], e)
}

func (f *fakeServer) get(name string, id int) entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.entities[name] {
		if e["id"] == id {
			return e
		}
	}
	return nil
}

func (f *fakeServer) notesFor(ticketID int) []entity {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []entity
	for _, n := range f.notes {
		if n["ticketID"] == ticketID {
			out = append(out, n)
		}
	}
	return out
}

func (f *fakeServer) failNext(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond = append(f.respond, fakeResponse{status, body})
}

func (f *fakeServer) write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		f.t.Errorf("encoding response: %v", err)
	}
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)
	for {
		m := f.maxInflight.Load()
		if n <= m || f.maxInflight.CompareAndSwap(m, n) {
			break
		}
	}
	if f.hold != nil {
		<-f.hold
	}

	// zone discovery is unauthenticated
	if strings.EqualFold(r.URL.Path, "/atservicesrest/v1.0/zoneInformation") {
		if r.URL.Query().Get("user") != testUser {
			f.write(w, http.StatusNotFound, entity{"errors": []string{"unknown user"}})
			return
		}
		f.write(w, http.StatusOK, entity{"zoneName": "Test", "url": f.srv.URL + "/ATServicesRest/", "ci": 1})
		return
	}

	if r.Header.Get("UserName") != testUser || r.Header.Get("Secret") != testSecret || r.Header.Get("ApiIntegrationcode") != testCode {
		f.write(w, http.StatusUnauthorized, entity{"errors": []string{"unauthorized"}})
		return
	}

	f.mu.Lock()
	if len(f.respond) > 0 {
		resp := f.respond[0]
		f.respond = f.respond[1:]
		f.mu.Unlock()
		w.WriteHeader(resp.status)
		_, _ = w.Write([]byte(resp.body))
		return
	}
	f.mu.Unlock()

	path, ok := strings.CutPrefix(r.URL.Path, "/ATServicesRest/v1.0/")
	if !ok {
		f.write(w, http.StatusNotFound, entity{"errors": []string{"not found " + r.URL.Path}})
		return
	}
	parts := strings.Split(path, "/")

	switch {
	case len(parts) == 3 && parts[1] == "entityInformation" && parts[2] == "fields":
		f.write(w, http.StatusOK, entity{"fields": f.fields[parts[0]]})
	case len(parts) == 3 && parts[1] == "entityInformation" && parts[2] == "userDefinedFields":
		f.write(w, http.StatusOK, entity{"fields": f.udfs[parts[0]]})
	case len(parts) == 2 && parts[1] == "query" && r.Method == http.MethodPost:
		var q query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			f.write(w, http.StatusBadRequest, entity{"errors": []string{err.Error()}})
			return
		}
		f.query(w, parts[0], q, 0)
	case len(parts) == 3 && parts[1] == "query" && parts[2] == "next" && r.Method == http.MethodGet:
		var q query
		if err := json.Unmarshal([]byte(r.URL.Query().Get("search")), &q); err != nil {
			f.write(w, http.StatusBadRequest, entity{"errors": []string{err.Error()}})
			return
		}
		after, _ := strconv.Atoi(r.URL.Query().Get("after"))
		f.query(w, parts[0], q, after)
	case len(parts) == 2 && r.Method == http.MethodGet:
		id, _ := strconv.Atoi(parts[1])
		if e := f.get(parts[0], id); e != nil {
			f.write(w, http.StatusOK, entity{"item": e})
		} else {
			f.write(w, http.StatusOK, entity{"item": nil})
		}
	case len(parts) == 1 && r.Method == http.MethodPost:
		f.create(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPatch:
		f.patch(w, r, parts[0])
	case len(parts) == 3 && parts[0] == "Tickets" && parts[2] == "Notes" && r.Method == http.MethodPost:
		var note entity
		if err := json.NewDecoder(r.Body).Decode(&note); err != nil {
			f.write(w, http.StatusBadRequest, entity{"errors": []string{err.Error()}})
			return
		}
		id, _ := strconv.Atoi(parts[1])
		note["ticketID"] = id
		f.mu.Lock()
		f.nextID++
		note["id"] = f.nextID
		f.notes = append(f.notes, note)
		f.mu.Unlock()
		f.write(w, http.StatusOK, entity{"itemId": note["id"]})
	default:
		f.write(w, http.StatusNotFound, entity{"errors": []string{"not found " + r.Method + " " + r.URL.Path}})
	}
}

type query struct {
	IncludeFields []string `json:"IncludeFields,omitempty"`
	Filter        []filter `json:"filter"`
}

type filter struct {
	Op    string `json:"op"`
	Field string `json:"field"`
	UDF   bool   `json:"udf"`
	Value any    `json:"value"`
}

func (f *fakeServer) query(w http.ResponseWriter, name string, q query, after int) {
	f.mu.Lock()
	var matched []entity
	for _, e := range f.entities[name] {
		if e["id"].(int) > after && matchesAll(e, q.Filter) {
			matched = append(matched, e)
		}
	}
	f.mu.Unlock()
	slices.SortFunc(matched, func(a, b entity) int { return a["id"].(int) - b["id"].(int) })

	var next any
	if len(matched) > f.pageSize {
		matched = matched[:f.pageSize]
		search, _ := json.Marshal(q)
		next = fmt.Sprintf("%s/ATServicesRest/v1.0/%s/query/next?search=%s&after=%d",
			f.srv.URL, name, url.QueryEscape(string(search)), matched[len(matched)-1]["id"])
	}
	if matched == nil {
		matched = []entity{}
	}
	matched = project(matched, q.IncludeFields)

	f.write(w, http.StatusOK, entity{
		"items":       matched,
		"pageDetails": entity{"count": len(matched), "requestCount": f.pageSize, "prevPageUrl": nil, "nextPageUrl": next},
	})
}

func matchesAll(e entity, filters []filter) bool {
	for _, flt := range filters {
		v, present := fieldValue(e, flt)
		switch flt.Op {
		case "eq":
			if !present || !equal(v, flt.Value) {
				return false
			}
		case "noteq":
			if present && equal(v, flt.Value) {
				return false
			}
		case "exist":
			if !present || v == nil || v == "" {
				return false
			}
		case "beginsWith":
			s, _ := v.(string)
			if !present || !strings.HasPrefix(strings.ToLower(s), strings.ToLower(fmt.Sprint(flt.Value))) {
				return false
			}
		default:
			panic("fake server does not support op " + flt.Op)
		}
	}
	return true
}

// project keeps only the included fields, as Autotask does when
// IncludeFields is set; UDFs are returned only if named there too.
func project(items []entity, include []string) []entity {
	if len(include) == 0 {
		return items
	}

	out := make([]entity, 0, len(items))
	for _, e := range items {
		p := entity{"userDefinedFields": []entity{}}
		for _, name := range include {
			if v, ok := e[name]; ok {
				p[name] = v
			}
		}
		udfs, _ := e["userDefinedFields"].([]entity)
		for _, u := range udfs {
			if slices.Contains(include, u["name"].(string)) {
				p["userDefinedFields"] = append(p["userDefinedFields"].([]entity), u)
			}
		}
		out = append(out, p)
	}
	return out
}

func fieldValue(e entity, flt filter) (any, bool) {
	if !flt.UDF {
		v, ok := e[flt.Field]
		return v, ok
	}
	udfs, _ := e["userDefinedFields"].([]entity)
	for _, u := range udfs {
		if u["name"] == flt.Field {
			return u["value"], true
		}
	}
	return nil, false
}

func equal(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func (f *fakeServer) create(w http.ResponseWriter, r *http.Request, name string) {
	var e entity
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		f.write(w, http.StatusBadRequest, entity{"errors": []string{err.Error()}})
		return
	}
	normalise(e)

	f.mu.Lock()
	f.nextID++
	e["id"] = f.nextID
	e["ticketNumber"] = fmt.Sprintf("T20261017.%04d", f.nextID-1000)
	f.entities[name] = append(f.entities[name], e)
	f.mu.Unlock()

	f.write(w, http.StatusOK, entity{"itemId": e["id"]})
}

func (f *fakeServer) patch(w http.ResponseWriter, r *http.Request, name string) {
	var changes entity
	if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
		f.write(w, http.StatusBadRequest, entity{"errors": []string{err.Error()}})
		return
	}
	normalise(changes)

	id, _ := changes["id"].(int)
	e := f.get(name, id)
	if e == nil {
		f.write(w, http.StatusNotFound, entity{"errors": []string{"no such entity"}})
		return
	}

	f.mu.Lock()
	for k, v := range changes {
		if k != "userDefinedFields" {
			e[k] = v
			continue
		}
		// UDFs are merged by name, as Autotask does
		existing, _ := e["userDefinedFields"].([]entity)
		for _, u := range v.([]entity) {
			i := slices.IndexFunc(existing, func(x entity) bool { return x["name"] == u["name"] })
			if i >= 0 {
				existing[i] = u
			} else {
				existing = append(existing, u)
			}
		}
		e["userDefinedFields"] = existing
	}
	f.mu.Unlock()

	f.write(w, http.StatusOK, entity{"itemId": id})
}

// normalise converts decoded JSON numbers to int and UDF lists to []entity
// so stored entities compare simply.
func normalise(e entity) {
	for k, v := range e {
		switch vv := v.(type) {
		case float64:
			e[k] = int(vv)
		case []any:
			if k == "userDefinedFields" {
				udfs := make([]entity, 0, len(vv))
				for _, u := range vv {
					udfs = append(udfs, entity(u.(map[string]any)))
				}
				e[k] = udfs
			}
		}
	}
}

// udf returns the value of a UDF on a stored entity.
func udf(e entity, name string) any {
	v, _ := fieldValue(e, filter{Field: name, UDF: true})
	return v
}

func picklist(name string, values ...entity) entity {
	return entity{"name": name, "isPickList": true, "picklistValues": values}
}

func pv(value int, label string, active bool) entity {
	return entity{"value": strconv.Itoa(value), "label": label, "isActive": active}
}
