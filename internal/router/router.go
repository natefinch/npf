// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The router package implements an HTTP request router for charm store
// HTTP requests.
package router

import (
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"

	"github.com/juju/errgo"
	charm "gopkg.in/juju/charm.v3"

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
	"precise": true,
	"quantal": true,
	"raring":  true,
	"saucy":   true,
	"trusty":  true,
	"utopic":  true,
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
	// together in preparation for a call to Handle.
	// The key should be comparable for equality.
	// Please do not return NaN. That would be silly, OK?
	Key() interface{}

	// Handle returns the results of invoking all the given handlers
	// on the given charm or bundle id. Each result is held in
	// the respective element of the returned slice.
	//
	// All of the handlers' Keys will be equal to the receiving handler's
	// Key.
	//
	// Each item in paths holds the remaining metadata path
	// for the handler in the corresponding position
	// in hs after the prefix in Handlers.Meta has been stripped,
	// and flags holds all the url query values.
	//
	// TODO(rog) document indexed errors.
	Handle(hs []BulkIncludeHandler, id *charm.Reference, paths []string, flags url.Values) ([]interface{}, error)
}

// IdHandler handles a charm store request rooted at the given id.
// The request path (req.URL.Path) holds the URL path after
// the id has been stripped off.
type IdHandler func(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error

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
	resolveURL func(ref *charm.Reference) error
}

// New returns a charm store router that will route requests to
// the given handlers and retrieve metadata from the given database.
//
// The resolveURL function will be called to resolve ids in
// router paths - it should fill in the Series and Revision
// fields of its argument URL if they are not specified.
// The Cause of the resolveURL error will be left unchanged,
// as for the handlers.
func New(handlers *Handlers, resolveURL func(url *charm.Reference) error) *Router {
	r := &Router{
		handlers:   handlers,
		resolveURL: resolveURL,
	}
	mux := NewServeMux()
	mux.Handle("/meta/", http.StripPrefix("/meta", HandleJSON(r.serveBulkMeta)))
	for path, handler := range r.handlers.Global {
		path = "/" + path
		prefix := path
		if strings.HasSuffix(prefix, "/") {
			prefix = prefix[0 : len(prefix)-1]
		}
		mux.Handle(path, http.StripPrefix(prefix, handler))
	}
	mux.Handle("/", HandleErrors(r.serveIds))
	r.handler = mux
	return r
}

// ServeHTTP implements http.Handler.ServeHTTP.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
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
	if err := r.resolveURL(url); err != nil {
		// Note: preserve error cause from resolveURL.
		return errgo.Mask(err, errgo.Any)
	}
	key, path := handlerKey(path)
	if key == "" {
		return params.ErrNotFound
	}
	if handler, ok := r.handlers.Id[key]; ok {
		req.URL.Path = path
		err := handler(url, w, req)
		// Note: preserve error cause from handlers.
		return errgo.Mask(err, errgo.Any)
	}
	if key != "meta/" && key != "meta" {
		return params.ErrNotFound
	}
	req.URL.Path = path
	resp, err := r.serveMeta(url, req)
	if err != nil {
		// Note: preserve error cause from handlers.
		return errgo.Mask(err, errgo.Any)
	}
	WriteJSON(w, http.StatusOK, resp)
	return nil
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

func (r *Router) serveMeta(id *charm.Reference, req *http.Request) (interface{}, error) {
	key, path := handlerKey(req.URL.Path)
	if key == "" {
		// GET id/meta
		// http://tinyurl.com/nysdjly
		return r.metaNames(), nil
	}
	if key == "any" {
		// GET id/meta/any?[include=meta[&include=meta...]]
		// http://tinyurl.com/q5vcjpk
		meta, err := r.GetMetadata(id, req.Form["include"])
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
		results, err := handler.Handle([]BulkIncludeHandler{handler}, id, []string{path}, req.Form)
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
	return nil, params.ErrNotFound
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

func (r *Router) metaNames() []string {
	names := make([]string, 0, len(r.handlers.Meta))
	for name := range r.handlers.Meta {
		names = append(names, strings.TrimSuffix(name, "/"))
	}
	sort.Strings(names)
	return names
}

// serveBulkMeta serves the "bulk" metadata retrieval endpoint
// that can return information on several ids at once.
//
// GET meta/$endpoint?id=$id0[&id=$id1...][$otherflags]
// http://tinyurl.com/kdrly9f
func (r *Router) serveBulkMeta(w http.ResponseWriter, req *http.Request) (interface{}, error) {
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
		meta, err := r.serveMeta(url, req)
		if errgo.Cause(err) == params.ErrMetadataNotFound {
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

// GetMetadata retrieves metadata for the given charm or bundle id,
// including information as specified by the includes slice.
func (r *Router) GetMetadata(id *charm.Reference, includes []string) (map[string]interface{}, error) {
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
	for _, g := range groups {
		// We know that we must have at least one element in the
		// slice here. We could use any member of the slice to
		// actually handle the request, so arbitrarily choose
		// g[0]. Note that g[0].Key() is equal to g[i].Key() for
		// every i in the slice.
		groupIncludes := includesByGroup[g[0].Key()]

		// Paths contains all the path elements after
		// the handler key has been stripped off.
		paths := make([]string, len(g))
		for i, include := range groupIncludes {
			_, paths[i] = handlerKey(include)
		}
		groupResults, err := g[0].Handle(g, id, paths, nil)
		if err != nil {
			// TODO(rog) if it's a BulkError, attach
			// the original include path to error (the BulkError
			// should contain the index of the failed one).
			return nil, errgo.Mask(err, errgo.Any)
		}
		for i, result := range groupResults {
			// Omit nil results from map. Note: omit statically typed
			// nil results too to make it easy for handlers to return
			// possibly nil data with a static type.
			// http://tinyurl.com/o5ptfkk
			if !isNull(result) {
				results[groupIncludes[i]] = result
			}
		}
	}
	return results, nil
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
