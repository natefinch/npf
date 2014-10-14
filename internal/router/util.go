// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package router

import (
	"fmt"
	"net/http"

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
