// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/juju/errgo"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v3"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type RouterSuite struct {
	jujutesting.IsolationSuite
}

var _ = gc.Suite(&RouterSuite{})

var routerGetTests = []struct {
	about            string
	handlers         Handlers
	urlStr           string
	expectStatus     int
	expectBody       interface{}
	expectQueryCount int32
	resolveURL       func(*charm.Reference) error
}{{
	about: "global handler",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo": HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
				return ReqInfo{
					Path: req.URL.Path,
					Form: req.Form,
				}, nil
			}),
		},
	},
	urlStr:       "/foo",
	expectStatus: http.StatusOK,
	expectBody: ReqInfo{
		Path: "",
	},
}, {
	about: "global handler with sub-path and flags",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo/bar/": HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
				return ReqInfo{
					Path: req.URL.Path,
					Form: req.Form,
				}, nil
			}),
		},
	},
	urlStr:       "/foo/bar/a/b?a=1&b=two",
	expectStatus: http.StatusOK,
	expectBody: ReqInfo{
		Path: "/a/b",
		Form: url.Values{
			"a": {"1"},
			"b": {"two"},
		},
	},
}, {
	about:        "invalid form",
	urlStr:       "/foo?a=%",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot parse form: invalid URL escape "%"`,
	},
}, {
	about: "id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/precise/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:precise/wordpress-34",
	},
}, {
	about: "id handler with extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:       "/precise/wordpress-34/foo/blah/arble",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:precise/wordpress-34",
		Path:     "/blah/arble",
	},
}, {
	about: "id handler with allowed extra path but none given",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:       "/precise/wordpress-34/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
	},
}, {
	about: "id handler with unwanted extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/precise/wordpress-34/foo/blah",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
	},
}, {
	about: "id handler with user",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/~joe/precise/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:~joe/precise/wordpress-34",
	},
}, {
	about: "id handler with user and extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:       "/~joe/precise/wordpress-34/foo/blah/arble",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:~joe/precise/wordpress-34",
		Path:     "/blah/arble",
	},
}, {
	about: "id handler that returns an error",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": errorIdHandler,
		},
	},
	urlStr:       "/~joe/precise/wordpress-34/foo/blah/arble",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "errorIdHandler error",
	},
}, {
	about: "id handler that returns a not-found error",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": func(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
				return params.ErrNotFound
			},
		},
	},
	urlStr:       "/~joe/precise/wordpress-34/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Message: "not found",
		Code:    params.ErrNotFound,
	},
}, {
	about: "id handler that returns some other kind of coded error",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": func(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
				return errgo.WithCausef(nil, params.ErrorCode("foo"), "a message")
			},
		},
	},
	urlStr:       "/~joe/precise/wordpress-34/foo",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "a message",
		Code:    "foo",
	},
}, {
	about: "id with unspecified series and revision, resolved",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/~joe/wordpress/foo",
	resolveURL:   newResolveURL("precise", 34),
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:~joe/precise/wordpress-34",
	},
}, {
	about: "id with error on resolving",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/wordpress/meta",
	resolveURL:   resolveURLError(errgo.New("resolve URL error")),
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "resolve URL error",
	},
}, {
	about: "id with error on resolving that has a Cause",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/wordpress/meta",
	resolveURL:   resolveURLError(params.ErrNotFound),
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Message: "not found",
		Code:    params.ErrNotFound,
	},
}, {
	about: "meta list",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
			"bar": testMetaHandler,
		},
	},
	urlStr:       "/precise/wordpress-42/meta",
	expectStatus: http.StatusOK,
	expectBody:   []string{"bar", "foo"},
}, {
	about: "meta handler",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	urlStr:       "/precise/wordpress-42/meta/foo",
	expectStatus: http.StatusOK,
	expectBody: &metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Method:   "GET",
	},
}, {
	about: "meta handler with additional elements",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler,
		},
	},
	urlStr:       "/precise/wordpress-42/meta/foo/bar/baz",
	expectStatus: http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Method:   "GET",
		Path:     "/bar/baz",
	},
}, {
	about: "meta handler with params",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	urlStr:       "/precise/wordpress-42/meta/foo?one=a&two=b&one=c",
	expectStatus: http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Method:   "GET",
		Flags: url.Values{
			"one": {"a", "c"},
			"two": {"b"},
		},
	},
}, {
	about:        "meta handler that's not found",
	urlStr:       "/precise/wordpress-42/meta/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
	},
}, {
	about: "meta handler with nil data",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": constMetaHandler(nil),
		},
	},
	urlStr:       "/precise/wordpress-42/meta/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrMetadataNotFound,
		Message: "metadata not found",
	},
}, {
	about: "meta handler with typed nil data",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": constMetaHandler((*struct{})(nil)),
		},
	},
	urlStr:       "/precise/wordpress-42/meta/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrMetadataNotFound,
		Message: "metadata not found",
	},
}, {
	about:  "meta handler with field selector",
	urlStr: "/precise/wordpress-42/meta/foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": fieldSelectHandler("handler1", 0, "field1", "field2"),
		},
	},
	expectStatus:     http.StatusOK,
	expectQueryCount: 1,
	expectBody: fieldSelectHandleInfo{
		HandlerId: "handler1",
		Doc: fieldSelectQueryInfo{
			Id:       mustParseReference("cs:precise/wordpress-42"),
			Selector: map[string]int{"field1": 1, "field2": 1},
		},
		Id:     mustParseReference("cs:precise/wordpress-42"),
		Method: "GET",
	},
}, {
	about:  "meta handler returning error with code",
	urlStr: "/precise/wordpress-42/meta/foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": errorMetaHandler(errgo.WithCausef(nil, params.ErrorCode("arble"), "a message")),
		},
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    "arble",
		Message: "a message",
	},
}, {
	about:        "meta/any, no includes",
	urlStr:       "/precise/wordpress-42/meta/any",
	expectStatus: http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: mustParseReference("cs:precise/wordpress-42"),
	},
}, {
	about:  "meta/any, some includes all using same key",
	urlStr: "/precise/wordpress-42/meta/any?include=field1-1&include=field2&include=field1-2",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"field1-1": fieldSelectHandler("handler1", 0, "field1"),
			"field2":   fieldSelectHandler("handler2", 0, "field2"),
			"field1-2": fieldSelectHandler("handler3", 0, "field1"),
		},
	},
	expectQueryCount: 1,
	expectStatus:     http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: mustParseReference("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"field1-1": fieldSelectHandleInfo{
				HandlerId: "handler1",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: mustParseReference("cs:precise/wordpress-42"),
			},
			"field2": fieldSelectHandleInfo{
				HandlerId: "handler2",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: mustParseReference("cs:precise/wordpress-42"),
			},
			"field1-2": fieldSelectHandleInfo{
				HandlerId: "handler3",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: mustParseReference("cs:precise/wordpress-42"),
			},
		},
	},
}, {
	about:  "meta/any, includes with additional path elements",
	urlStr: "/precise/wordpress-42/meta/any?include=item1/foo&include=item2/bar&include=item1",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"item1/": fieldSelectHandler("handler1", 0, "field1"),
			"item2/": fieldSelectHandler("handler2", 0, "field2"),
			"item1":  fieldSelectHandler("handler3", 0, "field3"),
		},
	},
	expectQueryCount: 1,
	expectStatus:     http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: mustParseReference("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"item1/foo": fieldSelectHandleInfo{
				HandlerId: "handler1",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id:   mustParseReference("cs:precise/wordpress-42"),
				Path: "/foo",
			},
			"item2/bar": fieldSelectHandleInfo{
				HandlerId: "handler2",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id:   mustParseReference("cs:precise/wordpress-42"),
				Path: "/bar",
			},
			"item1": fieldSelectHandleInfo{
				HandlerId: "handler3",
				Method:    "GET",
				Doc: fieldSelectQueryInfo{
					Id:       mustParseReference("cs:precise/wordpress-42"),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id: mustParseReference("cs:precise/wordpress-42"),
			},
		},
	},
}, {
	about:  "meta/any, nil metadata omitted",
	urlStr: "/precise/wordpress-42/meta/any?include=ok&include=nil",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"ok":       testMetaHandler,
			"nil":      constMetaHandler(nil),
			"typednil": constMetaHandler((*struct{})(nil)),
		},
	},
	expectStatus: http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: mustParseReference("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"ok": metaHandlerTestResp{
				Method:   "GET",
				CharmURL: "cs:precise/wordpress-42",
			},
		},
	},
}, {
	about:  "meta/any, handler returns error with cause",
	urlStr: "/precise/wordpress-42/meta/any?include=error",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"error": errorMetaHandler(errgo.WithCausef(nil, params.ErrorCode("foo"), "a message")),
		},
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    "foo",
		Message: "a message",
	},
}, {
	about:  "bulk meta handler, single id",
	urlStr: "/meta/foo?id=precise/wordpress-42",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"precise/wordpress-42": {
			CharmURL: "cs:precise/wordpress-42",
			Method:   "GET",
		},
	},
}, {
	about:  "bulk meta handler, several ids",
	urlStr: "/meta/foo?id=precise/wordpress-42&id=quantal/foo-32",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"precise/wordpress-42": {
			CharmURL: "cs:precise/wordpress-42",
			Method:   "GET",
		},
		"quantal/foo-32": {
			CharmURL: "cs:quantal/foo-32",
			Method:   "GET",
		},
	},
}, {
	about:  "bulk meta/any handler, several ids",
	urlStr: "/meta/any?id=precise/wordpress-42&id=quantal/foo-32&include=foo&include=bar/something",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler,
			"bar/": testMetaHandler,
		},
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]params.MetaAnyResponse{
		"precise/wordpress-42": {
			Id: mustParseReference("cs:precise/wordpress-42"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					CharmURL: "cs:precise/wordpress-42",
					Method:   "GET",
				},
				"bar/something": metaHandlerTestResp{
					CharmURL: "cs:precise/wordpress-42",
					Method:   "GET",
					Path:     "/something",
				},
			},
		},
		"quantal/foo-32": {
			Id: mustParseReference("cs:quantal/foo-32"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					Method:   "GET",
					CharmURL: "cs:quantal/foo-32",
				},
				"bar/something": metaHandlerTestResp{
					CharmURL: "cs:quantal/foo-32",
					Method:   "GET",
					Path:     "/something",
				},
			},
		},
	},
}, {
	about:  "bulk meta handler with unresolved id",
	urlStr: "/meta/foo/bar?id=wordpress",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler,
		},
	},
	resolveURL:   newResolveURL("precise", 100),
	expectStatus: http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"wordpress": {
			CharmURL: "cs:precise/wordpress-100",
			Method:   "GET",
			Path:     "/bar",
		},
	},
}, {
	about:  "bulk meta handler with extra flags",
	urlStr: "/meta/foo/bar?id=wordpress&arble=bletch&z=w&z=p",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler,
		},
	},
	resolveURL:   newResolveURL("precise", 100),
	expectStatus: http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"wordpress": {
			CharmURL: "cs:precise/wordpress-100",
			Path:     "/bar",
			Method:   "GET",
			Flags: url.Values{
				"arble": {"bletch"},
				"z":     {"w", "p"},
			},
		},
	},
}, {
	about:  "bulk meta handler with no ids",
	urlStr: "/meta/foo/bar",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler,
		},
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "no ids specified in meta request",
	},
}, {
	about:  "bulk meta handler with unresolvable id",
	urlStr: "/meta/foo?id=unresolved&id=precise/wordpress-23",
	resolveURL: func(url *charm.Reference) error {
		if url.Name == "unresolved" {
			return params.ErrNotFound
		}
		return nil
	},
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"precise/wordpress-23": {
			CharmURL: "cs:precise/wordpress-23",
			Method:   "GET",
		},
	},
}, {
	about:  "bulk meta handler with id resolution error",
	urlStr: "/meta/foo?id=resolveerror&id=precise/wordpress-23",
	resolveURL: func(url *charm.Reference) error {
		if url.Name == "resolveerror" {
			return errgo.Newf("an error")
		}
		return nil
	},
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler,
		},
	},
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "an error",
	},
}, {
	about:  "bulk meta handler with some nil data",
	urlStr: "/meta/foo?id=bundle/something-24&id=precise/wordpress-23",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": selectiveIdHandler(map[string]interface{}{
				"cs:bundle/something-24": "bundlefoo",
			}),
		},
	},
	expectStatus: http.StatusOK,
	expectBody: map[string]string{
		"bundle/something-24": "bundlefoo",
	},
}}

// newResolveURL returns a URL resolver that resolves
// unspecified series and revision to the given series
// and revision.
func newResolveURL(series string, revision int) func(*charm.Reference) error {
	return func(url *charm.Reference) error {
		if url.Series == "" {
			url.Series = series
		}
		if url.Revision == -1 {
			url.Revision = revision
		}
		return nil
	}
}

func resolveURLError(err error) func(*charm.Reference) error {
	return func(*charm.Reference) error {
		return err
	}
}

func noResolveURL(*charm.Reference) error {
	return nil
}

func (s *RouterSuite) TestRouterGet(c *gc.C) {
	for i, test := range routerGetTests {
		c.Logf("test %d: %s", i, test.about)
		resolve := noResolveURL
		if test.resolveURL != nil {
			resolve = test.resolveURL
		}
		router := New(&test.handlers, resolve)
		// Note that fieldSelectHandler increments queryCount each time
		// a query is made.
		queryCount = 0
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      router,
			URL:          test.urlStr,
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
		c.Assert(queryCount, gc.Equals, test.expectQueryCount)
	}
}

func (s *RouterSuite) TestRouterMethodsThatPassThroughUnresolvedId(c *gc.C) {
	// We omit HEAD because net/http does not send back the body
	// when doing a HEAD request. Given that there's no actual logic
	// in the code that is HEAD specific, it seems reasonable to drop the this case.
	// TODO(rog) Refactor the test to make the handler store the id in a local variable
	// rather than relying on the returned body.

	alwaysResolves := map[string]bool{
		"POST":   false,
		"PUT":    false,
		"GET":    true,
		"DELETE": true,
	}

	// Go 1.2 will not allow the form to be parsed unless
	// the content type header is set.
	header := http.Header{
		"Content-Type": {"application/zip"},
	}

	for method, resolves := range alwaysResolves {
		c.Logf("test %s", method)
		// First try with a metadata handler. This should always resolve,
		// regardless of the method.
		handlers := Handlers{
			Id: map[string]IdHandler{
				"idhandler": testIdHandler,
			},
			Meta: map[string]BulkIncludeHandler{
				"metahandler": testMetaHandler,
			},
		}
		router := New(&handlers, newResolveURL("series", 1234))
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler: router,
			Method:  method,
			URL:     "/wordpress/meta/metahandler",
			Body:    strings.NewReader(""),
			Header:  header,
			ExpectBody: &metaHandlerTestResp{
				CharmURL: "cs:series/wordpress-1234",
				Method:   method,
			},
		})

		// Then try with an id handler. This should only resolve
		// for some methods.
		var resp idHandlerTestResp
		if resolves {
			resp.CharmURL = "cs:series/wordpress-1234"
		} else {
			resp.CharmURL = "cs:wordpress"
		}
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:    router,
			Method:     method,
			Body:       strings.NewReader(""),
			Header:     header,
			URL:        "/wordpress/idhandler",
			ExpectBody: resp,
		})
	}
}

var getMetadataTests = []struct {
	id           string
	includes     []string
	expectResult map[string]interface{}
	expectError  string
}{{
	id:           "precise/wordpress-34",
	includes:     []string{},
	expectResult: map[string]interface{}{},
}, {
	id:       "~rog/precise/wordpress-2",
	includes: []string{"item1", "item2", "test"},
	expectResult: map[string]interface{}{
		"item1": fieldSelectHandleInfo{
			HandlerId: "handler1",
			Doc: fieldSelectQueryInfo{
				Id:       mustParseReference("cs:~rog/precise/wordpress-2"),
				Selector: map[string]int{"item1": 1, "item2": 1},
			},
			Id:     mustParseReference("cs:~rog/precise/wordpress-2"),
			Method: "GET",
		},
		"item2": fieldSelectHandleInfo{
			HandlerId: "handler2",
			Doc: fieldSelectQueryInfo{
				Id:       mustParseReference("cs:~rog/precise/wordpress-2"),
				Selector: map[string]int{"item1": 1, "item2": 1},
			},
			Id:     mustParseReference("cs:~rog/precise/wordpress-2"),
			Method: "GET",
		},
		"test": &metaHandlerTestResp{
			CharmURL: "cs:~rog/precise/wordpress-2",
			Method:   "GET",
		},
	},
}, {
	id:          "~rog/precise/wordpress-2",
	includes:    []string{"mistaek"},
	expectError: `unrecognized metadata name "mistaek"`,
}}

func (s *RouterSuite) TestGetMetadata(c *gc.C) {
	for i, test := range getMetadataTests {
		c.Logf("test %d: %q", i, test.includes)
		router := New(&Handlers{
			Meta: map[string]BulkIncludeHandler{
				"item1": fieldSelectHandler("handler1", 0, "item1"),
				"item2": fieldSelectHandler("handler2", 0, "item2"),
				"test":  testMetaHandler,
			},
		}, noResolveURL)
		id := mustParseReference(test.id)
		result, err := router.GetMetadata(id, test.includes)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(result, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(result, jc.DeepEquals, test.expectResult)
	}
}

var splitIdTests = []struct {
	path        string
	expectURL   string
	expectError string
}{{
	path:      "precise/wordpress-23",
	expectURL: "cs:precise/wordpress-23",
}, {
	path:      "~user/precise/wordpress-23",
	expectURL: "cs:~user/precise/wordpress-23",
}, {
	path:      "wordpress",
	expectURL: "cs:wordpress",
}, {
	path:      "~user/wordpress",
	expectURL: "cs:~user/wordpress",
}, {
	path:        "",
	expectError: `charm URL has invalid charm name: ""`,
}, {
	path:        "~foo-bar-/wordpress",
	expectError: `charm URL has invalid user name: "~foo-bar-/wordpress"`,
}}

func (s *RouterSuite) TestSplitId(c *gc.C) {
	for i, test := range splitIdTests {
		c.Logf("test %d: %s", i, test.path)
		url, rest, err := splitId(test.path)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(url, gc.IsNil)
			c.Assert(rest, gc.Equals, "")
			continue
		}
		c.Assert(url.String(), gc.Equals, test.expectURL)
		c.Assert(rest, gc.Equals, "")

		url, rest, err = splitId(test.path + "/some/more")
		c.Assert(err, gc.Equals, nil)
		c.Assert(url.String(), gc.Equals, test.expectURL)
		c.Assert(rest, gc.Equals, "/some/more")
	}
}

var handlerKeyTests = []struct {
	path       string
	expectKey  string
	expectRest string
}{{
	path:       "/foo/bar",
	expectKey:  "foo/",
	expectRest: "/bar",
}, {
	path:       "/foo",
	expectKey:  "foo",
	expectRest: "",
}, {
	path:       "/foo/bar/baz",
	expectKey:  "foo/",
	expectRest: "/bar/baz",
}, {
	path:       "/foo/",
	expectKey:  "foo",
	expectRest: "",
}, {
	path:       "foo/",
	expectKey:  "foo",
	expectRest: "",
}}

func (s *RouterSuite) TestHandlerKey(c *gc.C) {
	for i, test := range handlerKeyTests {
		c.Logf("test %d: %s", i, test.path)
		key, rest := handlerKey(test.path)
		c.Assert(key, gc.Equals, test.expectKey)
		c.Assert(rest, gc.Equals, test.expectRest)
	}
}

var splitPathTests = []struct {
	path       string
	index      int
	expectElem string
	expectRest string
}{{
	path:       "/foo/bar",
	expectElem: "foo",
	expectRest: "/bar",
}, {
	path:       "foo/bar",
	expectElem: "foo",
	expectRest: "/bar",
}, {
	path:       "foo/",
	expectElem: "foo",
	expectRest: "/",
}, {
	path:       "/foo/bar/baz",
	expectElem: "foo",
	expectRest: "/bar/baz",
}, {
	path:       "/foo",
	expectElem: "foo",
	expectRest: "",
}, {
	path:       "/foo/bar/baz",
	index:      4,
	expectElem: "bar",
	expectRest: "/baz",
}}

func (s *RouterSuite) TestSplitPath(c *gc.C) {
	for i, test := range splitPathTests {
		c.Logf("test %d: %s", i, test.path)
		elem, index := splitPath(test.path, test.index)
		c.Assert(elem, gc.Equals, test.expectElem)
		c.Assert(index, jc.LessThan, len(test.path)+1)
		c.Assert(test.path[index:], gc.Equals, test.expectRest)
	}
}

func (s *RouterSuite) TestWriteJSON(c *gc.C) {
	rec := httptest.NewRecorder()
	type Number struct {
		N int
	}
	err := WriteJSON(rec, http.StatusTeapot, Number{1234})
	c.Assert(err, gc.IsNil)
	c.Assert(rec.Code, gc.Equals, http.StatusTeapot)
	c.Assert(rec.Body.String(), gc.Equals, `{"N":1234}`)
	c.Assert(rec.Header().Get("content-type"), gc.Equals, "application/json")
}

func (s *RouterSuite) TestWriteError(c *gc.C) {
	rec := httptest.NewRecorder()
	WriteError(rec, errgo.Newf("an error"))
	var errResp params.Error
	err := json.Unmarshal(rec.Body.Bytes(), &errResp)
	c.Assert(err, gc.IsNil)
	c.Assert(errResp, gc.Equals, params.Error{Message: "an error"})
	c.Assert(rec.Code, gc.Equals, http.StatusInternalServerError)

	rec = httptest.NewRecorder()
	errResp0 := params.Error{
		Message: "a message",
		Code:    "some code",
	}
	WriteError(rec, &errResp0)
	var errResp1 params.Error
	err = json.Unmarshal(rec.Body.Bytes(), &errResp1)
	c.Assert(err, gc.IsNil)
	c.Assert(errResp1, gc.Equals, errResp0)
	c.Assert(rec.Code, gc.Equals, http.StatusInternalServerError)
}

func (s *RouterSuite) TestServeMux(c *gc.C) {
	mux := NewServeMux()
	mux.Handle("/data", HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
		return Foo{"hello"}, nil
	}))
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:    mux,
		URL:        "/data",
		ExpectBody: Foo{"hello"},
	})
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      mux,
		URL:          "/foo",
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: "not found",
			Code:    params.ErrNotFound,
		},
	})
}

var handlerTests = []struct {
	about        string
	handler      http.Handler
	urlStr       string
	expectStatus int
	expectBody   interface{}
}{{
	about: "handleErrors, normal error",
	handler: HandleErrors(func(http.ResponseWriter, *http.Request) error {
		return errgo.Newf("an error")
	}),
	urlStr:       "",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "an error",
	},
}, {
	about: "handleErrors, error with code",
	handler: HandleErrors(func(http.ResponseWriter, *http.Request) error {
		return &params.Error{
			Message: "something went wrong",
			Code:    "snafu",
		}
	}),
	urlStr:       "",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "something went wrong",
		Code:    "snafu",
	},
}, {
	about: "handleErrors, no error",
	handler: HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		w.WriteHeader(http.StatusTeapot)
		return nil
	}),
	expectStatus: http.StatusTeapot,
}, {
	about: "handleErrors, params error",
	handler: HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		return params.ErrMetadataNotFound
	}),
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Message: "metadata not found",
		Code:    params.ErrMetadataNotFound,
	},
}, {
	about: "handleErrors, wrapped params error",
	handler: HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		err := params.ErrMetadataNotFound
		return errgo.NoteMask(err, "annotation", errgo.Is(params.ErrMetadataNotFound))
	}),
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Message: "annotation: metadata not found",
		Code:    params.ErrMetadataNotFound,
	},
}, {
	about: "handleErrors: error - bad request",
	handler: HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		return params.ErrBadRequest
	}),
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Message: "bad request",
		Code:    params.ErrBadRequest,
	},
}, {
	about: "handleErrors: error - forbidden",
	handler: HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		return params.ErrForbidden
	}),
	expectStatus: http.StatusForbidden,
	expectBody: params.Error{
		Message: "forbidden",
		Code:    params.ErrForbidden,
	},
}, {
	about: "handleJSON, normal case",
	handler: HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
		return Foo{"hello"}, nil
	}),
	expectStatus: http.StatusOK,
	expectBody:   Foo{"hello"},
}, {
	about: "handleJSON, error case",
	handler: HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
		return nil, errgo.Newf("an error")
	}),
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "an error",
	},
}, {
	about:        "NotFoundHandler",
	handler:      NotFoundHandler(),
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Message: "not found",
		Code:    params.ErrNotFound,
	},
}}

type Foo struct {
	S string
}

type ReqInfo struct {
	Path string
	Form url.Values `json:",omitempty"`
}

func (s *RouterSuite) TestHandlers(c *gc.C) {
	for i, test := range handlerTests {
		c.Logf("test %d: %s", i, test.about)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      test.handler,
			URL:          "",
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

func errorIdHandler(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	return errgo.Newf("errorIdHandler error")
}

type idHandlerTestResp struct {
	CharmURL string
	Path     string
}

func testIdHandler(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
	WriteJSON(w, http.StatusOK, idHandlerTestResp{
		CharmURL: charmId.String(),
		Path:     req.URL.Path,
	})
	return nil
}

type metaHandlerTestResp struct {
	CharmURL string
	Path     string
	Method   string
	Flags    url.Values
}

var testMetaHandler = SingleIncludeHandler(
	func(id *charm.Reference, path string, method string, flags url.Values) (interface{}, error) {
		if len(flags) == 0 {
			flags = nil
		}
		return &metaHandlerTestResp{
			CharmURL: id.String(),
			Path:     path,
			Method:   method,
			Flags:    flags,
		}, nil
	},
)

// constMetaHandler returns a handler that always returns the given
// value.
func constMetaHandler(val interface{}) BulkIncludeHandler {
	return SingleIncludeHandler(
		func(id *charm.Reference, path string, method string, flags url.Values) (interface{}, error) {
			return val, nil
		},
	)
}

func errorMetaHandler(err error) BulkIncludeHandler {
	return SingleIncludeHandler(
		func(id *charm.Reference, path string, method string, flags url.Values) (interface{}, error) {
			return nil, err
		},
	)
}

type fieldSelectQueryInfo struct {
	Id       *charm.Reference
	Selector map[string]int
}

type fieldSelectHandleInfo struct {
	HandlerId string
	Doc       fieldSelectQueryInfo
	Id        *charm.Reference
	Path      string
	Method    string
	Flags     url.Values
}

var queryCount int32

// fieldSelectHandler returns a BulkIncludeHandler that returns
// information about the call for testing purposes.
// When the handler is invoked, it returns a fieldSelectHandleInfo value
// with the given handlerId. Key holds the grouping key,
// and fields holds the fields to select.
func fieldSelectHandler(handlerId string, key interface{}, fields ...string) BulkIncludeHandler {
	query := func(id *charm.Reference, selector map[string]int) (interface{}, error) {
		atomic.AddInt32(&queryCount, 1)
		return fieldSelectQueryInfo{
			Id:       id,
			Selector: selector,
		}, nil
	}
	handle := func(doc interface{}, id *charm.Reference, path string, method string, flags url.Values) (interface{}, error) {
		if len(flags) == 0 {
			flags = nil
		}
		return fieldSelectHandleInfo{
			HandlerId: handlerId,
			Doc:       doc.(fieldSelectQueryInfo),
			Id:        id,
			Path:      path,
			Method:    method,
			Flags:     flags,
		}, nil
	}
	return FieldIncludeHandler(key, query, fields, handle)
}

// selectiveIdHandler handles metadata by returning the
// data found in the map for the requested id.
func selectiveIdHandler(m map[string]interface{}) BulkIncludeHandler {
	return SingleIncludeHandler(func(id *charm.Reference, path string, method string, flags url.Values) (interface{}, error) {
		return m[id.String()], nil
	})
}

func mustParseReference(url string) *charm.Reference {
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}
