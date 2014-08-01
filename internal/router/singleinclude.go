package router

import (
	"net/url"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v2"
)

var _ BulkIncludeHandler = SingleIncludeHandler(nil)

// SingleIncludeHandler implements BulkMetaHander for a non-batching
// metadata retrieval function.
type SingleIncludeHandler func(id *charm.URL, path string, flags url.Values) (interface{}, error)

// Key implements BulkMetadataHander.Key.
func (h SingleIncludeHandler) Key() interface{} {
	// Use a local type so that we are guaranteed that nothing
	// other than SingleIncludeHandler can generate that key.
	type singleMetaHandlerKey struct{}
	return singleMetaHandlerKey(singleMetaHandlerKey{})
}

// Handle implements BulkMetadataHander.Handle.
func (h SingleIncludeHandler) Handle(hs []BulkIncludeHandler, id *charm.URL, paths []string, flags url.Values) ([]interface{}, error) {
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
