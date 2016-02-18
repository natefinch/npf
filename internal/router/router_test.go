// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package router // import "gopkg.in/juju/charmstore.v5-unstable/internal/router"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/juju/httprequest"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/httpbakery"

	"gopkg.in/juju/charmstore.v5-unstable/audit"
)

type RouterSuite struct {
	jujutesting.IsolationSuite
}

var _ = gc.Suite(&RouterSuite{})

var newResolvedURL = MustNewResolvedURL

var routerGetTests = []struct {
	about                     string
	handlers                  Handlers
	urlStr                    string
	expectStatus              int
	expectBody                interface{}
	expectQueryCount          int32
	expectWillIncludeMetadata []string
	resolveURL                func(*charm.URL) (*ResolvedURL, error)
	authorize                 func(*ResolvedURL, *http.Request) error
	exists                    func(*ResolvedURL, *http.Request) (bool, error)
}{{
	about: "global handler",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo": HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
				return ReqInfo{
					Method: req.Method,
					Path:   req.URL.Path,
					Form:   req.Form,
				}, nil
			}),
		},
	},
	urlStr:       "/foo",
	expectStatus: http.StatusOK,
	expectBody: ReqInfo{
		Method: "GET",
		Path:   "",
	},
}, {
	about: "global handler with sub-path and flags",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo/bar/": HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
				return ReqInfo{
					Method: req.Method,
					Path:   req.URL.Path,
					Form:   req.Form,
				}, nil
			}),
		},
	},
	urlStr:       "/foo/bar/a/b?a=1&b=two",
	expectStatus: http.StatusOK,
	expectBody: ReqInfo{
		Path:   "/a/b",
		Method: "GET",
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
		Method:   "GET",
		CharmURL: "cs:precise/wordpress-34",
	},
}, {
	about: "development id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/development/trusty/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:development/trusty/wordpress-34",
	},
}, {
	about: "id handler with invalid channel",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/bad-wolf/trusty/wordpress-34/foo",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
	},
}, {
	about: "windows id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/win81/visualstudio-2012/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:win81/visualstudio-2012",
	},
}, {
	about: "windows development id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/development/win81/visualstudio-2012/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:development/win81/visualstudio-2012",
	},
}, {
	about: "wily id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/wily/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:wily/wordpress-34",
	},
}, {
	about: "id handler with no series in id",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:wordpress-34",
	},
}, {
	about: "id handler with no revision in id",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/precise/wordpress/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:precise/wordpress",
	},
}, {
	about: "id handler with channel and name only",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/development/wordpress/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:development/wordpress",
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
		Method:   "GET",
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
		Method:   "GET",
		CharmURL: "cs:~joe/precise/wordpress-34",
	},
}, {
	about: "wily handler with user",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/~joe/wily/wordpress-34/foo",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:~joe/wily/wordpress-34",
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
		Method:   "GET",
		CharmURL: "cs:~joe/precise/wordpress-34",
		Path:     "/blah/arble",
	},
}, {
	about: "development id handler with user and extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:       "/~joe/development/precise/wordpress-34/foo/blah/arble",
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:~joe/development/precise/wordpress-34",
		Path:     "/blah/arble",
	},
}, {
	about: "id handler with user, invalid channel and extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:       "/~joe/bad-wolf/precise/wordpress-34/foo/blah/arble",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
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
			"foo": func(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
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
			"foo": func(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
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
	about: "id with unspecified series and revision, not resolved",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:       "/~joe/wordpress/foo",
	resolveURL:   resolveTo("precise", 34),
	expectStatus: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "GET",
		CharmURL: "cs:~joe/wordpress",
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
			"foo":  testMetaHandler(0),
			"bar":  testMetaHandler(1),
			"bar/": testMetaHandler(2),
			"foo/": testMetaHandler(3),
			"baz":  testMetaHandler(4),
		},
	},
	urlStr:       "/precise/wordpress-42/meta",
	expectStatus: http.StatusOK,
	expectBody:   []string{"bar", "baz", "foo"},
}, {
	about: "meta list at root",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler(0),
			"bar":  testMetaHandler(1),
			"bar/": testMetaHandler(2),
			"foo/": testMetaHandler(3),
			"baz":  testMetaHandler(4),
		},
	},
	urlStr:       "/meta",
	expectStatus: http.StatusOK,
	expectBody:   []string{"bar", "baz", "foo"},
}, {
	about: "meta list at root with trailing /",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler(0),
			"bar":  testMetaHandler(1),
			"bar/": testMetaHandler(2),
			"foo/": testMetaHandler(3),
			"baz":  testMetaHandler(4),
		},
	},
	urlStr:       "/meta/",
	expectStatus: http.StatusOK,
	expectBody:   []string{"bar", "baz", "foo"},
}, {
	about: "meta handler",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr: "/precise/wordpress-42/meta/foo",
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: &metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
	},
}, {
	about: "meta handler with additional elements",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler(0),
		},
	},
	urlStr: "/precise/wordpress-42/meta/foo/bar/baz",
	expectWillIncludeMetadata: []string{"foo/bar/baz"},
	expectStatus:              http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Path:     "/bar/baz",
	},
}, {
	about: "meta handler with params",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr: "/precise/wordpress-42/meta/foo?one=a&two=b&one=c",
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Flags: url.Values{
			"one": {"a", "c"},
			"two": {"b"},
		},
	},
}, {
	about:  "meta handler that's not found",
	urlStr: "/precise/wordpress-42/meta/foo",
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `unknown metadata "foo"`,
	},
}, {
	about:  "meta sub-handler that's not found",
	urlStr: "/precise/wordpress-42/meta/foo/bar",
	expectWillIncludeMetadata: []string{"foo/bar"},
	expectStatus:              http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `unknown metadata "foo/bar"`,
	},
}, {
	about: "meta handler with nil data",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": constMetaHandler(nil),
		},
	},
	urlStr: "/precise/wordpress-42/meta/foo",
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusNotFound,
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
	urlStr: "/precise/wordpress-42/meta/foo",
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusNotFound,
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
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectQueryCount:          1,
	expectBody: fieldSelectHandleGetInfo{
		HandlerId: "handler1",
		Doc: fieldSelectQueryInfo{
			Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
			Selector: map[string]int{"field1": 1, "field2": 1},
		},
		Id: newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
	},
}, {
	about:  "meta handler returning error with code",
	urlStr: "/precise/wordpress-42/meta/foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": errorMetaHandler(errgo.WithCausef(nil, params.ErrorCode("arble"), "a message")),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    "arble",
		Message: "a message",
	},
}, {
	about:  "unauthorized meta handler",
	urlStr: "/precise/wordpress-42/meta/foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 neverAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusUnauthorized,
	expectBody: params.Error{
		Code:    params.ErrUnauthorized,
		Message: "bad wolf",
	},
}, {
	about:        "meta/any, no includes, id exists",
	urlStr:       "/precise/wordpress-42/meta/any",
	expectStatus: http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
	},
}, {
	about:        "meta/any, no includes, id does not exist",
	urlStr:       "/precise/wordpress/meta/any",
	resolveURL:   resolveURLError(params.ErrNotFound),
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "not found",
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
	expectWillIncludeMetadata: []string{"field1-1", "field2", "field1-2"},
	expectQueryCount:          1,
	expectStatus:              http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"field1-1": fieldSelectHandleGetInfo{
				HandlerId: "handler1",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
			},
			"field2": fieldSelectHandleGetInfo{
				HandlerId: "handler2",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
			},
			"field1-2": fieldSelectHandleGetInfo{
				HandlerId: "handler3",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1},
				},
				Id: newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
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
	expectWillIncludeMetadata: []string{"item1/foo", "item2/bar", "item1"},
	expectQueryCount:          1,
	expectStatus:              http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"item1/foo": fieldSelectHandleGetInfo{
				HandlerId: "handler1",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id:   newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
				Path: "/foo",
			},
			"item2/bar": fieldSelectHandleGetInfo{
				HandlerId: "handler2",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id:   newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
				Path: "/bar",
			},
			"item1": fieldSelectHandleGetInfo{
				HandlerId: "handler3",
				Doc: fieldSelectQueryInfo{
					Id:       newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
					Selector: map[string]int{"field1": 1, "field2": 1, "field3": 1},
				},
				Id: newResolvedURL("cs:~charmers/precise/wordpress-42", 42),
			},
		},
	},
}, {
	about:  "meta/any, nil metadata omitted",
	urlStr: "/precise/wordpress-42/meta/any?include=ok&include=nil",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"ok":       testMetaHandler(0),
			"nil":      constMetaHandler(nil),
			"typednil": constMetaHandler((*struct{})(nil)),
		},
	},
	expectWillIncludeMetadata: []string{"ok", "nil"},
	expectStatus:              http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"ok": metaHandlerTestResp{
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
	expectWillIncludeMetadata: []string{"error"},
	expectStatus:              http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    "foo",
		Message: "a message",
	},
}, {
	about:  "bulk meta handler, single id",
	urlStr: "/meta/foo?id=precise/wordpress-42",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"precise/wordpress-42": {
			CharmURL: "cs:precise/wordpress-42",
		},
	},
}, {
	about:  "bulk meta handler, single id with invalid channel",
	urlStr: "/meta/foo?id=~user/bad-wolf/wily/wordpress-42",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `bad request: charm or bundle URL has invalid form: "~user/bad-wolf/wily/wordpress-42"`,
	},
}, {
	about:  "bulk meta handler, several ids",
	urlStr: "/meta/foo?id=precise/wordpress-42&id=utopic/foo-32&id=development/django",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"precise/wordpress-42": {
			CharmURL: "cs:precise/wordpress-42",
		},
		"utopic/foo-32": {
			CharmURL: "cs:utopic/foo-32",
		},
		"development/django": {
			CharmURL: "cs:precise/django-0",
		},
	},
}, {
	about:  "bulk meta/any handler, several ids",
	urlStr: "/meta/any?id=precise/wordpress-42&id=utopic/foo-32&id=development/django-47&include=foo&include=bar/something",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler(0),
			"bar/": testMetaHandler(1),
		},
	},
	expectWillIncludeMetadata: []string{"foo", "bar/something"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]params.MetaAnyResponse{
		"precise/wordpress-42": {
			Id: charm.MustParseURL("cs:precise/wordpress-42"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					CharmURL: "cs:precise/wordpress-42",
				},
				"bar/something": metaHandlerTestResp{
					CharmURL: "cs:precise/wordpress-42",
					Path:     "/something",
				},
			},
		},
		"utopic/foo-32": {
			Id: charm.MustParseURL("cs:utopic/foo-32"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					CharmURL: "cs:utopic/foo-32",
				},
				"bar/something": metaHandlerTestResp{
					CharmURL: "cs:utopic/foo-32",
					Path:     "/something",
				},
			},
		},
		"development/django-47": {
			Id: charm.MustParseURL("cs:precise/django-47"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					CharmURL: "cs:precise/django-47",
				},
				"bar/something": metaHandlerTestResp{
					CharmURL: "cs:precise/django-47",
					Path:     "/something",
				},
			},
		},
	},
}, {
	about:  "bulk meta/any handler, several ids, invalid channel",
	urlStr: "/meta/any?id=precise/wordpress-42&id=staging/trusty/django&include=foo&include=bar/something",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler(0),
			"bar/": testMetaHandler(1),
		},
	},
	expectWillIncludeMetadata: []string{"foo", "bar/something"},
	expectStatus:              http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `bad request: charm or bundle URL has invalid form: "staging/trusty/django"`,
	},
}, {
	about:  "bulk meta/any handler, discharge required",
	urlStr: "/meta/any?id=precise/wordpress-42&include=foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 dischargeRequiredAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "discharge required",
	},
}, {
	about:  "bulk meta/any handler, discharge required, ignore authorization",
	urlStr: "/meta/any?id=precise/wordpress-42&include=foo&ignore-auth=1",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 dischargeRequiredAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody:                map[string]params.MetaAnyResponse{},
}, {
	about:  "bulk meta/any handler, some unauthorized, ignore authorization",
	urlStr: "/meta/any?id=precise/wordpress-42&id=utopic/foo-32&include=foo&ignore-auth=1",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 dischargeRequiredAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]params.MetaAnyResponse{
		"utopic/foo-32": {
			Id: charm.MustParseURL("cs:utopic/foo-32"),
			Meta: map[string]interface{}{
				"foo": metaHandlerTestResp{
					CharmURL: "cs:utopic/foo-32",
				},
			},
		},
	},
}, {
	about:  "bulk meta/any handler, unauthorized",
	urlStr: "/meta/any?id=precise/wordpress-42&include=foo",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 neverAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "bad wolf",
	},
}, {
	about:  "bulk meta/any handler, unauthorized, ignore authorization",
	urlStr: "/meta/any?id=precise/wordpress-42&include=foo&ignore-auth=1",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	authorize:                 neverAuthorize,
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody:                map[string]params.MetaAnyResponse{},
}, {
	about:        "bulk meta/any handler, invalid ignore-auth flag",
	urlStr:       "/meta/any?id=precise/wordpress-42&include=foo&ignore-auth=meh",
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `bad request: unexpected bool value "meh" (must be "0" or "1")`,
	},
}, {
	about:  "bulk meta handler with unresolved id",
	urlStr: "/meta/foo/bar?id=wordpress",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler(0),
		},
	},
	resolveURL:                resolveTo("precise", 100),
	expectWillIncludeMetadata: []string{"foo/bar"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"wordpress": {
			CharmURL: "cs:precise/wordpress-100",
			Path:     "/bar",
		},
	},
}, {
	about:  "bulk meta handler with extra flags",
	urlStr: "/meta/foo/bar?id=wordpress&arble=bletch&z=w&z=p",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler(0),
		},
	},
	resolveURL:                resolveTo("precise", 100),
	expectWillIncludeMetadata: []string{"foo/bar"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"wordpress": {
			CharmURL: "cs:precise/wordpress-100",
			Path:     "/bar",
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
			"foo/": testMetaHandler(0),
		},
	},
	expectStatus: http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: "no ids specified in meta request",
	},
}, {
	about:  "bulk meta handler with unresolvable id",
	urlStr: "/meta/foo?id=unresolved&id=~foo/precise/wordpress-23",
	resolveURL: func(url *charm.URL) (*ResolvedURL, error) {
		if url.Name == "unresolved" {
			return nil, params.ErrNotFound
		}
		return &ResolvedURL{URL: *url, PromulgatedRevision: 99}, nil
	},
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]metaHandlerTestResp{
		"~foo/precise/wordpress-23": {
			CharmURL: "cs:precise/wordpress-99",
		},
	},
}, {
	about:  "bulk meta handler with id resolution error",
	urlStr: "/meta/foo?id=resolveerror&id=precise/wordpress-23",
	resolveURL: func(url *charm.URL) (*ResolvedURL, error) {
		if url.Name == "resolveerror" {
			return nil, errgo.Newf("an error")
		}
		return &ResolvedURL{URL: *url}, nil
	},
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusInternalServerError,
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
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]string{
		"bundle/something-24": "bundlefoo",
	},
}, {
	about:  "bulk meta handler with entity not found",
	urlStr: "/meta/foo?id=bundle/something-24&id=precise/wordpress-23",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": SingleIncludeHandler(func(id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
				if id.URL.Revision == 23 {
					return nil, errgo.WithCausef(nil, params.ErrNotFound, "")
				}
				return "something", nil
			}),
		},
	},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusOK,
	expectBody: map[string]string{
		"bundle/something-24": "something",
	},
}, {
	about:        "meta request with invalid entity reference",
	urlStr:       "/robots.txt/meta/any",
	handlers:     Handlers{},
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `not found: URL has invalid charm or bundle name: "robots.txt"`,
	},
}, {
	about:                     "bulk meta handler, invalid id",
	urlStr:                    "/meta/foo?id=robots.txt",
	handlers:                  Handlers{},
	expectWillIncludeMetadata: []string{"foo"},
	expectStatus:              http.StatusBadRequest,
	expectBody: params.Error{
		Code:    params.ErrBadRequest,
		Message: `bad request: URL has invalid charm or bundle name: "robots.txt"`,
	},
}}

// resolveTo returns a URL resolver that resolves
// unspecified series and revision to the given series
// and revision.
func resolveTo(series string, revision int) func(*charm.URL) (*ResolvedURL, error) {
	return func(url *charm.URL) (*ResolvedURL, error) {
		var rurl ResolvedURL
		rurl.URL = *url
		if url.Series == "" {
			rurl.URL.Series = series
		}
		if url.Revision == -1 {
			rurl.URL.Revision = revision
		}
		if url.User == "" {
			rurl.URL.User = "charmers"
			rurl.PromulgatedRevision = revision
		}
		return &rurl, nil
	}
}

func resolveURLError(err error) func(*charm.URL) (*ResolvedURL, error) {
	return func(*charm.URL) (*ResolvedURL, error) {
		return nil, err
	}
}

func alwaysResolveURL(u *charm.URL) (*ResolvedURL, error) {
	u1 := *u
	if u1.Series == "" {
		u1.Series = "precise"
	}
	if u1.Revision == -1 {
		u1.Revision = 0
	}
	promRev := -1
	if u1.User == "" {
		u1.User = "charmers"
		promRev = u1.Revision
	}
	return newResolvedURL(u1.String(), promRev), nil
}

func (s *RouterSuite) TestRouterGet(c *gc.C) {
	for i, test := range routerGetTests {
		c.Logf("test %d: %s", i, test.about)
		ctxt := alwaysContext
		if test.resolveURL != nil {
			ctxt.resolveURL = test.resolveURL
		}
		if test.authorize != nil {
			ctxt.authorizeURL = test.authorize
		}
		resolved := false
		var includedMetadata []string
		origResolve := ctxt.resolveURL
		ctxt.resolveURL = func(id *charm.URL) (*ResolvedURL, error) {
			resolved = true
			return origResolve(id)
		}
		ctxt.willIncludeMetadata = func(incs []string) {
			if resolved {
				c.Errorf("ResolveURL called before WillIncludeMetadata")
			}
			includedMetadata = incs
		}
		router := New(&test.handlers, ctxt)
		// Note that fieldSelectHandler increments queryCount each time
		// a query is made.
		queryCount = 0
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      router,
			URL:          test.urlStr,
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
		c.Assert(queryCount, gc.Equals, test.expectQueryCount)
		c.Assert(includedMetadata, jc.DeepEquals, test.expectWillIncludeMetadata)
	}
}

type funcContext struct {
	resolveURL          func(id *charm.URL) (*ResolvedURL, error)
	authorizeURL        func(id *ResolvedURL, req *http.Request) error
	willIncludeMetadata func([]string)
}

func (ctxt funcContext) ResolveURL(id *charm.URL) (*ResolvedURL, error) {
	return ctxt.resolveURL(id)
}

func (ctxt funcContext) ResolveURLs(ids []*charm.URL) ([]*ResolvedURL, error) {
	rurls := make([]*ResolvedURL, len(ids))
	for i, id := range ids {
		rurl, err := ctxt.resolveURL(id)
		if err != nil && errgo.Cause(err) != params.ErrNotFound {
			return nil, err
		}
		rurls[i] = rurl
	}
	return rurls, nil
}

func (ctxt funcContext) WillIncludeMetadata(includes []string) {
	ctxt.willIncludeMetadata(includes)
}

func (ctxt funcContext) AuthorizeEntity(id *ResolvedURL, req *http.Request) error {
	return ctxt.authorizeURL(id, req)
}

var parseBoolTests = []struct {
	value  string
	result bool
	err    bool
}{{
	value: "0",
}, {
	value: "",
}, {
	value:  "1",
	result: true,
}, {
	value: "invalid",
	err:   true,
}}

func (s *RouterSuite) TestParseBool(c *gc.C) {
	for i, test := range parseBoolTests {
		c.Logf("test %d: %s", i, test.value)
		result, err := ParseBool(test.value)
		c.Assert(result, gc.Equals, test.result)
		if test.err {
			c.Assert(err, gc.ErrorMatches, "unexpected bool value .*")
			continue
		}
		c.Assert(err, jc.ErrorIsNil)
	}
}

var alwaysContext = funcContext{
	resolveURL:          alwaysResolveURL,
	authorizeURL:        alwaysAuthorize,
	willIncludeMetadata: func([]string) {},
}

func (s *RouterSuite) TestCORSHeaders(c *gc.C) {
	h := New(&Handlers{
		Global: map[string]http.Handler{
			"foo": http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}),
		},
	}, alwaysContext)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: h,
		URL:     "/foo",
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Header().Get("Access-Control-Allow-Origin"), gc.Equals, "*")
	c.Assert(rec.Header().Get("Access-Control-Cache-Max-Age"), gc.Equals, "600")
	c.Assert(rec.Header().Get("Access-Control-Allow-Headers"), gc.Equals, "Bakery-Protocol-Version, Macaroons, X-Requested-With")
	c.Assert(rec.Header().Get("Access-Control-Allow-Methods"), gc.Equals, "DELETE,GET,HEAD,PUT,POST,OPTIONS")
	c.Assert(rec.Header().Get("Access-Control-Expose-Headers"), gc.Equals, "WWW-Authenticate")
}

func (s *RouterSuite) TestHTTPRequestPassedThroughToMeta(c *gc.C) {
	testReq, err := http.NewRequest("GET", "/wordpress/meta/foo", nil)
	c.Assert(err, gc.IsNil)
	doneQuery := false
	query := func(id *ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
		if req != testReq {
			return nil, fmt.Errorf("unexpected request found in Query")
		}
		doneQuery = true
		return 0, nil
	}
	doneGet := false
	handleGet := func(doc interface{}, id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		if req != testReq {
			return nil, fmt.Errorf("unexpected request found in HandleGet")
		}
		doneGet = true
		return 0, nil
	}
	donePut := false
	handlePut := func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
		if req != testReq {
			return fmt.Errorf("unexpected request found in HandlePut")
		}
		donePut = true
		return nil
	}
	update := func(id *ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
		return nil
	}
	h := New(&Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key:       0,
				Query:     query,
				Fields:    []string{"foo"},
				HandleGet: handleGet,
				HandlePut: handlePut,
				Update:    update,
			}),
		},
	}, alwaysContext)
	resp := httptest.NewRecorder()
	h.ServeHTTP(resp, testReq)
	c.Assert(resp.Code, gc.Equals, http.StatusOK, gc.Commentf("response body: %s", resp.Body))
	c.Assert(doneGet, jc.IsTrue)
	c.Assert(doneQuery, jc.IsTrue)

	testReq, err = http.NewRequest("PUT", "/wordpress/meta/foo", strings.NewReader(`"hello"`))
	testReq.Header.Set("Content-Type", "application/json")
	c.Assert(err, gc.IsNil)
	resp = httptest.NewRecorder()
	h.ServeHTTP(resp, testReq)
	c.Assert(resp.Code, gc.Equals, http.StatusOK, gc.Commentf("response body: %s", resp.Body))
	c.Assert(donePut, jc.IsTrue)
}

func (s *RouterSuite) TestOptionsHTTPMethod(c *gc.C) {
	h := New(&Handlers{}, alwaysContext)
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: h,
		Method:  "OPTIONS",
		URL:     "/foo",
		Header:  http.Header{"Origin": []string{"https://1.2.42.47"}},
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	header := rec.Header()
	c.Assert(header.Get("Access-Control-Allow-Origin"), gc.Equals, "https://1.2.42.47")
	c.Assert(header.Get("Access-Control-Cache-Max-Age"), gc.Equals, "600")
	c.Assert(header.Get("Access-Control-Allow-Headers"), gc.Equals, "Bakery-Protocol-Version, Macaroons, X-Requested-With")
	c.Assert(header.Get("Access-Control-Allow-Methods"), gc.Equals, "DELETE,GET,HEAD,PUT,POST,OPTIONS")
	c.Assert(header.Get("Allow"), gc.Equals, "DELETE,GET,HEAD,PUT,POST")
}

var routerPutTests = []struct {
	about               string
	handlers            Handlers
	urlStr              string
	body                interface{}
	expectCode          int
	expectBody          interface{}
	expectRecordedCalls []interface{}
	resolveURL          func(*charm.URL) (*ResolvedURL, error)
}{{
	about: "global handler",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo": HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
				return ReqInfo{
					Method: req.Method,
					Path:   req.URL.Path,
					Form:   req.Form,
				}, nil
			}),
		},
	},
	urlStr:     "/foo",
	expectCode: http.StatusOK,
	expectBody: ReqInfo{
		Method: "PUT",
		Path:   "",
	},
}, {
	about: "id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:     "/precise/wordpress-34/foo",
	expectCode: http.StatusOK,
	expectBody: idHandlerTestResp{
		Method:   "PUT",
		CharmURL: "cs:precise/wordpress-34",
	},
}, {
	about: "meta handler",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr:     "/precise/wordpress-42/meta/foo",
	expectCode: http.StatusOK,
	body:       "hello",
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:precise/wordpress-42",
			Paths:       []string{""},
			Values:      []interface{}{"hello"},
		},
	},
}, {
	about: "meta/any",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
			"bar": testMetaHandler(1),
		},
	},
	urlStr: "/precise/wordpress-42/meta/any",
	body: params.MetaAnyResponse{
		Meta: map[string]interface{}{
			"foo": "foo-value",
			"bar": map[string]interface{}{
				"bar-value1": 234.0,
				"bar-value2": "whee",
			},
		},
	},
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 2,
			Id:          "cs:precise/wordpress-42",
			Paths:       []string{"", ""},
			Values: []interface{}{
				"foo-value",
				map[string]interface{}{
					"bar-value1": 234.0,
					"bar-value2": "whee",
				},
			},
		},
	},
}, {
	about: "meta/any with extra paths",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": testMetaHandler(0),
			"bar":  testMetaHandler(1),
		},
	},
	urlStr: "/precise/wordpress-42/meta/any",
	body: params.MetaAnyResponse{
		Meta: map[string]interface{}{
			"foo/one": "foo-value-one",
			"foo/two": "foo-value-two",
			"bar":     1234.0,
		},
	},
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 3,
			Id:          "cs:precise/wordpress-42",
			Paths:       []string{"/one", "/two", ""},
			Values: []interface{}{
				"foo-value-one",
				"foo-value-two",
				1234.0,
			},
		},
	},
}, {
	about: "bulk meta",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr: "/meta/foo",
	body: map[string]string{
		"precise/wordpress-42": "forty two",
		"precise/foo-134":      "blah",
	},
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:precise/foo-134",
			Paths:       []string{""},
			Values:      []interface{}{"blah"},
		},
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:precise/wordpress-42",
			Paths:       []string{""},
			Values:      []interface{}{"forty two"},
		},
	},
}, {
	about: "bulk meta any",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  testMetaHandler(0),
			"bar":  testMetaHandler(1),
			"baz/": testMetaHandler(2),
		},
	},
	urlStr: "/meta/any",
	body: map[string]params.MetaAnyResponse{
		"precise/wordpress-42": {
			Meta: map[string]interface{}{
				"foo": "foo-wordpress-val",
				"bar": "bar-wordpress-val",
			},
		},
		"precise/mysql-134": {
			Meta: map[string]interface{}{
				"foo":      "foo-mysql-val",
				"baz/blah": "baz/blah-mysql-val",
				"baz/ppp":  "baz/ppp-mysql-val",
			},
		},
		"trusty/django-47": {
			Meta: map[string]interface{}{
				"foo": "foo-django-val",
			},
		},
	},
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 3,
			Id:          "cs:precise/mysql-134",
			Paths:       []string{"", "/blah", "/ppp"},
			Values:      []interface{}{"foo-mysql-val", "baz/blah-mysql-val", "baz/ppp-mysql-val"},
		},
		metaHandlerTestPutParams{
			NumHandlers: 2,
			Id:          "cs:precise/wordpress-42",
			Paths:       []string{"", ""},
			Values:      []interface{}{"foo-wordpress-val", "bar-wordpress-val"},
		},
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:trusty/django-47",
			Paths:       []string{""},
			Values:      []interface{}{"foo-django-val"},
		},
	},
}, {
	about: "field include handler with bulk meta any",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo":  fieldSelectHandler("handler1", 0, "field1", "field2"),
			"bar":  fieldSelectHandler("handler2", 0, "field3", "field4"),
			"baz/": fieldSelectHandler("handler3", 1, "field5"),
		},
	},
	urlStr: "/meta/any",
	body: map[string]params.MetaAnyResponse{
		"precise/mysql-123": {
			Meta: map[string]interface{}{
				"foo":      "foo-mysql-val",
				"baz/blah": "baz/blah-mysql-val",
				"baz/ppp":  "baz/ppp-mysql-val",
			},
		},
		"precise/wordpress-42": {
			Meta: map[string]interface{}{
				"foo": "foo-wordpress-val",
				"bar": "bar-wordpress-val",
			},
		},
	},
	expectRecordedCalls: []interface{}{
		fieldSelectHandleUpdateInfo{
			Id: "cs:precise/mysql-123",
			Fields: map[string]fieldSelectHandlePutInfo{
				"field1": {
					Id:    "cs:precise/mysql-123",
					Value: "foo-mysql-val",
				},
				"field2": {
					Id:    "cs:precise/mysql-123",
					Value: "foo-mysql-val",
				},
			},
		},
		fieldSelectHandleUpdateInfo{
			Id: "cs:precise/mysql-123",
			Fields: map[string]fieldSelectHandlePutInfo{
				"field5/blah": {
					Id:    "cs:precise/mysql-123",
					Value: "baz/blah-mysql-val",
				},
				"field5/ppp": {
					Id:    "cs:precise/mysql-123",
					Value: "baz/ppp-mysql-val",
				},
			},
		},
		fieldSelectHandleUpdateInfo{
			Id: "cs:precise/wordpress-42",
			Fields: map[string]fieldSelectHandlePutInfo{
				"field1": {
					Id:    "cs:precise/wordpress-42",
					Value: "foo-wordpress-val",
				},
				"field2": {
					Id:    "cs:precise/wordpress-42",
					Value: "foo-wordpress-val",
				},
				"field3": {
					Id:    "cs:precise/wordpress-42",
					Value: "bar-wordpress-val",
				},
				"field4": {
					Id:    "cs:precise/wordpress-42",
					Value: "bar-wordpress-val",
				},
			},
		},
	},
}, {
	about: "field include handler with no HandlePut",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
			}),
		},
	},
	urlStr:     "/precise/wordpress-23/meta/foo",
	body:       "something",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "PUT not supported",
	},
}, {
	about: "field include handler when HandlePut returns an error",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return errgo.WithCausef(nil, params.ErrNotFound, "message")
				},
			}),
		},
	},
	urlStr:     "/precise/wordpress-23/meta/foo",
	body:       "something",
	expectCode: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "message",
	},
}, {
	about: "meta put to field include handler with several errors",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return errgo.WithCausef(nil, params.ErrNotFound, "foo error")
				},
				Update: nopUpdate,
			}),
			"bar": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return errgo.New("bar error")
				},
				Update: nopUpdate,
			}),
			"baz": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return nil
				},
				Update: nopUpdate,
			}),
		},
	},
	urlStr: "/precise/wordpress-23/meta/any",
	body: params.MetaAnyResponse{
		Meta: map[string]interface{}{
			"foo": "one",
			"bar": "two",
			"baz": "three",
		},
	},
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    params.ErrMultipleErrors,
		Message: "multiple (2) errors",
		Info: map[string]*params.Error{
			"foo": {
				Code:    params.ErrNotFound,
				Message: "foo error",
			},
			"bar": {
				Message: "bar error",
			},
		},
	},
}, {
	about: "meta/any put with update error",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo/": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					if path == "/bad" {
						return fmt.Errorf("foo/bad error")
					}
					return nil
				},
				Update: func(id *ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
					return params.ErrBadRequest
				},
			}),
			"bar": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 1,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return fmt.Errorf("bar error")
				},
			}),
		},
	},
	urlStr: "/precise/wordpress-23/meta/any",
	body: params.MetaAnyResponse{
		Meta: map[string]interface{}{
			"foo/one": "one",
			"foo/two": "two",
			"foo/bad": "bad",
			"bar":     "bar",
		},
	},
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    params.ErrMultipleErrors,
		Message: "multiple (4) errors",
		Info: map[string]*params.Error{
			// All endpoints that share the same bulk key should
			// get the same error, as the update pertains to all of them,
			// but endpoints for which the HandlePut failed will
			// not be included in that.
			"foo/one": {
				Code:    params.ErrBadRequest,
				Message: "bad request",
			},
			"foo/two": {
				Code:    params.ErrBadRequest,
				Message: "bad request",
			},
			"foo/bad": {
				Message: "foo/bad error",
			},
			"bar": {
				Message: "bar error",
			},
		},
	},
}, {
	about: "bulk meta/any put with several errors",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return nil
				},
				Update: nopUpdate,
			}),
			"bar": NewFieldIncludeHandler(FieldIncludeHandlerParams{
				Key: 0,
				HandlePut: func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
					return errgo.WithCausef(nil, params.ErrNotFound, "bar error")
				},
				Update: nopUpdate,
			}),
		},
	},
	resolveURL: func(id *charm.URL) (*ResolvedURL, error) {
		if id.Name == "bad" {
			return nil, params.ErrBadRequest
		}
		return &ResolvedURL{URL: *id}, nil
	},
	urlStr: "/meta/any",
	body: map[string]params.MetaAnyResponse{
		"precise/mysql-123": {
			Meta: map[string]interface{}{
				"foo": "fooval",
				"bar": "barval",
			},
		},
		"bad": {
			Meta: map[string]interface{}{
				"foo": "foo-wordpress-val",
				"bar": "bar-wordpress-val",
			},
		},
	},
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Code:    params.ErrMultipleErrors,
		Message: "multiple (2) errors",
		Info: map[string]*params.Error{
			"precise/mysql-123": {
				Code:    params.ErrMultipleErrors,
				Message: "multiple (1) errors",
				Info: map[string]*params.Error{
					"bar": {
						Code:    params.ErrNotFound,
						Message: "bar error",
					},
				},
			},
			"bad": {
				Message: "bad request",
				Code:    params.ErrBadRequest,
			},
		},
	},
}, {
	about: "meta put with unresolved URL",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr:     "/wordpress/meta/foo",
	resolveURL: resolveTo("series", 245),
	expectCode: http.StatusOK,
	body:       "hello",
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:series/wordpress-245",
			Paths:       []string{""},
			Values:      []interface{}{"hello"},
		},
	},
}, {
	about: "bulk put with unresolved URL",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr:     "/meta/foo",
	resolveURL: resolveTo("series", 245),
	expectCode: http.StatusOK,
	body: map[string]string{
		"wordpress": "hello",
	},
	expectRecordedCalls: []interface{}{
		metaHandlerTestPutParams{
			NumHandlers: 1,
			Id:          "cs:series/wordpress-245",
			Paths:       []string{""},
			Values:      []interface{}{"hello"},
		},
	},
}, {
	about: "bulk put with ids specified in URL",
	handlers: Handlers{
		Meta: map[string]BulkIncludeHandler{
			"foo": testMetaHandler(0),
		},
	},
	urlStr:     "/meta/foo?id=wordpress",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "ids may not be specified in meta PUT request",
	},
}}

func nopUpdate(id *ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
	return nil
}

func (s *RouterSuite) TestRouterPut(c *gc.C) {
	for i, test := range routerPutTests {
		c.Logf("test %d: %s", i, test.about)
		ResetRecordedCalls()
		resolve := alwaysResolveURL
		if test.resolveURL != nil {
			resolve = test.resolveURL
		}
		bodyVal, err := json.Marshal(test.body)
		c.Assert(err, gc.IsNil)
		ctxt := alwaysContext
		ctxt.resolveURL = resolve
		router := New(&test.handlers, ctxt)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: router,
			URL:     test.urlStr,
			Body:    bytes.NewReader(bodyVal),
			Method:  "PUT",
			Header: map[string][]string{
				"Content-Type": {"application/json"},
			},
			ExpectStatus: test.expectCode,
			ExpectBody:   test.expectBody,
		})
		c.Assert(RecordedCalls(), jc.DeepEquals, test.expectRecordedCalls)
	}
}

var routerPutWithInvalidContentTests = []struct {
	about       string
	urlStr      string
	contentType string
	body        string
	expectCode  int
	expectBody  interface{}
}{{
	about:       "invalid content type with meta",
	urlStr:      "/precise/wordpress-23/meta/foo",
	contentType: "foo/bar",
	expectCode:  http.StatusBadRequest,
	expectBody: params.Error{
		Message: `unexpected Content-Type "foo/bar"; expected "application/json"`,
		Code:    params.ErrBadRequest,
	},
}, {
	about:       "invalid content type with bulk meta",
	urlStr:      "/meta/foo",
	contentType: "foo/bar",
	expectCode:  http.StatusBadRequest,
	expectBody: params.Error{
		Message: `unexpected Content-Type "foo/bar"; expected "application/json"`,
		Code:    params.ErrBadRequest,
	},
}, {
	about:       "bad JSON with meta",
	urlStr:      "/precise/wordpress-23/meta/foo",
	contentType: "application/json",
	body:        `"foo`,
	expectCode:  http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot unmarshal body: unexpected EOF`,
	},
}, {
	about:       "bad JSON with bulk meta",
	urlStr:      "/meta/foo",
	contentType: "application/json",
	body:        `"foo`,
	expectCode:  http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot unmarshal body: unexpected EOF`,
	},
}}

func (s *RouterSuite) TestRouterPutWithInvalidContent(c *gc.C) {
	for i, test := range routerPutWithInvalidContentTests {
		c.Logf("test %d: %s", i, test.about)
		handlers := &Handlers{
			Meta: map[string]BulkIncludeHandler{
				"foo": testMetaHandler(0),
			},
		}
		router := New(handlers, alwaysContext)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: router,
			URL:     test.urlStr,
			Body:    strings.NewReader(test.body),
			Method:  "PUT",
			Header: map[string][]string{
				"Content-Type": {test.contentType},
			},
			ExpectStatus: test.expectCode,
			ExpectBody:   test.expectBody,
		})
	}
}

func alwaysExists(id *ResolvedURL, req *http.Request) (bool, error) {
	return true, nil
}

func alwaysAuthorize(id *ResolvedURL, req *http.Request) error {
	return nil
}

func neverAuthorize(id *ResolvedURL, req *http.Request) error {
	return errgo.WithCausef(nil, params.ErrUnauthorized, "bad wolf")
}

func dischargeRequiredAuthorize(id *ResolvedURL, req *http.Request) error {
	if id.String() == "cs:utopic/foo-32" {
		return nil
	}
	return httpbakery.NewDischargeRequiredError(nil, "/", errgo.New("discharge required"))
}

var getMetadataTests = []struct {
	id           *ResolvedURL
	includes     []string
	expectResult map[string]interface{}
	expectError  string
}{{
	id:           newResolvedURL("~charmers/precise/wordpress-34", 34),
	includes:     []string{},
	expectResult: map[string]interface{}{},
}, {
	id:       newResolvedURL("~rog/precise/wordpress-2", -1),
	includes: []string{"item1", "item2", "test"},
	expectResult: map[string]interface{}{
		"item1": fieldSelectHandleGetInfo{
			HandlerId: "handler1",
			Doc: fieldSelectQueryInfo{
				Id:       newResolvedURL("cs:~rog/precise/wordpress-2", -1),
				Selector: map[string]int{"item1": 1, "item2": 1},
			},
			Id: newResolvedURL("cs:~rog/precise/wordpress-2", -1),
		},
		"item2": fieldSelectHandleGetInfo{
			HandlerId: "handler2",
			Doc: fieldSelectQueryInfo{
				Id:       newResolvedURL("cs:~rog/precise/wordpress-2", -1),
				Selector: map[string]int{"item1": 1, "item2": 1},
			},
			Id: newResolvedURL("cs:~rog/precise/wordpress-2", -1),
		},
		"test": &metaHandlerTestResp{
			CharmURL: "cs:~rog/precise/wordpress-2",
		},
	},
}, {
	id:          newResolvedURL("~rog/precise/wordpress-2", -1),
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
				"test":  testMetaHandler(0),
			},
		}, alwaysContext)
		result, err := router.GetMetadata(test.id, test.includes, nil)
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
	path:      "development/wordpress",
	expectURL: "cs:development/wordpress",
}, {
	path:      "~user/development/wordpress",
	expectURL: "cs:~user/development/wordpress",
}, {
	path:        "",
	expectError: `URL has invalid charm or bundle name: ""`,
}, {
	path:        "~foo-bar-/wordpress",
	expectError: `charm or bundle URL has invalid user name: "~foo-bar-/wordpress"`,
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
		c.Assert(err, gc.Equals, nil)
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
	err := httprequest.WriteJSON(rec, http.StatusTeapot, Number{1234})
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
	c.Assert(errResp, gc.DeepEquals, params.Error{Message: "an error"})
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
	c.Assert(errResp1, gc.DeepEquals, errResp0)
	c.Assert(rec.Code, gc.Equals, http.StatusInternalServerError)
}

func (s *RouterSuite) TestServeMux(c *gc.C) {
	mux := NewServeMux()
	mux.Handle("/data", HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
		return Foo{"hello"}, nil
	}))
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:    mux,
		URL:        "/data",
		ExpectBody: Foo{"hello"},
	})
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      mux,
		URL:          "/foo",
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Message: `no handler for "/foo"`,
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
	handler: HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
		return Foo{"hello"}, nil
	}),
	expectStatus: http.StatusOK,
	expectBody:   Foo{"hello"},
}, {
	about: "handleJSON, error case",
	handler: HandleJSON(func(_ http.Header, req *http.Request) (interface{}, error) {
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
	Path   string
	Method string
	Form   url.Values `json:",omitempty"`
}

func (s *RouterSuite) TestHandlers(c *gc.C) {
	for i, test := range handlerTests {
		c.Logf("test %d: %s", i, test.about)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      test.handler,
			URL:          "",
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

var resolvedURLTests = []struct {
	rurl                 *ResolvedURL
	expectUserOwnedURL   *charm.URL
	expectPreferredURL   *charm.URL
	expectPromulgatedURL *charm.URL
}{{
	rurl:                 MustNewResolvedURL("~charmers/precise/wordpress-23", 4),
	expectUserOwnedURL:   charm.MustParseURL("~charmers/precise/wordpress-23"),
	expectPreferredURL:   charm.MustParseURL("precise/wordpress-4"),
	expectPromulgatedURL: charm.MustParseURL("precise/wordpress-4"),
}, {
	rurl:               MustNewResolvedURL("~who/development/trusty/wordpress-42", -1),
	expectUserOwnedURL: charm.MustParseURL("~who/trusty/wordpress-42"),
	expectPreferredURL: charm.MustParseURL("~who/trusty/wordpress-42"),
}, {
	rurl:               MustNewResolvedURL("~charmers/precise/wordpress-23", -1),
	expectUserOwnedURL: charm.MustParseURL("~charmers/precise/wordpress-23"),
	expectPreferredURL: charm.MustParseURL("~charmers/precise/wordpress-23"),
}, {
	rurl:                 MustNewResolvedURL("~charmers/development/trusty/wordpress-42", 0),
	expectUserOwnedURL:   charm.MustParseURL("~charmers/trusty/wordpress-42"),
	expectPreferredURL:   charm.MustParseURL("trusty/wordpress-0"),
	expectPromulgatedURL: charm.MustParseURL("trusty/wordpress-0"),
}, {
	rurl:                 withPreferredSeries(MustNewResolvedURL("~charmers/wordpress-42", 0), "trusty"),
	expectUserOwnedURL:   charm.MustParseURL("~charmers/wordpress-42"),
	expectPreferredURL:   charm.MustParseURL("trusty/wordpress-0"),
	expectPromulgatedURL: charm.MustParseURL("wordpress-0"),
}, {
	rurl:               withPreferredSeries(MustNewResolvedURL("~charmers/wordpress-42", -1), "trusty"),
	expectUserOwnedURL: charm.MustParseURL("~charmers/wordpress-42"),
	expectPreferredURL: charm.MustParseURL("~charmers/trusty/wordpress-42"),
}}

func withPreferredSeries(r *ResolvedURL, series string) *ResolvedURL {
	r.PreferredSeries = series
	return r
}

func (*RouterSuite) TestResolvedURL(c *gc.C) {
	testMethod := func(name string, rurl *ResolvedURL, m func() *charm.URL, expect *charm.URL) {
		c.Logf("- method %s", name)
		u := m()
		c.Assert(u, jc.DeepEquals, expect)
		// Ensure it's not aliased.
		c.Assert(u, gc.Not(gc.Equals), &rurl.URL)
	}
	for i, test := range resolvedURLTests {
		c.Logf("test %d: %#v", i, test.rurl)
		testMethod("UserOwnedURL", test.rurl, test.rurl.UserOwnedURL, test.expectUserOwnedURL)
		testMethod("PromulgatedURL", test.rurl, test.rurl.PromulgatedURL, test.expectPromulgatedURL)

		testMethod("PreferredURL", test.rurl, test.rurl.PreferredURL, test.expectPreferredURL)
	}
}

func errorIdHandler(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return errgo.Newf("errorIdHandler error")
}

type idHandlerTestResp struct {
	Method   string
	CharmURL string
	Path     string
}

func testIdHandler(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	httprequest.WriteJSON(w, http.StatusOK, idHandlerTestResp{
		CharmURL: charmId.String(),
		Path:     req.URL.Path,
		Method:   req.Method,
	})
	return nil
}

type metaHandlerTestResp struct {
	CharmURL string
	Path     string
	Flags    url.Values
}

var testMetaGetHandler = SingleIncludeHandler(
	func(id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		if len(flags) == 0 {
			flags = nil
		}
		return &metaHandlerTestResp{
			CharmURL: id.String(),
			Path:     path,
			Flags:    flags,
		}, nil
	},
)

type testMetaHandler int

func (testMetaHandler) Key() interface{} {
	type testMetaHandlerKey struct{}
	return testMetaHandlerKey{}
}

func (testMetaHandler) HandleGet(hs []BulkIncludeHandler, id *ResolvedURL, paths []string, flags url.Values, req *http.Request) ([]interface{}, error) {
	results := make([]interface{}, len(hs))
	for i, h := range hs {
		_ = h.(testMetaHandler)
		if len(flags) == 0 {
			flags = nil
		}
		results[i] = &metaHandlerTestResp{
			CharmURL: id.String(),
			Path:     paths[i],
			Flags:    flags,
		}
	}
	return results, nil
}

type metaHandlerTestPutParams struct {
	Id          string
	NumHandlers int
	Paths       []string
	Values      []interface{}
}

func (testMetaHandler) HandlePut(hs []BulkIncludeHandler, id *ResolvedURL, paths []string, rawValues []*json.RawMessage, req *http.Request) []error {
	// Handlers are provided in arbitrary order,
	// so we order them (and their associated paths
	// and values) to enable easier testing.
	keys := make(sort.StringSlice, len(hs))
	for i, h := range hs {
		// Sort by handler primary, path secondary.
		keys[i] = fmt.Sprintf("%d.%s", int(h.(testMetaHandler)), paths[i])
	}
	sort.Sort(groupSort{
		key: keys,
		other: []swapper{
			sort.StringSlice(paths),
			swapFunc(func(i, j int) {
				rawValues[i], rawValues[j] = rawValues[j], rawValues[i]
			}),
		},
	})

	values := make([]interface{}, len(rawValues))
	for i, val := range rawValues {
		err := json.Unmarshal(*val, &values[i])
		if err != nil {
			panic(err)
		}
	}
	RecordCall(metaHandlerTestPutParams{
		NumHandlers: len(hs),
		Id:          id.String(),
		Paths:       paths,
		Values:      values,
	})
	return nil
}

// constMetaHandler returns a handler that always returns the given
// value.
func constMetaHandler(val interface{}) BulkIncludeHandler {
	return SingleIncludeHandler(
		func(id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
			return val, nil
		},
	)
}

func errorMetaHandler(err error) BulkIncludeHandler {
	return SingleIncludeHandler(
		func(id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
			return nil, err
		},
	)
}

type fieldSelectQueryInfo struct {
	Id       *ResolvedURL
	Selector map[string]int
}

type fieldSelectHandleGetInfo struct {
	HandlerId string
	Doc       fieldSelectQueryInfo
	Id        *ResolvedURL
	Path      string
	Flags     url.Values
}

type fieldSelectHandleUpdateInfo struct {
	Id     string
	Fields map[string]fieldSelectHandlePutInfo
}

type fieldSelectHandlePutInfo struct {
	Id    string
	Path  string
	Value interface{}
}

var queryCount int32

var (
	callRecordsMutex sync.Mutex
	callRecords      byJSON
)

// RecordCall adds a value that can be retrieved later with
// RecordedCalls.
//
// This is used to check the parameters passed to
// handlers that do not return results.
func RecordCall(x interface{}) {
	callRecordsMutex.Lock()
	defer callRecordsMutex.Unlock()
	callRecords = append(callRecords, x)
}

// ResetRecordedCalls clears the call records.
func ResetRecordedCalls() {
	callRecordsMutex.Lock()
	defer callRecordsMutex.Unlock()
	callRecords = nil
}

// RecordedCalls returns the values passed to RecordCall,
// ordered by their JSON serialization.
func RecordedCalls() []interface{} {
	callRecordsMutex.Lock()
	defer callRecordsMutex.Unlock()

	sort.Sort(callRecords)
	return callRecords
}

// byJSON implements sort.Interface, ordering its
// elements lexicographically by marshaled JSON
// representation.
type byJSON []interface{}

func (b byJSON) Less(i, j int) bool {
	idata, err := json.Marshal(b[i])
	if err != nil {
		panic(err)
	}
	jdata, err := json.Marshal(b[j])
	if err != nil {
		panic(err)
	}
	return bytes.Compare(idata, jdata) < 0
}

func (b byJSON) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

func (b byJSON) Len() int {
	return len(b)
}

// fieldSelectHandler returns a BulkIncludeHandler that returns
// information about the call for testing purposes.
// When the GET handler is invoked, it returns a fieldSelectHandleGetInfo value
// with the given handlerId. Key holds the grouping key,
// and fields holds the fields to select.
//
// When the PUT handler is invoked SetCallRecord is called with
// a fieldSelectHandlePutInfo value holding the parameters that were
// provided.
func fieldSelectHandler(handlerId string, key interface{}, fields ...string) BulkIncludeHandler {
	query := func(id *ResolvedURL, selector map[string]int, req *http.Request) (interface{}, error) {
		atomic.AddInt32(&queryCount, 1)
		return fieldSelectQueryInfo{
			Id:       id,
			Selector: selector,
		}, nil
	}
	handleGet := func(doc interface{}, id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		if len(flags) == 0 {
			flags = nil
		}
		return fieldSelectHandleGetInfo{
			HandlerId: handlerId,
			Doc:       doc.(fieldSelectQueryInfo),
			Id:        id,
			Path:      path,
			Flags:     flags,
		}, nil
	}

	handlePut := func(id *ResolvedURL, path string, val *json.RawMessage, updater *FieldUpdater, req *http.Request) error {
		var vali interface{}
		err := json.Unmarshal(*val, &vali)
		if err != nil {
			panic(err)
		}
		for _, field := range fields {
			updater.UpdateField(field+path, fieldSelectHandlePutInfo{
				Id:    id.String(),
				Value: vali,
			}, nil)
		}
		return nil
	}

	update := func(id *ResolvedURL, fields map[string]interface{}, entries []audit.Entry) error {
		// We make information on how update and handlePut have
		// been called by calling SetCallRecord with the above
		// parameters. The fields will have been created by
		// handlePut, and therefore are known to contain
		// fieldSelectHandlePutInfo values. We convert the
		// values to static types so that it is more obvious
		// what the values in fieldSelectHandleUpdateInfo.Fields
		// contain.
		infoFields := make(map[string]fieldSelectHandlePutInfo)
		for name, val := range fields {
			infoFields[name] = val.(fieldSelectHandlePutInfo)
		}
		RecordCall(fieldSelectHandleUpdateInfo{
			Id:     id.String(),
			Fields: infoFields,
		})
		return nil
	}

	return NewFieldIncludeHandler(FieldIncludeHandlerParams{
		Key:       key,
		Query:     query,
		Fields:    fields,
		HandleGet: handleGet,
		HandlePut: handlePut,
		Update:    update,
	})
}

// selectiveIdHandler handles metadata by returning the
// data found in the map for the requested id.
func selectiveIdHandler(m map[string]interface{}) BulkIncludeHandler {
	return SingleIncludeHandler(func(id *ResolvedURL, path string, flags url.Values, req *http.Request) (interface{}, error) {
		return m[id.String()], nil
	})
}

type swapper interface {
	Swap(i, j int)
}

type swapFunc func(i, j int)

func (f swapFunc) Swap(i, j int) {
	f(i, j)
}

// groupSort is an implementation of sort.Interface
// that keeps a set of secondary values sorted according
// to the same criteria as key.
type groupSort struct {
	key   sort.Interface
	other []swapper
}

func (g groupSort) Less(i, j int) bool {
	return g.key.Less(i, j)
}

func (g groupSort) Swap(i, j int) {
	g.key.Swap(i, j)
	for _, o := range g.other {
		o.Swap(i, j)
	}
}

func (g groupSort) Len() int {
	return g.key.Len()
}
