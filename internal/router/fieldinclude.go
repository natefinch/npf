package router

import (
	"encoding/json"
	"net/url"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
)

// A FieldQueryFunc is used to retrieve a metadata document for the given URL,
// selecting only those fields specified in keys of the given selector.
type FieldQueryFunc func(id *charm.Reference, selector map[string]int) (interface{}, error)

// FieldUpdater records field changes made by a FieldUpdateFunc.
type FieldUpdater struct {
	fields map[string]interface{}
}

// UpdateField requests that the provided field is updated with
// the given value.
func (u *FieldUpdater) UpdateField(fieldName string, val interface{}) {
	u.fields[fieldName] = val
}

// A FieldUpdateFunc is used to update a metadata document for the
// given id. For each field in fields, it should set that field to
// its corresponding value in the metadata document.
type FieldUpdateFunc func(id *charm.Reference, fields map[string]interface{}) error

// A FieldGetFunc returns some data from the given document. The
// document will have been returned from an earlier call to the
// associated QueryFunc.
type FieldGetFunc func(doc interface{}, id *charm.Reference, path string, flags url.Values) (interface{}, error)

// FieldPutFunc sets using the given FieldUpdater corresponding to fields to be set
// in the metadata document for the given id. The path holds the metadata path
// after the initial prefix has been removed.
type FieldPutFunc func(id *charm.Reference, path string, val *json.RawMessage, updater *FieldUpdater) error

// FieldIncludeHandlerParams specifies the parameters for NewFieldIncludeHandler.
type FieldIncludeHandlerParams struct {
	// Key is used to group together similar FieldIncludeHandlers
	// (the same query should be generated for any given key).
	Key interface{}

	// Query is used to retrieve the document from the database for
	// GET requests. The fields passed to the query will be the
	// union of all fields found in all the handlers in the bulk
	// request.
	Query FieldQueryFunc

	// Fields specifies which fields are required by the given handler.
	Fields []string

	// Handle actually returns the data from the document retrieved
	// by Query, for GET requests.
	HandleGet FieldGetFunc

	// HandlePut generates update operations for a PUT
	// operation.
	HandlePut FieldPutFunc

	// Update is used to update the document in the database for
	// PUT requests.
	Update FieldUpdateFunc
}

type fieldIncludeHandler struct {
	p FieldIncludeHandlerParams
}

// FieldIncludeHandler returns a BulkIncludeHandler that will perform
// only a single database query for several requests. See FieldIncludeHandlerParams
// for more detail.
//
// See in ../v4/api.go for an example of its use.
func FieldIncludeHandler(p FieldIncludeHandlerParams) BulkIncludeHandler {
	return &fieldIncludeHandler{p}
}

func (h *fieldIncludeHandler) Key() interface{} {
	return h.p.Key
}

func (h *fieldIncludeHandler) HandlePut(hs []BulkIncludeHandler, id *charm.Reference, paths []string, values []*json.RawMessage) []error {
	updater := &FieldUpdater{
		fields: make(map[string]interface{}),
	}
	var errs []error
	errCount := 0
	setError := func(i int, err error) {
		if errs == nil {
			errs = make([]error, len(hs))
		}
		if errs[i] == nil {
			errs[i] = err
			errCount++
		}
	}
	for i, h := range hs {
		h := h.(*fieldIncludeHandler)
		if h.p.HandlePut == nil {
			setError(i, errgo.New("PUT not supported"))
			continue
		}
		if err := h.p.HandlePut(id, paths[i], values[i], updater); err != nil {
			setError(i, errgo.Mask(err, errgo.Any))
		}
	}
	if errCount == len(hs) {
		// Every HandlePut request has drawn an error,
		// no need to call Update.
		return errs
	}
	if err := h.p.Update(id, updater.fields); err != nil {
		for i := range hs {
			setError(i, err)
		}
	}
	return errs
}

func (h *fieldIncludeHandler) HandleGet(hs []BulkIncludeHandler, id *charm.Reference, paths []string, flags url.Values) ([]interface{}, error) {
	funcs := make([]FieldGetFunc, len(hs))
	selector := make(map[string]int)
	// Extract the handler functions and union all the fields.
	for i, h := range hs {
		h := h.(*fieldIncludeHandler)
		funcs[i] = h.p.HandleGet
		for _, field := range h.p.Fields {
			selector[field] = 1
		}
	}
	// Make the single query.
	doc, err := h.p.Query(id, selector)
	if err != nil {
		// Note: preserve error cause from handlers.
		return nil, errgo.Mask(err, errgo.Any)
	}

	// Call all the handlers with the resulting query document.
	results := make([]interface{}, len(hs))
	for i, f := range funcs {
		var err error
		results[i], err = f(doc, id, paths[i], flags)
		if err != nil {
			// TODO correlate error with handler (perhaps return
			// an error that identifies the slice position of the handler that
			// failed).
			// Note: preserve error cause from handlers.
			return nil, errgo.Mask(err, errgo.Any)
		}
	}
	return results, nil
}
