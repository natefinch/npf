// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"gopkg.in/juju/charm.v2"
	"labix.org/v2/mgo"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/params"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}

type RouterSuite struct {
	storetesting.IsolatedMgoSuite
}

var _ = gc.Suite(&RouterSuite{})

func (s *RouterSuite) populateDatabase(c *gc.C) *mgo.Database {
	// Populate the database with two collections and a couple of
	// items each.
	db := s.Session.DB("testing")
	c1 := db.C("c1")
	err := c1.Insert(&testItem1{1, "item1.1"}, &testItem1{2, "item1.2"})
	c.Assert(err, gc.IsNil)
	c2 := db.C("c2")
	err = c2.Insert(&testItem2{3, 888}, &testItem2{4, 999})
	c.Assert(err, gc.IsNil)

	// Register the two collections.
	RegisterCollection("c1", (*testItem1)(nil))
	RegisterCollection("c2", (*testItem2)(nil))
	s.AddCleanup(func(c *gc.C) {
		collections = make(map[reflect.Type]string)
	})
	return db
}

var routerTests = []struct {
	about      string
	handlers   Handlers
	urlStr     string
	expectCode int
	expectBody interface{}
	resolveURL func(*charm.URL) error
}{{
	about: "global handler",
	handlers: Handlers{
		Global: map[string]http.Handler{
			"foo": HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
				return &Foo{"hello"}, nil
			}),
		},
	},
	urlStr:     "http://example.com/foo",
	expectCode: http.StatusOK,
	expectBody: Foo{"hello"},
}, {
	about: "id handler",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-34/foo",
	expectCode: http.StatusOK,
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
	urlStr:     "http://example.com/precise/wordpress-34/foo/blah/arble",
	expectCode: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:precise/wordpress-34",
		Path:     "blah/arble",
	},
}, {
	about: "id handler with allowed extra path but none given",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": testIdHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-34/foo",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "not found",
	},
}, {
	about: "id handler with unwanted extra path",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-34/foo/blah",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "not found",
	},
}, {
	about: "id handler with user",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:     "http://example.com/~joe/precise/wordpress-34/foo",
	expectCode: http.StatusOK,
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
	urlStr:     "http://example.com/~joe/precise/wordpress-34/foo/blah/arble",
	expectCode: http.StatusOK,
	expectBody: idHandlerTestResp{
		CharmURL: "cs:~joe/precise/wordpress-34",
		Path:     "blah/arble",
	},
}, {
	about: "id handler that returns an error",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo/": errorIdHandler,
		},
	},
	urlStr:     "http://example.com/~joe/precise/wordpress-34/foo/blah/arble",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "errorIdHandler error",
	},
}, {
	about: "id with unspecified series and revision, resolved",
	handlers: Handlers{
		Id: map[string]IdHandler{
			"foo": testIdHandler,
		},
	},
	urlStr:     "http://example.com/~joe/wordpress/foo",
	resolveURL: newResolveURL("precise", 34),
	expectCode: http.StatusOK,
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
	urlStr:     "http://example.com/wordpress/meta",
	resolveURL: resolveURLError,
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "resolve URL error",
	},
}, {
	about: "meta handler",
	handlers: Handlers{
		Meta: map[string]MetaHandler{
			"foo": testMetaHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-42/meta/foo",
	expectCode: http.StatusOK,
	expectBody: &metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
	},
}, {
	about: "meta handler with additional elements",
	handlers: Handlers{
		Meta: map[string]MetaHandler{
			"foo/": testMetaHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-42/meta/foo/bar/baz",
	expectCode: http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Path:     "bar/baz",
	},
}, {
	about: "meta handler with params",
	handlers: Handlers{
		Meta: map[string]MetaHandler{
			"foo": testMetaHandler,
		},
	},
	urlStr:     "http://example.com/precise/wordpress-42/meta/foo?one=a&two=b&one=c",
	expectCode: http.StatusOK,
	expectBody: metaHandlerTestResp{
		CharmURL: "cs:precise/wordpress-42",
		Flags: url.Values{
			"one": {"a", "c"},
			"two": {"b"},
		},
	},
}, {
	about:      "meta handler that's not found",
	urlStr:     "http://example.com/precise/wordpress-42/meta/foo",
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "not found",
	},
}, {
	about:  "meta handler that gets something",
	urlStr: "http://example.com/precise/wordpress-42/meta/foo",
	handlers: Handlers{
		Meta: map[string]MetaHandler{
			"foo": metaGetItem1,
		},
	},
	expectCode: http.StatusOK,
	expectBody: testItem1{
		Id:   1,
		Name: "item1.1",
	},
}, {
	about:      "meta/any, no includes",
	urlStr:     "http://example.com/precise/wordpress-42/meta/any",
	expectCode: http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
	},
}, {
	about:  "meta/any, some includes",
	urlStr: "http://example.com/precise/wordpress-42/meta/any?include=item1&include=item2&include=test",
	handlers: Handlers{
		Meta: map[string]MetaHandler{
			"item1": metaGetItem1,
			"item2": metaGetItem2,
			"test":  testMetaHandler,
		},
	},
	expectCode: http.StatusOK,
	expectBody: params.MetaAnyResponse{
		Id: charm.MustParseURL("cs:precise/wordpress-42"),
		Meta: map[string]interface{}{
			"item1": map[string]interface{}{
				"Id":   float64(1),
				"Name": "item1.1",
			},
			"item2": map[string]interface{}{
				"Id":    float64(3),
				"Count": float64(888),
			},
			"test": map[string]interface{}{
				"CharmURL": "cs:precise/wordpress-42",
				"Path":     "",
				"Flags":    nil,
			},
		},
	},
}}

// newResolveURL returns a URL resolver that resolves
// unspecified series and revision to the given series
// and revision.
func newResolveURL(series string, revision int) func(*charm.URL) error {
	return func(url *charm.URL) error {
		if url.Series == "" {
			url.Series = series
		}
		if url.Revision == -1 {
			url.Revision = revision
		}
		return nil
	}
}

func resolveURLError(*charm.URL) error {
	return fmt.Errorf("resolve URL error")
}

func noResolveURL(*charm.URL) error {
	return nil
}

func (s *RouterSuite) TestRouter(c *gc.C) {
	db := s.populateDatabase(c)

	for i, test := range routerTests {
		c.Logf("test %d: %s", i, test.about)
		resolve := noResolveURL
		if test.resolveURL != nil {
			resolve = test.resolveURL
		}
		router := New(db, &test.handlers, resolve)
		storetesting.AssertJSONCall(c, router, "GET", test.urlStr, "", test.expectCode, test.expectBody)
	}
}

var getMetadataTests = []struct {
	id           string
	includes     []string
	expectResult map[string]interface{}
	expectError  string
}{{
	id:       "precise/wordpress-34",
	includes: []string{},
}, {
	id:       "~rog/precise/wordpress-2",
	includes: []string{"item1", "item2", "test"},
	expectResult: map[string]interface{}{
		"item1": &testItem1{
			Id:   1,
			Name: "item1.1",
		},
		"item2": &testItem2{
			Id:    3,
			Count: 888,
		},
		"test": &metaHandlerTestResp{
			CharmURL: "cs:~rog/precise/wordpress-2",
		},
	},
}, {
	id:          "~rog/precise/wordpress-2",
	includes:    []string{"mistaek"},
	expectError: `unrecognized metadata name "mistaek"`,
}}

func (s *RouterSuite) TestGetMetadata(c *gc.C) {
	db := s.populateDatabase(c)
	for i, test := range getMetadataTests {
		c.Logf("test %d: %q", i, test.includes)
		router := New(db, &Handlers{
			Meta: map[string]MetaHandler{
				"item1": metaGetItem1,
				"item2": metaGetItem2,
				"test":  testMetaHandler,
			},
		}, noResolveURL)
		id := charm.MustParseURL(test.id)
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
		c.Assert(rest, gc.Equals, "some/more")
	}
}

var handlerKeyTests = []struct {
	key        string
	expectKey  string
	expectRest string
}{{
	key:        "foo/bar",
	expectKey:  "foo/",
	expectRest: "bar",
}, {
	key:        "foo",
	expectKey:  "foo",
	expectRest: "",
}, {
	key:        "foo/bar/baz",
	expectKey:  "foo/",
	expectRest: "bar/baz",
}, {
	key:        "foo/",
	expectKey:  "foo",
	expectRest: "",
}}

func (s *RouterSuite) TestHandlerKey(c *gc.C) {
	for i, test := range handlerKeyTests {
		c.Logf("test %d: %s", i, test.key)
		key, rest := handlerKey(test.key)
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
	path:       "foo/bar",
	expectElem: "foo",
	expectRest: "bar",
}, {
	path:       "foo/",
	expectElem: "foo",
	expectRest: "",
}, {
	path:       "foo/bar/baz",
	expectElem: "foo",
	expectRest: "bar/baz",
}, {
	path:       "foo",
	expectElem: "foo",
	expectRest: "",
}, {
	path:       "foo/bar/baz",
	index:      4,
	expectElem: "bar",
	expectRest: "baz",
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
	WriteError(rec, fmt.Errorf("an error"))
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

var handlerTests = []struct {
	about      string
	handler    http.Handler
	urlStr     string
	expectCode int
	expectBody interface{}
}{{
	about: "handleErrors, normal error",
	handler: HandleErrors(func(http.ResponseWriter, *http.Request) error {
		return fmt.Errorf("an error")
	}),
	urlStr:     "http://example.com",
	expectCode: http.StatusInternalServerError,
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
	urlStr:     "http://example.com",
	expectCode: http.StatusInternalServerError,
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
	expectCode: http.StatusTeapot,
}, {
	about: "handleJSON, normal case",
	handler: HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
		return &Foo{"hello"}, nil
	}),
	expectCode: http.StatusOK,
	expectBody: Foo{"hello"},
}, {
	about: "handleJSON, error case",
	handler: HandleJSON(func(w http.ResponseWriter, req *http.Request) (interface{}, error) {
		return nil, fmt.Errorf("an error")
	}),
	expectCode: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: "an error",
	},
}}

type Foo struct {
	S string
}

func (s *RouterSuite) TestHandlers(c *gc.C) {
	for i, test := range handlerTests {
		c.Logf("test %d: %s", i, test.about)
		storetesting.AssertJSONCall(c, test.handler, "GET", "http://example.com", "", test.expectCode, test.expectBody)
	}
}

func metaGetItem1(getter ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	var item *testItem1
	err := getter.GetItem(1, &item, "id", "name")
	if err != nil {
		return nil, fmt.Errorf("GetItem failure: %v", err)
	}
	return item, nil
}

func metaGetItem2(getter ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	var item *testItem2
	err := getter.GetItem(3, &item, "id", "count")
	if err != nil {
		return nil, fmt.Errorf("GetItem failure: %v", err)
	}
	return item, nil
}

func errorIdHandler(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	return fmt.Errorf("errorIdHandler error")
}

type idHandlerTestResp struct {
	CharmURL string
	Path     string
}

func testIdHandler(charmId *charm.URL, w http.ResponseWriter, req *http.Request) error {
	WriteJSON(w, http.StatusOK, idHandlerTestResp{
		CharmURL: charmId.String(),
		Path:     req.URL.Path,
	})
	return nil
}

type metaHandlerTestResp struct {
	CharmURL string
	Path     string
	Flags    url.Values
}

func testMetaHandler(getter ItemGetter, id *charm.URL, path string, flags url.Values) (interface{}, error) {
	if len(flags) == 0 {
		flags = nil
	}
	return &metaHandlerTestResp{
		CharmURL: id.String(),
		Path:     path,
		Flags:    flags,
	}, nil
}

type testItem1 struct {
	Id   int `bson:"_id"`
	Name string
}

type testItem2 struct {
	Id    int `bson:"_id"`
	Count int
}
