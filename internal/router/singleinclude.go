package router

import (
	"encoding/json"
	"net/url"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"
)

var _ BulkIncludeHandler = SingleIncludeHandler(nil)

// SingleIncludeHandler implements BulkMetaHander for a non-batching
// metadata retrieval function that can perform a GET only.
type SingleIncludeHandler func(id *charm.Reference, path string, flags url.Values) (interface{}, error)

// Key implements BulkMetadataHander.Key.
func (h SingleIncludeHandler) Key() interface{} {
	// Use a local type so that we are guaranteed that nothing
	// other than SingleIncludeHandler can generate that key.
	type singleMetaHandlerKey struct{}
	return singleMetaHandlerKey(singleMetaHandlerKey{})
}

// Handle implements BulkMetadataHander.HandleGet.
func (h SingleIncludeHandler) HandleGet(hs []BulkIncludeHandler, id *charm.Reference, paths []string, flags url.Values) ([]interface{}, error) {
	results := make([]interface{}, len(hs))
	for i, h := range hs {
		h := h.(SingleIncludeHandler)
		result, err := h(id, paths[i], flags)
		if err != nil {
			// TODO(rog) include index of failed handler.
			return nil, errgo.Mask(err, errgo.Any)
		}
		results[i] = result
	}
	return results, nil
}

var errPutNotImplemented = errgo.New("PUT not implemented")

func (h SingleIncludeHandler) HandlePut(hs []BulkIncludeHandler, id *charm.Reference, paths []string, values []*json.RawMessage) []error {
	errs := make([]error, len(hs))
	for i := range hs {
		errs[i] = errPutNotImplemented
	}
	return errs
}
