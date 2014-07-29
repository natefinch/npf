package router

import (
	"net/url"

	"gopkg.in/juju/charm.v2"
)

// A FieldQueryFunc is used to retrieve a metadata document for the given URL,
// selecting only those fields specified in keys of the given selector.
type FieldQueryFunc func(id *charm.URL, selector map[string]int) (interface{}, error)

// A FieldHandlerFunc returns some data from the given document. The
// document will have been returned from an earlier call to the
// associated QueryFunc.
type FieldHandlerFunc func(doc interface{}, id *charm.URL, path string, flags url.Values) (interface{}, error)

// FieldIncludeHandler returns a BulkIncludeHandler that will perform
// only a single database query for several requests. The given key is
// used to group together similar FieldIncludeHandlers (the same query
// should be generated for a given key). The given query is used to
// retrieve the document from the database, and the given handle
// function is used to actually retrieve the metadata after the query.
//
// The fields specify which fields are required by the given handler.
// The fields passed to the query will be the union of all fields found
// in all the handlers in the bulk request.
func FieldIncludeHandler(key interface{}, q FieldQueryFunc, fields []string, handle FieldHandlerFunc) BulkIncludeHandler {
	return &fieldIncludeHandler{
		key:    key,
		query:  q,
		fields: fields,
		handle: handle,
	}
}

type fieldIncludeHandler struct {
	key    interface{}
	query  FieldQueryFunc
	fields []string
	handle FieldHandlerFunc
}

func (h *fieldIncludeHandler) Key() interface{} {
	return h.key
}

func (h *fieldIncludeHandler) Handle(hs []BulkIncludeHandler, id *charm.URL, paths []string, flags url.Values) ([]interface{}, error) {
	funcs := make([]FieldHandlerFunc, len(hs))
	selector := make(map[string]int)
	// Extract the handler functions and union all the fields.
	for i, h := range hs {
		h := h.(*fieldIncludeHandler)
		funcs[i] = h.handle
		for _, field := range h.fields {
			selector[field] = 1
		}
	}
	// Make the single query.
	doc, err := h.query(id, selector)
	if err != nil {
		return nil, err
	}
	// Call all the handlers with the resulting query document
	results := make([]interface{}, len(hs))
	for i, f := range funcs {
		var err error
		results[i], err = f(doc, id, paths[i], flags)
		if err != nil {
			// TODO correlate error with handler (perhaps return
			// an error that identifies the slice position of the handler that
			// failed
			return nil, err
		}
	}
	return results, nil
}
