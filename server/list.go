// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// list builds the page of the list node the request asks for: the items
// the client may read, in the order of the list, from s, at most l of them.
func (s *Server) list(ctx context.Context, req *Request) (any, error) {
	return s.listOf(ctx, req, req.Node.Href, nil)
}

// listOf builds the page of the list document of req.Node from the items
// of the list at source that keep accepts (all when nil).
func (s *Server) listOf(ctx context.Context, req *Request, source string, keep func(*Node) bool) (any, error) {
	members, err := s.store.Members(ctx, source)
	if err != nil && !errors.Is(err, ErrNoNode) {
		return nil, err
	}
	var items []*Node
	for _, m := range members {
		if m.Value != nil && s.canRead(req.Principal, m) && (keep == nil || keep(m)) {
			items = append(items, m)
		}
	}
	if req.Query.HasSFDI {
		items = filterSFDI(items, req.Query.SFDI)
	}
	sortItems(req.Node.Resource, items)
	if req.Query.HasAfter {
		items = filterAfter(items, req.Query.After)
	}
	limit := s.cfg.DefaultLimit
	if req.Query.HasLimit {
		limit = req.Query.Limit
	}
	if limit > s.cfg.MaxLimit {
		limit = s.cfg.MaxLimit
	}
	all := len(items)
	start := int(req.Query.Start)
	if start > all {
		start = all
	}
	end := start + int(limit)
	if end > all {
		end = all
	}
	doc := s.profile.Clone(req.Node.Value)
	l, ok := doc.(ieee20305.Lister)
	if !ok {
		return nil, httpError(http.StatusInternalServerError, "%s is not a list", req.Node.Href)
	}
	page := make([]any, 0, end-start)
	for _, n := range items[start:end] {
		page = append(page, n.Value)
	}
	if err := l.SetListItems(page); err != nil {
		return nil, httpError(http.StatusInternalServerError, "%s: %v", req.Node.Href, err)
	}
	l.SetListCounts(uint32(all), uint32(len(page)))
	setHref(doc, req.Node.Href)
	return doc, nil
}

// filterSFDI keeps the items whose sFDI is sfdi.
func filterSFDI(items []*Node, sfdi uint64) []*Node {
	var out []*Node
	for _, n := range items {
		if v, ok := fieldUint(n.Value, "SFDI"); ok && v == sfdi {
			out = append(out, n)
		}
	}
	return out
}

// filterAfter keeps the events that start at or after a time, and the
// items that are no events.
func filterAfter(items []*Node, after int64) []*Node {
	var out []*Node
	for _, n := range items {
		if start, ok := eventStart(n.Value); !ok || start >= after {
			out = append(out, n)
		}
	}
	return out
}

// sortItems orders the items of a list as IEEE 2030.5 orders lists:
// events by start time then most recent creation; EndDevices by sFDI;
// readings and log events most recent first; others as they were added.
func sortItems(list string, items []*Node) {
	switch {
	case strings.HasSuffix(list, "EndDeviceList"):
		sort.SliceStable(items, func(i, j int) bool {
			a, _ := fieldUint(items[i].Value, "SFDI")
			b, _ := fieldUint(items[j].Value, "SFDI")
			return a < b
		})
	case anyEvent(items):
		sort.SliceStable(items, func(i, j int) bool {
			a, _ := eventStart(items[i].Value)
			b, _ := eventStart(items[j].Value)
			if a != b {
				return a < b
			}
			return fieldInt(items[i].Value, "CreationTime") > fieldInt(items[j].Value, "CreationTime")
		})
	case strings.HasSuffix(list, "LogEventList"):
		sort.SliceStable(items, func(i, j int) bool {
			return fieldInt(items[i].Value, "CreatedDateTime") > fieldInt(items[j].Value, "CreatedDateTime")
		})
	}
}

func anyEvent(items []*Node) bool {
	for _, n := range items {
		if _, ok := eventStart(n.Value); ok {
			return true
		}
	}
	return false
}

// eventStart returns the interval start of an event (a type that embeds
// ieee20305.Event).
func eventStart(v any) (int64, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0, false
	}
	f := rv.Elem().FieldByName("Interval")
	if !f.IsValid() || f.Type() != reflect.TypeOf(ieee20305.DateTimeInterval{}) {
		return 0, false
	}
	return int64(f.Interface().(ieee20305.DateTimeInterval).Start), true
}

// fieldUint returns an unsigned integer field of the document v, followed
// through embedding.
func fieldUint(v any, name string) (uint64, bool) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0, false
	}
	f := rv.Elem().FieldByName(name)
	if !f.IsValid() {
		return 0, false
	}
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return f.Uint(), true
	default:
		return 0, false
	}
}

// fieldInt returns an integer field of the document v, or 0.
func fieldInt(v any, name string) int64 {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return 0
	}
	f := rv.Elem().FieldByName(name)
	switch {
	case !f.IsValid():
		return 0
	case f.Kind() == reflect.Int64 || f.Kind() == reflect.Int32 || f.Kind() == reflect.Int:
		return f.Int()
	}
	return 0
}

// setHref sets the href of a document (the attribute of Resource).
func setHref(v any, href string) {
	if h, ok := v.(ieee20305.Hrefer); ok {
		h.SetHref(href)
		return
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return
	}
	f := rv.Elem().FieldByName("Href")
	switch {
	case !f.IsValid() || !f.CanSet():
	case f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.String:
		f.Set(reflect.ValueOf(&href))
	case f.Kind() == reflect.String:
		f.SetString(href)
	}
}

// newHref allocates the href of a new resource under parent: parent/<n>
// with the next number.
func (s *Server) newHref(parent string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.counters[parent]; !ok {
		s.counters[parent] = s.maxChild(parent)
	}
	s.counters[parent]++
	return path.Join(parent, strconv.Itoa(s.counters[parent]))
}

// maxChild returns the largest number among the hrefs parent/<n> and
// parent/<n>/... the store holds, so that hrefs stay unique after a restart
// with a persistent store.
func (s *Server) maxChild(parent string) int {
	hrefs, err := s.store.Hrefs(context.Background())
	if err != nil {
		return 0
	}
	max := 0
	prefix := strings.TrimSuffix(parent, "/") + "/"
	for _, h := range hrefs {
		rest, ok := strings.CutPrefix(h, prefix)
		if !ok {
			continue
		}
		rest, _, _ = strings.Cut(rest, "/")
		if n, err := strconv.Atoi(rest); err == nil && n > max {
			max = n
		}
	}
	return max
}
