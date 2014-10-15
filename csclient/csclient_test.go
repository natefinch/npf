// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient_test

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	charmtesting "gopkg.in/juju/charm.v4/testing"
	"gopkg.in/mgo.v2"

	"github.com/juju/charmstore/csclient"
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type suite struct {
	storetesting.IsolatedMgoSuite
	client *csclient.Client
	srv    *httptest.Server
	store  *charmstore.Store
}

var _ = gc.Suite(&suite{})

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

func newServer(c *gc.C, session *mgo.Session, config charmstore.ServerParams) (*httptest.Server, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db, nil)
	c.Assert(err, gc.IsNil)
	handler, err := charmstore.NewServer(db, nil, config, map[string]charmstore.NewAPIHandlerFunc{"v4": v4.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	return httptest.NewServer(handler), store
}

func (s *suite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
	s.client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
	})
}

func (s *suite) TearDownTest(c *gc.C) {
	s.srv.Close()
	s.IsolatedMgoSuite.TearDownTest(c)
}

var doTests = []struct {
	about           string
	method          string
	path            string
	nilResult       bool
	expectResult    interface{}
	expectError     string
	expectErrorCode params.ErrorCode
}{{
	about: "success",
	path:  "/wordpress/expand-id",
	expectResult: []params.ExpandedId{{
		Id: "cs:utopic/wordpress-42",
	}},
}, {
	about:     "success with nil result",
	path:      "/wordpress/expand-id",
	nilResult: true,
}, {
	about:       "non-absolute path",
	path:        "wordpress",
	expectError: `path "wordpress" is not absolute`,
}, {
	about:       "URL parse error",
	path:        "/wordpress/%zz",
	expectError: `parse .*: invalid URL escape "%zz"`,
}, {
	about:           "result with error code",
	path:            "/blahblah",
	expectError:     "not found",
	expectErrorCode: params.ErrNotFound,
}}

func (s *suite) TestDo(c *gc.C) {
	ch := charmtesting.Charms.CharmDir("wordpress")
	url := mustParseReference("utopic/wordpress-42")
	err := s.store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)

	for i, test := range doTests {
		c.Logf("test %d: %s", i, test.about)

		if test.method == "" {
			test.method = "GET"
		}

		// Set up the request.
		req, err := http.NewRequest(test.method, "", nil)
		c.Assert(err, gc.IsNil)

		// Send the request.
		var result json.RawMessage
		var resultPtr interface{}
		if !test.nilResult {
			resultPtr = &result
		}
		err = s.client.Do(req, test.path, resultPtr)

		// Check the response.
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError, gc.Commentf("error is %T; %#v", err, err))
			c.Assert(result, gc.IsNil)
			cause := errgo.Cause(err)
			if code, ok := cause.(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			continue
		}
		c.Assert(err, gc.IsNil)
		if test.expectResult != nil {
			c.Assert([]byte(result), storetesting.JSONEquals, test.expectResult)
		}
	}
}

func (s *suite) TestDoAuthorization(c *gc.C) {
	// Add a charm to be deleted.
	ch := charmtesting.Charms.CharmDir("wordpress")
	url := mustParseReference("utopic/wordpress-42")
	err := s.store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)

	// Check that when we use incorrect authorization,
	// we get an error trying to delete the charm
	client := csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     serverParams.AuthUsername,
		Password: "bad password",
	})
	req, err := http.NewRequest("DELETE", "", nil)
	c.Assert(err, gc.IsNil)
	err = client.Do(req, "/utopic/wordpress-42/archive", nil)
	c.Assert(err, gc.ErrorMatches, "invalid user name or password")
	c.Assert(errgo.Cause(err), gc.Equals, params.ErrUnauthorized)

	// Check that it's still there.
	req, err = http.NewRequest("GET", "", nil)
	err = client.Do(req, "/utopic/wordpress-42/expand-id", nil)
	c.Assert(err, gc.IsNil)

	// Then check that when we use the correct authorization,
	// the delete succeeds.
	client = csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
	})
	req, err = http.NewRequest("DELETE", "", nil)
	c.Assert(err, gc.IsNil)
	err = client.Do(req, "/utopic/wordpress-42/archive", nil)
	c.Assert(err, gc.IsNil)

	// Check that it's now really gone.
	req, err = http.NewRequest("GET", "", nil)
	err = client.Do(req, "/utopic/wordpress-42/expand-id", nil)
	c.Assert(err, gc.ErrorMatches, `no matching charm or bundle for "cs:wordpress"`)
}

var doWithBadResponseTests = []struct {
	about       string
	error       error
	response    *http.Response
	responseErr error
	expectError string
}{{
	about:       "http client Do failure",
	error:       errgo.New("round trip failure"),
	expectError: "Get .*: round trip failure",
}, {
	about: "body read error",
	response: &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(&errorReader{"body read error"}),
		ContentLength: -1,
	},
	expectError: "cannot read response body: body read error",
}, {
	about: "badly formatted json response",
	response: &http.Response{
		Status:        "200 OK",
		StatusCode:    200,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal response "bad": .*`,
}, {
	about: "badly formatted json error",
	response: &http.Response{
		Status:        "404 Not found",
		StatusCode:    404,
		Proto:         "HTTP/1.0",
		ProtoMajor:    1,
		ProtoMinor:    0,
		Body:          ioutil.NopCloser(strings.NewReader("bad")),
		ContentLength: -1,
	},
	expectError: `cannot unmarshal error response "bad": .*`,
}, {
	about: "error response with empty message",
	response: &http.Response{
		Status:     "404 Not found",
		StatusCode: 404,
		Proto:      "HTTP/1.0",
		ProtoMajor: 1,
		ProtoMinor: 0,
		Body: ioutil.NopCloser(bytes.NewReader(mustMarshalJSON(&params.Error{
			Code: "foo",
		}))),
		ContentLength: -1,
	},
	expectError: "error response with empty message .*",
}}

func (s *suite) TestDoWithBadResponse(c *gc.C) {
	for i, test := range doWithBadResponseTests {
		c.Logf("test %d: %s", i, test.about)
		cl := csclient.New(csclient.Params{
			URL: "http://0.1.2.3",
			HTTPClient: &http.Client{
				Transport: &cannedRoundTripper{
					resp:  test.response,
					error: test.error,
				},
			},
		})
		var result interface{}
		err := cl.Do(&http.Request{
			Method: "GET",
		}, "/foo", &result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

var hyphenateTests = []struct {
	val    string
	expect string
}{{
	val:    "Hello",
	expect: "hello",
}, {
	val:    "HelloThere",
	expect: "hello-there",
}, {
	val:    "HelloHTTP",
	expect: "hello-http",
}, {
	val:    "helloHTTP",
	expect: "hello-http",
}, {
	val:    "hellothere",
	expect: "hellothere",
}, {
	val:    "Long4Camel32WithDigits45",
	expect: "long4-camel32-with-digits45",
}, {
	// The result here is equally dubious, but Go identifiers
	// should not contain underscores.
	val:    "With_Dubious_Underscore",
	expect: "with_-dubious_-underscore",
}}

func (s *suite) TestGet(c *gc.C) {
	ch := charmtesting.Charms.CharmDir("wordpress")
	url := mustParseReference("utopic/wordpress-42")
	err := s.store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)

	var result []params.ExpandedId
	err = s.client.Get("/utopic/wordpress/expand-id", &result)
	c.Assert(err, gc.IsNil)
	c.Assert(result, jc.DeepEquals, []params.ExpandedId{{
		Id: "cs:utopic/wordpress-42",
	}})
}

func (s *suite) TestHyphenate(c *gc.C) {
	for i, test := range hyphenateTests {
		c.Logf("test %d. %q", i, test.val)
		c.Assert(csclient.Hyphenate(test.val), gc.Equals, test.expect)
	}
}

var metaBadTypeTests = []struct {
	result      interface{}
	expectError string
}{{
	result:      "",
	expectError: "expected pointer, not string",
}, {
	result:      new(string),
	expectError: `expected pointer to struct, not \*string`,
}, {
	result:      new(struct{ Embed }),
	expectError: "anonymous fields not supported",
}, {
	expectError: "expected valid result pointer, not nil",
}}

func (s *suite) TestMetaBadType(c *gc.C) {
	id := mustParseReference("wordpress")
	for _, test := range metaBadTypeTests {
		_, err := s.client.Meta(id, test.result)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

type Embed struct{}
type embed struct{}

func (s *suite) TestMeta(c *gc.C) {
	ch := charmtesting.Charms.CharmDir("wordpress")
	url := mustParseReference("utopic/wordpress-42")
	err := s.store.AddCharmWithArchive(url, ch)
	c.Assert(err, gc.IsNil)

	// Put some extra-info.
	req, _ := http.NewRequest("PUT", "", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = ioutil.NopCloser(strings.NewReader(`"value"`))
	err = s.client.Do(req, "/utopic/wordpress-42/meta/extra-info/attr", nil)
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about           string
		id              string
		expectResult    interface{}
		expectError     string
		expectErrorCode params.ErrorCode
	}{{
		about:        "no fields",
		id:           "utopic/wordpress",
		expectResult: &struct{}{},
	}, {
		about: "single field",
		id:    "utopic/wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "three fields",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata *charm.Meta
			CharmConfig   *charm.Config
			ExtraInfo     map[string]string
		}{
			CharmMetadata: ch.Meta(),
			CharmConfig:   ch.Config(),
			ExtraInfo:     map[string]string{"attr": "value"},
		},
	}, {
		about: "tagged field",
		id:    "wordpress",
		expectResult: &struct {
			Foo  *charm.Meta `csclient:"charm-metadata"`
			Attr string      `csclient:"extra-info/attr"`
		}{
			Foo:  ch.Meta(),
			Attr: "value",
		},
	}, {
		about:           "id not found",
		id:              "bogus",
		expectResult:    &struct{}{},
		expectError:     `cannot get "/bogus/meta/any": no matching charm or bundle for "cs:bogus"`,
		expectErrorCode: params.ErrNotFound,
	}, {
		about: "unmarshal into invalid type",
		id:    "wordpress",
		expectResult: new(struct {
			CharmMetadata []string
		}),
		expectError: `cannot unmarshal charm-metadata: json: cannot unmarshal object into Go value of type \[]string`,
	}, {
		about: "unmarshal into struct with unexported fields",
		id:    "wordpress",
		expectResult: &struct {
			unexported    int
			CharmMetadata *charm.Meta
			// Embedded anonymous fields don't get tagged as unexported
			// due to https://code.google.com/p/go/issues/detail?id=7247
			// TODO fix in go 1.5.
			// embed
		}{
			CharmMetadata: ch.Meta(),
		},
	}, {
		about: "metadata not appropriate for charm",
		id:    "wordpress",
		expectResult: &struct {
			CharmMetadata  *charm.Meta
			BundleMetadata *charm.BundleData
		}{
			CharmMetadata: ch.Meta(),
		},
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		// Make a result value of the same type as the expected result,
		// but empty.
		result := reflect.New(reflect.TypeOf(test.expectResult).Elem()).Interface()
		id, err := s.client.Meta(mustParseReference(test.id), result)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			if code, ok := errgo.Cause(err).(params.ErrorCode); ok {
				c.Assert(code, gc.Equals, test.expectErrorCode)
			} else {
				c.Assert(test.expectErrorCode, gc.Equals, params.ErrorCode(""))
			}
			c.Assert(id, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(id, jc.DeepEquals, url)
		c.Assert(result, jc.DeepEquals, test.expectResult)
	}
}

type errorReader struct {
	error string
}

func (e *errorReader) Read(buf []byte) (int, error) {
	return 0, errgo.New(e.error)
}

type cannedRoundTripper struct {
	resp  *http.Response
	error error
}

func (r *cannedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.resp, r.error
}

func mustParseReference(url string) *charm.Reference {
	// TODO implement MustParseReference in charm.
	ref, err := charm.ParseReference(url)
	if err != nil {
		panic(err)
	}
	return ref
}

func mustMarshalJSON(x interface{}) []byte {
	data, err := json.Marshal(x)
	if err != nil {
		panic(err)
	}
	return data
}
