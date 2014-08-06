// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	jc "github.com/juju/testing/checkers"
	gc "launchpad.net/gocheck"
)

// JSONCallParams holds parameters for AssertJSONCall.
// If left empty, some fields will automatically be filled
// with defaults.
type JSONCallParams struct {
	// Handler holds the handler to use to make the request.
	Handler http.Handler

	// Method holds the HTTP method to use for the call.
	// GET is assumed if this is empty.
	Method string

	// URL holds the URL to pass when making the request.
	URL string

	// Body holds the body to send in the request.
	Body string

	// BodyContentType holds the content type of the
	// body. If this is empty, the a content type of
	// "text/plain; charset=utf-8"  is assumed.
	BodyContentType string

	// ExpectCode holds the expected HTTP status code.
	// http.StatusOK is assumed if this is zero.
	ExpectCode int

	// ExpectBody holds the expected JSON body.
	ExpectBody interface{}
}

// AssertJSONCall asserts that when the given handler is called with
// the given parameters, the result is as specified.
func AssertJSONCall(c *gc.C, p JSONCallParams) {
	if p.Method == "" {
		p.Method = "GET"
	}
	if p.ExpectCode == 0 {
		p.ExpectCode = http.StatusOK
	}
	rec := DoRequest(c, p.Handler, p.Method, p.URL, p.Body, p.BodyContentType, nil)
	c.Assert(rec.Code, gc.Equals, p.ExpectCode, gc.Commentf("body: %s", rec.Body.Bytes()))
	if p.ExpectBody == nil {
		c.Assert(rec.Body.Bytes(), gc.HasLen, 0)
		return
	}
	// Rather than unmarshaling into something of the expected
	// body type, we reform the expected body in JSON and
	// back to interface{}, so we can check the whole content.
	// Otherwise we lose information when unmarshaling.
	expectBodyBytes, err := json.Marshal(p.ExpectBody)
	c.Assert(err, gc.IsNil)
	var expectBodyVal interface{}
	err = json.Unmarshal(expectBodyBytes, &expectBodyVal)
	c.Assert(err, gc.IsNil)

	var gotBodyVal interface{}
	err = json.Unmarshal(rec.Body.Bytes(), &gotBodyVal)
	c.Assert(err, gc.IsNil, gc.Commentf("json body: %q", rec.Body.Bytes()))
	// TODO(rog) check that content type is application/json
	c.Assert(gotBodyVal, jc.DeepEquals, expectBodyVal)
}

// DoRequest invokes a request on the given handler with the given
// method, URL, body, body content type and headers.
func DoRequest(c *gc.C, handler http.Handler, method string, urlStr string, body, bodyContentType string, header map[string][]string) *httptest.ResponseRecorder {
	req, err := http.NewRequest(method, urlStr, strings.NewReader(body))
	c.Assert(err, gc.IsNil)
	if header != nil {
		req.Header = header
	}
	if bodyContentType == "" {
		bodyContentType = "text/plain; charset=utf-8"
	}
	req.Header.Set("Content-Type", bodyContentType)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}
