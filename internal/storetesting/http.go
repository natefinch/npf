// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"

	jc "github.com/juju/testing/checkers"
	gc "launchpad.net/gocheck"
)

// AssertJSONCall asserts that when the given handler is called with
// the given method, URL, and body, the result has the expected
// status code and body.
func AssertJSONCall(
	c *gc.C,
	handler http.Handler,
	method string,
	urlStr string,
	body string,
	expectCode int,
	expectBody interface{},
) {
	rec := DoRequest(c, handler, method, urlStr, body)
	c.Assert(rec.Code, gc.Equals, expectCode, gc.Commentf("body: %s", rec.Body.Bytes()))
	if expectBody == nil {
		c.Assert(rec.Body.Bytes(), gc.HasLen, 0)
		return
	}
	resp := reflect.New(reflect.TypeOf(expectBody))
	err := json.Unmarshal(rec.Body.Bytes(), resp.Interface())
	c.Assert(err, gc.IsNil)
	c.Assert(resp.Elem().Interface(), jc.DeepEquals, expectBody)
}

// DoRequest invokes a request on the given handler with the given
// method, URL and body.
func DoRequest(c *gc.C, handler http.Handler, method string, urlStr string, body string) *httptest.ResponseRecorder {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, urlStr, r)
	c.Assert(err, gc.IsNil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
