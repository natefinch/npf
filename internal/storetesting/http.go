// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

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
	Body io.Reader

	// Header specifies the HTTP headers to use when making
	// the request.
	Header http.Header

	// ContentLength specifies the length of the body.
	// It may be zero, in which case the default net/http
	// content-length behaviour will be used.
	ContentLength int64

	// Username, if specified, is used for HTTP basic authentication.
	Username string

	// Password, if specified, is used for HTTP basic authentication.
	Password string

	// ExpectCode holds the expected HTTP status code.
	// http.StatusOK is assumed if this is zero.
	// TODO(rog) change this to ExpectStatus
	ExpectCode int

	// ExpectBody holds the expected JSON body.
	ExpectBody interface{}
}

// AssertJSONCall asserts that when the given handler is called with
// the given parameters, the result is as specified.
func AssertJSONCall(c *gc.C, p JSONCallParams) {
	c.Logf("JSON call, url %q", p.URL)
	if p.Method == "" {
		p.Method = "GET"
	}
	if p.ExpectCode == 0 {
		p.ExpectCode = http.StatusOK
	}
	rec := DoRequest(c, p.Handler, p.Method, p.URL, p.Body, p.ContentLength, p.Header, p.Username, p.Password)
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
// method, URL, body, content length and headers.
func DoRequest(c *gc.C, handler http.Handler, method string, urlStr string, body io.Reader, contentLength int64, header map[string][]string, username, password string) *httptest.ResponseRecorder {
	// TODO frankban: this function has too many arguments.
	//   Use something like RequestParams.
	srv := httptest.NewServer(handler)
	defer srv.Close()

	urlStr = srv.URL + urlStr

	req, err := http.NewRequest(method, urlStr, body)
	c.Assert(err, gc.IsNil)
	if header != nil {
		req.Header = header
	}
	if contentLength != 0 {
		req.ContentLength = contentLength
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := http.DefaultClient.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()

	// TODO(rog) don't return a ResponseRecorder because we're not actually
	// using httptest.NewRecorder ?
	var rec httptest.ResponseRecorder
	rec.HeaderMap = resp.Header
	rec.Code = resp.StatusCode
	rec.Body = new(bytes.Buffer)
	_, err = io.Copy(rec.Body, resp.Body)
	c.Assert(err, gc.IsNil)
	return &rec
}
