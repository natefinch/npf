// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// The router package implements an HTTP request router for charm store
// HTTP requests.
package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/juju/utils/jsonhttp"
	"github.com/juju/utils/parallel"
	"gopkg.in/errgo.v1"
	charm "gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/params"
)

// Implementation note on error handling:
//
// We use errgo.Any only when necessary, so that we can see at a glance
// which are the possible places that could be returning an error with a
// Cause (the only kind of error that can end up setting an HTTP status
// code)

var knownSeries = map[string]bool{
	"bundle":  true,
	"oneiric": true,
	"precise": true,
	"quantal": true,
	"raring":  true,
	"saucy":   true,
	"trusty":  true,
	"utopic":  true,
	"vivid":   true,
}

// BulkIncludeHandler represents a metadata handler that can
// handle multiple metadata "include" requests in a single batch.
//
// For simple metadata handlers that cannot be
// efficiently combined, see SingleIncludeHandler.
//
// All handlers may assume that http.Request.ParseForm
// has been called to parse the URL form values.
type BulkIncludeHandler interface {
	// Key returns a value that will be used to group handlers
	// together in preparation for a call to HandleGet or HandlePut.
	// The key should be comparable for equality.
	// Please do not return NaN. That would be silly, OK?
	Key() interface{}

	// HandleGet returns the results of invoking all the given handlers
	// on the given charm or bundle id. Each result is held in
	// the respective element of the returned slice.
	//
	// All of the handlers' Keys will be equal to the receiving handler's
	// Key.
	//
	// Each item in paths holds the remaining metadata path
	// for the handler in the corresponding position
	// in hs after the prefix in Handlers.Meta has been stripped,
	// and flags holds all the URL query values.
	//
	// TODO(rog) document indexed errors.
	HandleGet(hs []BulkIncludeHandler, id *charm.Reference, paths []string, flags url.Values, req *http.Request) ([]interface{}, error)

	// HandlePut invokes a PUT request on all the given handlers on
	// the given charm or bundle id. If there is an error, the
	// returned errors slice should contain one element for each element
	// in paths. The error for handler hs[i] should be returned in errors[i].
	// If there is no error, an empty slice should be returned.
	//
	// Each item in paths holds the remaining metadata path
	// for the handler in the corresponding position
	// in hs after the prefix in Handlers.Meta has been stripped,
	// and flags holds all the url query values.
	HandlePut(hs []BulkIncludeHandler, id *charm.Reference, paths []string, values []*json.RawMessage, req *http.Request) []error
}

// IdHandler handles a charm store request rooted at the given id.
// The request path (req.URL.Path) holds the URL path after
// the id has been stripped off.
// The fullySpecified parameter holds whether the charm id was
// fully specified in the original client request.
type IdHandler func(charmId *charm.Reference, fullySpecified bool, w http.ResponseWriter, req *http.Request) error

// Handlers specifies how HTTP requests will be routed
// by the router. All errors returned by the handlers will
// be processed by WriteError with their Cause left intact.
// This means that, for example, if they return an error
// with a Cause that is params.ErrNotFound, the HTTP
// status code will reflect that (assuming the error has
// not been absorbed by the bulk metadata logic).
type Handlers struct {
	// Global holds handlers for paths not matched by Meta or Id.
	// The map key is the path; the value is the handler that will
	// be used to handle that path.
	//
	// Path matching is by matched by longest-prefix - the same as
	// http.ServeMux.
	//
	// Note that, unlike http.ServeMux, the prefix is stripped
	// from the URL path before the hander is invoked,
	// matching the behaviour of the other handlers.
	Global map[string]http.Handler

	// Id holds handlers for paths which correspond to a single
	// charm or bundle id other than the meta path. The map key
	// holds the first element of the path, which may end in a
	// trailing slash (/) to indicate that longer paths are allowed
	// too.
	Id map[string]IdHandler

	// Meta holds metadata handlers for paths under the meta
	// endpoint. The map key holds the first element of the path,
	// which may end in a trailing slash (/) to indicate that longer
	// paths are allowed too.
	Meta map[string]BulkIncludeHandler
}

// Router represents a charm store HTTP request router.
type Router struct {
	handlers   *Handlers
	handler    http.Handler
	resolveURL func(id *charm.Reference) error
	authorize  func(id *charm.Reference, req *http.Request) error
	exists     func(id *charm.Reference, req *http.Request) (bool, error)
}

// New returns a charm store router that will route requests to
// the given handlers and retrieve metadata from the given database.
//
// The resolveURL function will be called to resolve ids in
// router paths - it should fill in the Series and Revision
// fields of its argument URL if they are not specified.
// The Cause of the resolveURL error will be left unchanged,
// as for the handlers.
//
// The authorize function will be called to authorize the request.
// The Cause of the authorize error will be left unchanged,
// as for the handlers.
//
// The exists function may be called to test whether an entity
// exists when an API endpoint needs to know that
// but has no appropriate handler to call.
func New(
	handlers *Handlers,
	resolveURL func(id *charm.Reference) error,
	authorize func(id *charm.Reference, req *http.Request) error,
	exists func(id *charm.Reference, req *http.Request) (bool, error),
) *Router {
	r := &Router{
		handlers:   handlers,
		resolveURL: resolveURL,
		authorize:  authorize,
		exists:     exists,
	}
	mux := NewServeMux()
	mux.Handle("/meta/", http.StripPrefix("/meta", HandleErrors(r.serveBulkMeta)))
	for path, handler := range r.handlers.Global {
		path = "/" + path
		prefix := strings.TrimSuffix(path, "/")
		mux.Handle(path, http.StripPrefix(prefix, handler))
	}
	mux.Handle("/", HandleErrors(r.serveIds))
	r.handler = mux
	return r
}

// ServeHTTP implements http.Handler.ServeHTTP.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Allow cross-domain access from anywhere, including AJAX
	// requests. An AJAX request will add an X-Requested-With:
	// XMLHttpRequest header, which is a non-standard header, and
	// hence will require a pre-flight request, so we need to
	// specify that that header is allowed, and we also need to
	// implement the OPTIONS method so that the pre-flight request
	// can work.
	// See https://developer.mozilla.org/en-US/docs/Web/HTTP/Access_control_CORS
	header := w.Header()
	header.Set("Access-Control-Allow-Origin", "*")
	header.Set("Access-Control-Allow-Headers", "X-Requested-With")

	if req.Method == "OPTIONS" {
		// We cheat here and say that all methods are allowed,
		// even though any individual endpoint will allow
		// only a subset of these. This means we can avoid
		// putting OPTIONS handling in every endpoint,
		// and it shouldn't actually matter in practice.
		header.Set("Allow", "DELETE,GET,HEAD,PUT,POST")
		return
	}
	if err := req.ParseForm(); err != nil {
		WriteError(w, errgo.Notef(err, "cannot parse form"))
		return
	}
	r.handler.ServeHTTP(w, req)
}

// Handlers returns the set of handlers that the router was created with.
// This should not be changed.
func (r *Router) Handlers() *Handlers {
	return r.handlers
}

// serveIds serves requests that may be rooted at a charm or bundle id.
func (r *Router) serveIds(w http.ResponseWriter, req *http.Request) error {
	// We can ignore a trailing / because we do not return any
	// relative URLs. If we start to return relative URL redirects,
	// we will need to redirect non-slash-terminated URLs
	// to slash-terminated URLs.
	// http://cdivilly.wordpress.com/2014/03/11/why-trailing-slashes-on-uris-are-important/
	path := strings.TrimSuffix(req.URL.Path, "/")
	url, path, err := splitId(path)
	if err != nil {
		return errgo.Mask(err)
	}
	key, path := handlerKey(path)
	if key == "" {
		return errgo.WithCausef(nil, params.ErrNotFound, "")
	}
	fullySpecified := url.Series != "" && url.Revision != -1
	handler := r.handlers.Id[key]
	if handler == nil || idHandlerNeedsResolveURL(req) {
		// If it's not an id handler, it's a meta endpoint, so
		// we always want a resolved URL. Otherwise we leave the
		// URL unresolved for cases where the id may validly not
		// exist (for example when uploading a new charm).
		if err := r.resolveURL(url); err != nil {
			// Note: preserve error cause from resolveURL.
			return errgo.Mask(err, errgo.Any)
		}
	}
	if handler != nil {
		req.URL.Path = path
		if err := r.authorize(url, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		err := handler(url, fullySpecified, w, req)
		// Note: preserve error cause from handlers.
		return errgo.Mask(err, errgo.Any)
	}
	if key != "meta/" && key != "meta" {
		return errgo.WithCausef(nil, params.ErrNotFound, params.ErrNotFound.Error())
	}
	req.URL.Path = path
	return r.serveMeta(url, w, req)
}

func idHandlerNeedsResolveURL(req *http.Request) bool {
	return req.Method != "POST" && req.Method != "PUT"
}

// handlerKey returns a key that can be used to look up a handler at the
// given path, and the remaining path elements. If there is no possible
// key, the returned key is empty.
func handlerKey(path string) (key, rest string) {
	path = strings.TrimPrefix(path, "/")
	key, i := splitPath(path, 0)
	if key == "" {
		// TODO what *should* we get if we GET just an id?
		return "", rest
	}
	if i < len(path)-1 {
		// There are more elements, so include the / character
		// that terminates the element.
		return path[0 : i+1], path[i:]
	}
	return key, ""
}

func (r *Router) serveMeta(id *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	switch req.Method {
	case "GET", "HEAD":
		resp, err := r.serveMetaGet(id, req)
		if err != nil {
			// Note: preserve error causes from meta handlers.
			return errgo.Mask(err, errgo.Any)
		}
		jsonhttp.WriteJSON(w, http.StatusOK, resp)
		return nil
	case "PUT":
		// Put requests don't return any data unless there's
		// an error.
		return r.serveMetaPut(id, req)
	}
	return params.ErrMethodNotAllowed
}

func (r *Router) serveMetaGet(id *charm.Reference, req *http.Request) (interface{}, error) {
	if err := r.authorize(id, req); err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	key, path := handlerKey(req.URL.Path)
	if key == "" {
		// GET id/meta
		// http://tinyurl.com/nysdjly
		return r.metaNames(), nil
	}
	if key == "any" {
		// GET id/meta/any?[include=meta[&include=meta...]]
		// http://tinyurl.com/q5vcjpk
		includes := req.Form["include"]
		// If there are no includes, we have no handlers to generate
		// a "not found" error when the id doesn't exist, so we need
		// to check explicitly.
		if len(includes) == 0 {
			exists, err := r.exists(id, req)
			if err != nil {
				return nil, errgo.Notef(err, "cannot determine existence of %q", id)
			}
			if !exists {
				return nil, errgo.WithCausef(nil, params.ErrNotFound, "")
			}
			return params.MetaAnyResponse{Id: id}, nil
		}
		meta, err := r.GetMetadata(id, includes, req)
		if err != nil {
			// Note: preserve error cause from handlers.
			return nil, errgo.Mask(err, errgo.Any)
		}
		return params.MetaAnyResponse{
			Id:   id,
			Meta: meta,
		}, nil
	}
	if handler := r.handlers.Meta[key]; handler != nil {
		results, err := handler.HandleGet([]BulkIncludeHandler{handler}, id, []string{path}, req.Form, req)
		if err != nil {
			// Note: preserve error cause from handlers.
			return nil, errgo.Mask(err, errgo.Any)
		}
		result := results[0]
		if isNull(result) {
			return nil, params.ErrMetadataNotFound
		}
		return results[0], nil
	}
	return nil, errgo.WithCausef(nil, params.ErrNotFound, "unknown metadata %q", strings.TrimPrefix(req.URL.Path, "/"))
}

const jsonContentType = "application/json"

func unmarshalJSONBody(req *http.Request, val interface{}) error {
	if ct := req.Header.Get("Content-Type"); ct != jsonContentType {
		return errgo.WithCausef(nil, params.ErrBadRequest, "unexpected Content-Type %q; expected %q", ct, jsonContentType)
	}
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(val); err != nil {
		return errgo.Notef(err, "cannot unmarshal body")
	}
	return nil
}

// serveMetaPut serves a PUT request to the metadata for the given id.
// The metadata to be put is in the request body.
// PUT /$id/meta/...
func (r *Router) serveMetaPut(id *charm.Reference, req *http.Request) error {
	if err := r.authorize(id, req); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	var body json.RawMessage
	if err := unmarshalJSONBody(req, &body); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrBadRequest))
	}
	return r.serveMetaPutBody(id, req, &body)
}

// serveMetaPutBody serves a PUT request to the metadata for the given id.
// The metadata to be put is in body.
// This method is used both for individual metadata PUTs and
// also bulk metadata PUTs.
func (r *Router) serveMetaPutBody(id *charm.Reference, req *http.Request, body *json.RawMessage) error {
	key, path := handlerKey(req.URL.Path)
	if key == "" {
		return params.ErrForbidden
	}
	if key == "any" {
		// PUT id/meta/any
		var bodyMeta struct {
			Meta map[string]*json.RawMessage
		}
		if err := json.Unmarshal(*body, &bodyMeta); err != nil {
			return errgo.Notef(err, "cannot unmarshal body")
		}
		if err := r.PutMetadata(id, bodyMeta.Meta, req); err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		return nil
	}
	if handler := r.handlers.Meta[key]; handler != nil {
		errs := handler.HandlePut(
			[]BulkIncludeHandler{handler},
			id,
			[]string{path},
			[]*json.RawMessage{body},
			req,
		)
		if len(errs) > 0 && errs[0] != nil {
			// Note: preserve error cause from handlers.
			return errgo.Mask(errs[0], errgo.Any)
		}
		return nil
	}
	return errgo.WithCausef(nil, params.ErrNotFound, "")
}

// isNull reports whether the given value will encode to
// a null JSON value.
func isNull(val interface{}) bool {
	if val == nil {
		return true
	}
	v := reflect.ValueOf(val)
	if kind := v.Kind(); kind != reflect.Map && kind != reflect.Ptr && kind != reflect.Slice {
		return false
	}
	return v.IsNil()
}

// metaNames returns a slice of all the metadata endpoint names.
func (r *Router) metaNames() []string {
	names := make([]string, 0, len(r.handlers.Meta))
	for name := range r.handlers.Meta {
		// Ensure that we don't generate duplicate entries
		// when there's an entry for both "x" and "x/".
		trimmed := strings.TrimSuffix(name, "/")
		if trimmed != name && r.handlers.Meta[trimmed] != nil {
			continue
		}
		names = append(names, trimmed)
	}
	sort.Strings(names)
	return names
}

// serveBulkMeta serves bulk metadata requests (requests to /meta/...).
func (r *Router) serveBulkMeta(w http.ResponseWriter, req *http.Request) error {
	switch req.Method {
	case "GET", "HEAD":
		// A bare meta returns all endpoints.
		// See http://tinyurl.com/q2qd9nn
		if req.URL.Path == "/" || req.URL.Path == "" {
			jsonhttp.WriteJSON(w, http.StatusOK, r.metaNames())
			return nil
		}
		resp, err := r.serveBulkMetaGet(req)
		if err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		jsonhttp.WriteJSON(w, http.StatusOK, resp)
		return nil
	case "PUT":
		return r.serveBulkMetaPut(req)
	default:
		return params.ErrMethodNotAllowed
	}
}

// serveBulkMetaGet serves the "bulk" metadata retrieval endpoint
// that can return information on several ids at once.
//
// GET meta/$endpoint?id=$id0[&id=$id1...][$otherflags]
// http://tinyurl.com/kdrly9f
func (r *Router) serveBulkMetaGet(req *http.Request) (interface{}, error) {
	// TODO get the metadata concurrently for each id.
	ids := req.Form["id"]
	if len(ids) == 0 {
		return nil, errgo.Newf("no ids specified in meta request")
	}
	delete(req.Form, "id")
	result := make(map[string]interface{})
	for _, id := range ids {
		url, err := charm.ParseReference(id)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		if err := r.resolveURL(url); err != nil {
			if errgo.Cause(err) == params.ErrNotFound {
				// URLs not found will be omitted from the result.
				// http://tinyurl.com/o5ptfkk
				continue
			}
			// Note: preserve error cause from resolveURL.
			return nil, errgo.Mask(err, errgo.Any)
		}
		meta, err := r.serveMetaGet(url, req)
		if cause := errgo.Cause(err); cause == params.ErrNotFound || cause == params.ErrMetadataNotFound {
			// The relevant data does not exist.
			// http://tinyurl.com/o5ptfkk
			continue
		}
		if err != nil {
			return nil, errgo.Mask(err)
		}
		result[id] = meta
	}
	return result, nil
}

// serveBulkMetaPut serves a bulk PUT request to several ids.
// PUT /meta/$endpoint
// http://tinyurl.com/na83nta
func (r *Router) serveBulkMetaPut(req *http.Request) error {
	if len(req.Form["id"]) > 0 {
		return fmt.Errorf("ids may not be specified in meta PUT request")
	}
	var ids map[string]*json.RawMessage
	if err := unmarshalJSONBody(req, &ids); err != nil {
		return errgo.Mask(err, errgo.Is(params.ErrBadRequest))
	}
	var multiErr multiError
	for id, val := range ids {
		if err := r.serveBulkMetaPutOne(req, id, val); err != nil {
			if multiErr == nil {
				multiErr = make(multiError)
			}
			multiErr[id] = errgo.Mask(err, errgo.Any)
		}
	}
	if len(multiErr) != 0 {
		return multiErr
	}
	return nil
}

// serveBulkMetaPutOne serves a PUT to a single id as part of a bulk PUT
// request. It's in a separate function to make the error handling easier.
func (r *Router) serveBulkMetaPutOne(req *http.Request, id string, val *json.RawMessage) error {
	url, err := charm.ParseReference(id)
	if err != nil {
		return errgo.Mask(err)
	}
	if err := r.resolveURL(url); err != nil {
		// Note: preserve error cause from resolveURL.
		return errgo.Mask(err, errgo.Any)
	}
	if err := r.authorize(url, req); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	if err := r.serveMetaPutBody(url, req, val); err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	return nil
}

// maxMetadataConcurrency specifies the maximum number
// of goroutines started to service a given GetMetadata request.
// 5 is enough to more that cover the number of metadata
// group handlers in the current API.
const maxMetadataConcurrency = 5

// GetMetadata retrieves metadata for the given charm or bundle id,
// including information as specified by the includes slice.
func (r *Router) GetMetadata(id *charm.Reference, includes []string, req *http.Request) (map[string]interface{}, error) {
	groups := make(map[interface{}][]BulkIncludeHandler)
	includesByGroup := make(map[interface{}][]string)
	for _, include := range includes {
		// Get the key that lets us choose the include handler.
		includeKey, _ := handlerKey(include)
		handler := r.handlers.Meta[includeKey]
		if handler == nil {
			return nil, errgo.Newf("unrecognized metadata name %q", include)
		}

		// Get the key that lets us group this handler into the
		// correct bulk group.
		key := handler.Key()
		groups[key] = append(groups[key], handler)
		includesByGroup[key] = append(includesByGroup[key], include)
	}
	results := make(map[string]interface{})
	// TODO when the number of groups is 1 (a common case,
	// using parallel.NewRun is actually slowing things down
	// by creating a goroutine). We could optimise it so that
	// it doesn't actually create a goroutine in that case.
	run := parallel.NewRun(maxMetadataConcurrency)
	var mu sync.Mutex
	for _, g := range groups {
		g := g
		run.Do(func() error {
			// We know that we must have at least one element in the
			// slice here. We could use any member of the slice to
			// actually handle the request, so arbitrarily choose
			// g[0]. Note that g[0].Key() is equal to g[i].Key() for
			// every i in the slice.
			groupIncludes := includesByGroup[g[0].Key()]

			// Paths contains all the path elements after
			// the handler key has been stripped off.
			// TODO(rog) BUG shouldn't this be len(groupIncludes) ?
			paths := make([]string, len(g))
			for i, include := range groupIncludes {
				_, paths[i] = handlerKey(include)
			}
			groupResults, err := g[0].HandleGet(g, id, paths, nil, req)
			if err != nil {
				// TODO(rog) if it's a BulkError, attach
				// the original include path to error (the BulkError
				// should contain the index of the failed one).
				return errgo.Mask(err, errgo.Any)
			}
			mu.Lock()
			for i, result := range groupResults {
				// Omit nil results from map. Note: omit statically typed
				// nil results too to make it easy for handlers to return
				// possibly nil data with a static type.
				// http://tinyurl.com/o5ptfkk
				if !isNull(result) {
					results[groupIncludes[i]] = result
				}
			}
			mu.Unlock()
			return nil
		})
	}
	if err := run.Wait(); err != nil {
		// We could have got multiple errors, but we'll only return one of them.
		return nil, errgo.Mask(err.(parallel.Errors)[0], errgo.Any)
	}
	return results, nil
}

// PutMetadata puts metadata for the given id. Each key in data holds
// the name of a metadata endpoint; its associated value
// holds the value to be written.
func (r *Router) PutMetadata(id *charm.Reference, data map[string]*json.RawMessage, req *http.Request) error {
	groups := make(map[interface{}][]BulkIncludeHandler)
	valuesByGroup := make(map[interface{}][]*json.RawMessage)
	pathsByGroup := make(map[interface{}][]string)
	for path, body := range data {
		// Get the key that lets us choose the meta handler.
		metaKey, _ := handlerKey(path)
		handler := r.handlers.Meta[metaKey]
		if handler == nil {
			return errgo.Newf("unrecognized metadata name %q", path)
		}

		// Get the key that lets us group this handler into the
		// correct bulk group.
		key := handler.Key()
		groups[key] = append(groups[key], handler)
		valuesByGroup[key] = append(valuesByGroup[key], body)

		// Paths contains all the path elements after
		// the handler key has been stripped off.
		pathsByGroup[key] = append(pathsByGroup[key], path)
	}
	var multiErr multiError
	for _, g := range groups {
		// We know that we must have at least one element in the
		// slice here. We could use any member of the slice to
		// actually handle the request, so arbitrarily choose
		// g[0]. Note that g[0].Key() is equal to g[i].Key() for
		// every i in the slice.
		key := g[0].Key()

		paths := pathsByGroup[key]
		// The paths passed to the handler contain all the path elements
		// after the handler key has been stripped off.
		strippedPaths := make([]string, len(paths))
		for i, path := range paths {
			_, strippedPaths[i] = handlerKey(path)
		}

		errs := g[0].HandlePut(g, id, strippedPaths, valuesByGroup[key], req)
		if len(errs) > 0 {
			if multiErr == nil {
				multiErr = make(multiError)
			}
			if len(errs) != len(paths) {
				return fmt.Errorf("unexpected error count; expected %d, got %q", len(paths), errs)
			}
			for i, err := range errs {
				if err != nil {
					multiErr[paths[i]] = err
				}
			}
		}
	}
	if len(multiErr) != 0 {
		return multiErr
	}
	return nil
}

// splitPath returns the first path element
// after path[i:] and the start of the next
// element.
//
// For example, splitPath("/foo/bar/bzr", 4) returns ("bar", 8).
func splitPath(path string, i int) (elem string, nextIndex int) {
	if i < len(path) && path[i] == '/' {
		i++
	}
	j := strings.Index(path[i:], "/")
	if j == -1 {
		return path[i:], len(path)
	}
	j += i
	return path[i:j], j
}

// splitId splits the given URL path into a charm or bundle
// URL and the rest of the path.
func splitId(path string) (url *charm.Reference, rest string, err error) {
	path = strings.TrimPrefix(path, "/")

	part, i := splitPath(path, 0)

	// skip ~<username>
	if strings.HasPrefix(part, "~") {
		part, i = splitPath(path, i)
	}
	// skip series
	if knownSeries[part] {
		part, i = splitPath(path, i)
	}

	// part should now contain the charm name,
	// and path[0:i] should contain the entire
	// charm id.

	urlStr := strings.TrimSuffix(path[0:i], "/")
	url, err = charm.ParseReference(urlStr)
	if err != nil {
		return nil, "", errgo.Mask(err)
	}
	return url, path[i:], nil
}
