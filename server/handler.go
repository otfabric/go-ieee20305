// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"

	ieee20305 "github.com/otfabric/go-ieee20305"
)

// MediaType is the media type of IEEE 2030.5 XML documents.
const MediaType = "application/sep+xml"

// Query is the paging and selection of a request, from the parameters its
// method takes in the WADL.
type Query struct {
	// Start (s) is the index of the first item of a list page; Limit (l) is
	// the number of items. HasLimit reports whether l was given.
	Start, Limit uint32
	HasLimit     bool
	// After (a) selects the events of a list that start after a time,
	// HasAfter whether it was given.
	After    int64
	HasAfter bool
	// SFDI (sFDI) selects an EndDevice by its SFDI, HasSFDI whether it was
	// given.
	SFDI    uint64
	HasSFDI bool
}

func parseQuery(r *http.Request, allowed []string) (Query, error) {
	var q Query
	values := r.URL.Query()
	for _, name := range allowed {
		raw, ok := values[name]
		if !ok || len(raw) == 0 {
			continue
		}
		if len(raw) > 1 {
			return q, httpError(http.StatusBadRequest, "query parameter %s given %d times", name, len(raw))
		}
		v := raw[0]
		var err error
		switch name {
		case "s":
			var n uint64
			n, err = strconv.ParseUint(v, 10, 32)
			q.Start = uint32(n)
		case "l":
			var n uint64
			n, err = strconv.ParseUint(v, 10, 32)
			q.Limit, q.HasLimit = uint32(n), true
		case "a":
			q.After, err = strconv.ParseInt(v, 10, 64)
			q.HasAfter = true
		case "sFDI":
			q.SFDI, err = strconv.ParseUint(v, 10, 64)
			q.HasSFDI = true
		}
		if err != nil {
			return q, httpError(http.StatusBadRequest, "query parameter %s=%q: %v", name, v, err)
		}
	}
	return q, nil
}

// ServeHTTP answers an IEEE 2030.5 request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if s.isClosed() {
		http.Error(w, "server closed", http.StatusServiceUnavailable)
		return
	}
	p, err := s.principal(r)
	if err != nil {
		s.fail(w, Principal{Edition: s.cfg.DefaultEdition}, err)
		return
	}
	if err := s.serve(ctx, w, r, p); err != nil {
		s.fail(w, p, err)
	}
}

func (s *Server) serve(ctx context.Context, w http.ResponseWriter, r *http.Request, p Principal) error {
	node, err := s.store.Get(ctx, r.URL.Path)
	if errors.Is(err, ErrNoNode) {
		return httpError(http.StatusNotFound, "%s", r.URL.Path)
	}
	if err != nil {
		return err
	}
	spec, ok := s.resources[node.Resource]
	if !ok {
		return httpError(http.StatusInternalServerError, "resource %s of %s is unknown", node.Resource, node.Href)
	}
	if !ieee20305.InEdition(node.Resource, p.Edition) && !s.profileResource(node.Resource) {
		return httpError(http.StatusNotFound, "%s is no resource of IEEE 2030.5-%v", r.URL.Path, p.Edition)
	}
	if !s.canRead(p, node) {
		return httpError(http.StatusNotFound, "%s", r.URL.Path)
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	ms, ok := s.method(spec, method)
	if !ok {
		w.Header()["Allow"] = []string{strings.Join(s.allowed(spec), ", ")}
		return httpError(http.StatusMethodNotAllowed, "%s %s", r.Method, r.URL.Path)
	}
	q, err := parseQuery(r, ms.QueryParams)
	if err != nil {
		return err
	}
	req := &Request{Request: r, Principal: p, Node: node, Spec: spec, Method: ms, Query: q}
	hooks := s.hooks[node.Resource]
	switch method {
	case http.MethodGet:
		if accept := r.Header.Get("Accept"); accept != "" && !acceptsXML(accept) {
			return httpError(http.StatusNotAcceptable, "Accept %q: the server sends %s", accept, MediaType)
		}
		return s.get(ctx, w, req, hooks, r.Method == http.MethodHead)
	case http.MethodPut, http.MethodPost:
		if err := s.decodeBody(w, req); err != nil {
			return err
		}
		if method == http.MethodPut {
			return s.put(ctx, w, req, hooks)
		}
		return s.post(ctx, w, req, hooks)
	case http.MethodDelete:
		if hooks.Delete != nil {
			if err := hooks.Delete(ctx, req); err != nil {
				return err
			}
		} else {
			if !s.canChange(p, node) {
				return httpError(http.StatusForbidden, "%s is not the client's", node.Href)
			}
			if err := s.store.Delete(ctx, node.Href); err != nil {
				return err
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	return httpError(http.StatusMethodNotAllowed, "%s", r.Method)
}

func (s *Server) profileResource(id string) bool {
	for _, r := range s.profile.Resources {
		if r.ID == id {
			return true
		}
	}
	return false
}

// method returns the method spec of a method the server supports on spec.
func (s *Server) method(spec ieee20305.ResourceSpec, name string) (ieee20305.MethodSpec, bool) {
	for _, d := range s.cfg.Disable[spec.ID] {
		if strings.EqualFold(d, name) {
			return ieee20305.MethodSpec{}, false
		}
	}
	for _, m := range spec.Methods {
		if m.Method != name {
			continue
		}
		switch m.Conformance {
		case ieee20305.ConformanceMandatory, ieee20305.ConformanceOptional:
			return m, true
		case ieee20305.ConformanceDiscouraged:
			for _, e := range s.cfg.EnableDiscouraged[spec.ID] {
				if strings.EqualFold(e, name) {
					return m, true
				}
			}
		case ieee20305.ConformanceError:
		}
		return ieee20305.MethodSpec{}, false
	}
	return ieee20305.MethodSpec{}, false
}

// allowed returns the methods the server supports on spec, for Allow.
func (s *Server) allowed(spec ieee20305.ResourceSpec) []string {
	var out []string
	for _, m := range spec.Methods {
		if _, ok := s.method(spec, m.Method); ok {
			out = append(out, m.Method)
		}
	}
	sort.Strings(out)
	return out
}

func acceptsXML(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mt, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		switch mt {
		case MediaType, "application/xml", "text/xml", "*/*", "application/*":
			return true
		}
	}
	return false
}

// decodeBody decodes and validates the document of a PUT or POST.
func (s *Server) decodeBody(w http.ResponseWriter, req *Request) error {
	if ct := req.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || (mt != MediaType && mt != "application/xml" && mt != "text/xml") {
			return httpError(http.StatusUnsupportedMediaType, "Content-Type %q: the server takes %s", ct, MediaType)
		}
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, s.cfg.MaxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return httpError(http.StatusRequestEntityTooLarge, "request body over %d bytes", s.cfg.MaxRequestBytes)
		}
		return httpError(http.StatusBadRequest, "reading the body: %v", err)
	}
	v, err := s.profile.Codec(req.Principal.Edition).Decode(data)
	if err != nil {
		return &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest, Err: err}
	}
	n, _ := s.profile.ElementName(v)
	name := n.Local
	if req.Method.Request != "" && !s.profile.Derives(name, req.Method.Request) {
		return &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest,
			Err: errors.New(name + " where the resource takes " + req.Method.Request)}
	}
	if vv, ok := v.(interface{ Validate() error }); ok {
		if err := vv.Validate(); err != nil {
			return &HTTPError{Status: http.StatusBadRequest, Reason: ReasonInvalidRequest, Err: err}
		}
	}
	req.Doc = v
	return nil
}

func (s *Server) get(ctx context.Context, w http.ResponseWriter, req *Request, hooks Hooks, head bool) error {
	var doc any
	var err error
	switch {
	case hooks.Get != nil:
		doc, err = hooks.Get(ctx, req)
	case req.Node.Value == nil:
		return httpError(http.StatusNotFound, "%s does not exist yet", req.Node.Href)
	case isList(req.Node.Value):
		doc, err = s.list(ctx, req)
	default:
		doc = req.Node.Value
	}
	if err != nil {
		return err
	}
	return s.respond(w, req.Principal, http.StatusOK, doc, head)
}

func (s *Server) put(ctx context.Context, w http.ResponseWriter, req *Request, hooks Hooks) error {
	created := req.Node.Value == nil
	if hooks.Update != nil {
		if err := hooks.Update(ctx, req); err != nil {
			return err
		}
	} else if err := s.replace(ctx, req); err != nil {
		return err
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
	return nil
}

// replace is the default PUT: the client replaces a document it may change.
// The links of the document are the server's: those of the stored document
// stay, except for an operator.
func (s *Server) replace(ctx context.Context, req *Request) error {
	if !s.canChange(req.Principal, req.Node) {
		return httpError(http.StatusForbidden, "%s is not the client's", req.Node.Href)
	}
	if req.Principal.Role != RoleOperator && req.Node.Value != nil {
		keepLinks(req.Doc, req.Node.Value)
	}
	setHref(req.Doc, req.Node.Href)
	n := *req.Node
	n.Value, n.Modified = req.Doc, s.clock()
	return s.store.Put(ctx, &n)
}

func (s *Server) post(ctx context.Context, w http.ResponseWriter, req *Request, hooks Hooks) error {
	var href string
	var err error
	switch {
	case hooks.Create != nil:
		href, err = hooks.Create(ctx, req)
	case isList(req.Node.Value):
		href, err = s.createDefault(ctx, req)
	default:
		return httpError(http.StatusMethodNotAllowed, "POST to %s", req.Node.Href)
	}
	if err != nil {
		return err
	}
	if href == "" {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	w.Header().Set("Location", href)
	w.WriteHeader(http.StatusCreated)
	return nil
}

// createItem stores v as a new item of the list node, owned by owner, and
// returns its href.
func (s *Server) createItem(ctx context.Context, list *Node, v any, owner string) (string, error) {
	href := s.newHref(list.Href)
	setHref(v, href)
	n := &Node{Href: href, Resource: itemResource(list.Resource), Value: v, Owner: owner, Parent: list.Href, Modified: s.clock()}
	if err := s.store.Put(ctx, n); err != nil {
		return "", err
	}
	if err := s.store.AddMember(ctx, list.Href, href); err != nil {
		return "", err
	}
	return href, nil
}

// respond writes doc in the edition of p.
func (s *Server) respond(w http.ResponseWriter, p Principal, status int, doc any, head bool) error {
	out, err := s.profile.Codec(p.Edition).Marshal(doc)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(status)
	if !head {
		_, _ = w.Write(out)
	}
	return nil
}

// fail answers with the status of err: an Error document for a 400.
func (s *Server) fail(w http.ResponseWriter, p Principal, err error) {
	var he *HTTPError
	if !errors.As(err, &he) {
		s.log.Error("request failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if he.Status >= 500 {
		s.log.Error("request failed", "err", err)
	}
	if he.Status == http.StatusBadRequest {
		doc := &ieee20305.Error{ReasonCode: he.Reason}
		if out, merr := s.profile.Codec(p.Edition).Marshal(doc); merr == nil {
			w.Header().Set("Content-Type", MediaType)
			w.WriteHeader(he.Status)
			_, _ = w.Write(out)
			return
		}
	}
	w.WriteHeader(he.Status)
}

// isList reports whether a document is a list.
func isList(v any) bool {
	_, ok := v.(ieee20305.Lister)
	return ok
}

// itemResource returns the WADL resource of the items of a list resource:
// EndDevice for EndDeviceList.
func itemResource(list string) string { return strings.TrimSuffix(list, "List") }
