// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The router package implements an HTTP request router for charm store
// HTTP requests.
package router

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"

	charm "gopkg.in/juju/charm.v2"
	"labix.org/v2/mgo"
	"labix.org/v2/mgo/bson"

	"github.com/juju/charmstore/params"
)

var knownSeries = map[string]bool{
	"bundle":  true,
	"precise": true,
	"quantal": true,
	"raring":  true,
	"saucy":   true,
	"trusty":  true,
	"utopic":  true,
}

// MetaHandler retrieves metadata for the given id. The path provides
// the full name of the metadata endpoint (for example, "charm-metadata"
// or "extra-info/"), and the flags holds all the url query values.
//
// The getter can be used to retrieve items from mongo.
type MetaHandler func(getter ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error)

// IdHandler handles a charm store request rooted at the given id.
// The request path (req.URL.Path) holds the URL path after
// the id has been stripped off.
type IdHandler func(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error

// Handlers specifies how HTTP requests will be routed
// by the router.
type Handlers struct {
	// Global holds handlers for paths not matched by Meta or Id.
	// The map key is the path; the value is the handler that will
	// be used to handle that path.
	//
	// Path matching is by matched by longest-prefix - the same as
	// http.ServeMux.
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
	Meta map[string]MetaHandler
}

// Router represents a charm store HTTP request router.
type Router struct {
	handlers *Handlers
	db       *mgo.Database
	handler  http.Handler
}

// New returns a charm store router that will route requests to
// the given handlers and retrieve metadata from the given database.
func New(db *mgo.Database, handlers *Handlers) *Router {
	r := &Router{
		handlers: handlers,
		db:       db,
	}
	mux := http.NewServeMux()
	for path, handler := range r.handlers.Global {
		mux.Handle("/"+path, handler)
	}
	mux.Handle("/", HandleErrors(r.serveIds))
	r.handler = mux
	return r
}

var ErrNotFound = fmt.Errorf("not found")

// ServeHTTP implements http.Handler.ServeHTTP.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.handler.ServeHTTP(w, req)
}

// Handlers returns the set of handlers that the router was created with.
// This should not be changed.
func (r *Router) Handlers() *Handlers {
	return r.handlers
}

// serveIds serves requests that may be rooted at a charm or bundle id.
func (r *Router) serveIds(w http.ResponseWriter, req *http.Request) error {
	if err := req.ParseForm(); err != nil {
		return err
	}
	// TODO(rog) can we really just always ignore a trailing slash ?
	path := req.URL.Path
	path = strings.TrimSuffix(path, "/")
	path = strings.TrimPrefix(path, "/")
	url, path, err := splitId(path)
	if err != nil {
		return err
	}
	if url.Series == "" || url.Revision == -1 {
		// TODO(rog) look up charm URL
		return fmt.Errorf("imprecise charm URLs not yet supported")
	}
	key, path := handlerKey(path)
	if key == "" {
		return ErrNotFound
	}
	if handler, ok := r.handlers.Id[key]; ok {
		req.URL.Path = path
		return handler(url, w, req)
	}
	if key != "meta/" {
		return ErrNotFound
	}
	req.URL.Path = path
	resp, err := r.serveMeta(url, w, req)
	if err != nil {
		return err
	}
	WriteJSON(w, http.StatusOK, resp)
	return nil
}

// handlerKey returns a key that can be used to look up a handler at the
// given path, and the remaining path elements. If there is no possible
// key, the returned key is empty.
func handlerKey(path string) (key, rest string) {
	key, i := splitPath(path, 0)
	if key == "" {
		// TODO what *should* we get if we GET just an id?
		return "", rest
	}
	if i != len(path) {
		// There are more elements, so include the / character
		// that terminates the element.
		return path[0:i], path[i:]
	}
	return key, ""
}

func (r *Router) serveMeta(id *charm.URL, w http.ResponseWriter, req *http.Request) (interface{}, error) {
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
			return nil, err
		}
		return params.MetaAnyResponse{
			Id:   id,
			Meta: meta,
		}, nil
	}
	if handler := r.handlers.Meta[key]; handler != nil {
		return handler(getterFunc(r.getter), id, path, req.Form)
	}
	return nil, ErrNotFound
}

func (r *Router) metaNames() []string {
	names := make([]string, 0, len(r.handlers.Meta))
	for name := range r.handlers.Meta {
		names = append(names, strings.TrimSuffix(name, "/"))
	}
	return names
}

var (
	collectionMutex sync.Mutex
	collections     = make(map[reflect.Type]string)
)

// RegisterCollection registers the type of docType as document type for
// a given collection, so documents of that type can be retrieved using
// ItemGetter's GetItem method.
//
// The document type must be a pointer to a struct or map type suitable
// for unmarshalling with mgo/bson.
func RegisterCollection(collectionName string, docType interface{}) {
	collectionMutex.Lock()
	defer collectionMutex.Unlock()
	t := reflect.TypeOf(docType)
	if t.Kind() != reflect.Ptr {
		panic(fmt.Errorf("RegisterCollection called with non-pointer type %T", docType))
	}
	if k := t.Elem().Kind(); k != reflect.Struct && k != reflect.Map {
		panic(fmt.Errorf("RegisterCollection called with invalid type %T", docType))
	}
	// The type that's passed to GetItem is a pointer to a pointer,
	// so store that type in the collections map.
	t = reflect.PtrTo(t)
	for typ, name := range collections {
		if name == collectionName && typ != t {
			panic(fmt.Errorf("duplicate type registered for collection %q", collectionName))
		}
	}
	collections[t] = collectionName
}

func collectionForValue(val interface{}) (string, error) {
	collectionMutex.Lock()
	defer collectionMutex.Unlock()
	if coll := collections[reflect.TypeOf(val)]; coll != "" {
		return coll, nil
	}
	return "", fmt.Errorf("no collection found for value of type %T", val)
}

// The ItemGetter interface allows the retrieval of a document from
// mongo, filling fields as specified (the field names refer to the bson
// field names in the marshalled form of the document).
//
// The actual collection retrieved from is determined by the type of
// val, which should be a pointer to a type registered with
// RegisterCollection.
//
// For example, if RegisterCollection("charms", (*EntityDoc)(nil)) has
// been called, then:
//
//     var item *EntityDoc			// Note: it's a pointer.
//     err := getter.GetItem(charmId, &item, "charmmeta")
//
// will retrieve the document with _id=charmId from the charms
// collection and set item to an EntityDoc with at least its CharmMeta
// field set.
//
// Note that the retrieved item should not be modified - it may be in
// use concurrently by other goroutines.
type ItemGetter interface {
	GetItem(id interface{}, val interface{}, fields ...string) error
}

type getterFunc func(id interface{}, val interface{}, fields ...string) error

func (f getterFunc) GetItem(id interface{}, val interface{}, fields ...string) error {
	return f(id, val, fields...)
}

// GetMetadata retrieves metadata for the given charm or bundle id,
// including information as specified by the includes slice.
func (r *Router) GetMetadata(id *charm.URL, includes []string) (map[string]interface{}, error) {
	if len(includes) == 0 {
		return nil, nil
	}
	// TODO(rog) optimize this to avoid making a db round trip
	// for every piece of metadata and to cache previously
	// fetched results.
	getter := getterFunc(r.getter)
	results := make(map[string]interface{})
	for _, include := range includes {
		handler := r.handlers.Meta[include]
		if handler == nil {
			return nil, fmt.Errorf("unrecognized metadata name %q", include)
		}
		result, err := handler(getter, id, "", nil)
		if err != nil {
			// TODO(rog) If error is "inappropriate metadata", then
			// just omit the result.
			return nil, fmt.Errorf("cannot get meta/%q: %v", include, err)
		}
		results[include] = result
	}
	return results, nil
}

func (r *Router) getter(id interface{}, val interface{}, fields ...string) error {
	if len(fields) == 0 {
		return fmt.Errorf("no fields specified in GetItem")
	}
	collection, err := collectionForValue(val)
	if err != nil {
		return err
	}
	selector := make(bson.D, len(fields))
	for i, field := range fields {
		selector[i] = bson.DocElem{
			Name:  field,
			Value: 1,
		}
	}
	return r.db.C(collection).
		Find(bson.D{{"_id", id}}).
		Select(selector).
		One(val)
}

// splitPath returns the first path element
// after path[i:] and the start of the next
// element.
//
// For example, splitPath("foo/bar/bzr", 4) returns ("bar", 8).
func splitPath(path string, i int) (elem string, nextIndex int) {
	j := strings.Index(path[i:], "/")
	if j == -1 {
		return path[i:], len(path)
	}
	j += i
	return path[i:j], j + 1
}

func splitId(path string) (url *charm.URL, rest string, err error) {
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
	ref, series, err := charm.ParseReference(urlStr)
	if err != nil {
		return nil, "", err
	}
	url = &charm.URL{
		Reference: ref,
		Series:    series,
	}
	return url, path[i:], nil
}
