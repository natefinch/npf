// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package router

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore/params"
)

var (
	HandleErrors = jsonhttp.HandleErrors(errToResp)
	HandleJSON   = jsonhttp.HandleJSON(errToResp)
	WriteError   = jsonhttp.WriteError(errToResp)
)

func errToResp(err error) (int, interface{}) {
	errorBody := errorResponseBody(err)
	status := http.StatusInternalServerError
	switch errorBody.Code {
	case params.ErrNotFound, params.ErrMetadataNotFound:
		status = http.StatusNotFound
	case params.ErrBadRequest:
		status = http.StatusBadRequest
	case params.ErrForbidden:
		status = http.StatusForbidden
	case params.ErrUnauthorized:
		status = http.StatusUnauthorized
	case params.ErrMethodNotAllowed:
		// TODO(rog) from RFC 2616, section 4.7: An Allow header
		// field MUST be present in a 405 (Method Not Allowed)
		// response.
		// Perhaps we should not ever return StatusMethodNotAllowed.
		status = http.StatusMethodNotAllowed
	}
	return status, errorBody
}

// errorResponse returns an appropriate error
// response for the provided error.
func errorResponseBody(err error) *params.Error {
	errResp := &params.Error{
		Message: err.Error(),
	}
	cause := errgo.Cause(err)
	if coder, ok := cause.(errorCoder); ok {
		errResp.Code = coder.ErrorCode()
	}
	if infoer, ok := cause.(errorInfoer); ok {
		errResp.Info = infoer.ErrorInfo()
	}
	return errResp
}

type errorInfoer interface {
	ErrorInfo() map[string]*params.Error
}

type errorCoder interface {
	ErrorCode() params.ErrorCode
}

// multiError holds multiple errors.
type multiError map[string]error

func (err multiError) Error() string {
	return fmt.Sprintf("multiple (%d) errors", len(err))
}

func (err multiError) ErrorCode() params.ErrorCode {
	return params.ErrMultipleErrors
}

func (err multiError) ErrorInfo() map[string]*params.Error {
	m := make(map[string]*params.Error)
	for key, err := range err {
		m[key] = errorResponseBody(err)
	}
	return m
}

// NotFoundHandler is like http.NotFoundHandler except it
// returns a JSON error response.
func NotFoundHandler() http.Handler {
	return HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		return errgo.WithCausef(nil, params.ErrNotFound, params.ErrNotFound.Error())
	})
}

func NewServeMux() *ServeMux {
	return &ServeMux{http.NewServeMux()}
}

// ServeMux is like http.ServeMux but returns
// JSON errors when pages are not found.
type ServeMux struct {
	*http.ServeMux
}

func (mux *ServeMux) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.RequestURI == "*" {
		mux.ServeMux.ServeHTTP(w, req)
		return
	}
	h, pattern := mux.Handler(req)
	if pattern == "" {
		WriteError(w, errgo.WithCausef(nil, params.ErrNotFound, "no handler for %q", req.URL.Path))
		return
	}
	h.ServeHTTP(w, req)
}

// RelativeURLPath returns a relative URL path that is lexically equivalent to
// targpath when interpreted by url.URL.ResolveReference.
// On succes, the returned path will always be relative to basePath, even if basePath
// and targPath share no elements. An error is returned if targPath can't
// be made relative to basePath (for example when either basePath
// or targetPath are non-absolute).
func RelativeURLPath(basePath, targPath string) (string, error) {
	if !strings.HasPrefix(basePath, "/") {
		return "", errgo.Newf("non-absolute base URL")
	}
	if !strings.HasPrefix(targPath, "/") {
		return "", errgo.Newf("non-absolute target URL")
	}
	baseParts := strings.Split(basePath, "/")
	targParts := strings.Split(targPath, "/")

	// For the purposes of dotdot, the last element of
	// the paths are irrelevant. We save the last part
	// of the target path for later.
	lastElem := targParts[len(targParts)-1]
	baseParts = baseParts[0 : len(baseParts)-1]
	targParts = targParts[0 : len(targParts)-1]

	// Find the common prefix between the two paths:
	var i int
	for ; i < len(baseParts); i++ {
		if i >= len(targParts) || baseParts[i] != targParts[i] {
			break
		}
	}
	dotdotCount := len(baseParts) - i
	targOnly := targParts[i:]
	result := make([]string, 0, dotdotCount+len(targOnly)+1)
	for i := 0; i < dotdotCount; i++ {
		result = append(result, "..")
	}
	result = append(result, targOnly...)
	result = append(result, lastElem)
	return strings.Join(result, "/"), nil
}
