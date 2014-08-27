// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package router

import (
	"encoding/json"
	"net/http"

	"github.com/juju/errgo"

	"github.com/juju/charmstore/params"
)

// HandleErrors returns a Handler that calls the given function.
// If the function reports an error, it sets the HTTP response
// code and sends the error as a JSON reply by calling
// WriteError.
func HandleErrors(handle func(http.ResponseWriter, *http.Request) error) http.Handler {
	f := func(w http.ResponseWriter, req *http.Request) {
		if err := handle(w, req); err != nil {
			WriteError(w, err)
		}
	}
	return http.HandlerFunc(f)
}

// HandleJSON returns a Handler that calls the given function.
// The result is formatted as JSON.
// TODO(rog) remove ResponseWriter argument from function argument.
// It is redundant (and possibly dangerous) if used in combination with the interface{}
// return.
func HandleJSON(handle func(http.ResponseWriter, *http.Request) (interface{}, error)) http.Handler {
	f := func(w http.ResponseWriter, req *http.Request) error {
		val, err := handle(w, req)
		if err != nil {
			return errgo.Mask(err, errgo.Any)
		}
		return WriteJSON(w, http.StatusOK, val)
	}
	return HandleErrors(f)
}

// WriteError writes an JSON error response to the
// given ResponseWriter and sets an appropriate
// HTTP status.
func WriteError(w http.ResponseWriter, err error) {
	errResp := &params.Error{
		Message: err.Error(),
	}
	if err, ok := errgo.Cause(err).(params.ErrorCode); ok {
		errResp.Code = err
	}
	status := http.StatusInternalServerError
	switch errResp.Code {
	case params.ErrNotFound, params.ErrMetadataNotFound:
		status = http.StatusNotFound
	case params.ErrBadRequest:
		status = http.StatusBadRequest
	case params.ErrForbidden:
		status = http.StatusForbidden
	case params.ErrUnauthorized:
		status = http.StatusUnauthorized
	}
	// TODO log writeJSON error if it happens?
	WriteJSON(w, status, errResp)
}

// WriteJSON writes the given value to the ResponseWriter
// and sets the HTTP status to the given code.
func WriteJSON(w http.ResponseWriter, code int, val interface{}) error {
	// TODO consider marshalling directly to w using json.NewEncoder.
	// pro: this will not require a full buffer allocation.
	// con: if there's an error after the first write, it will be lost.
	data, err := json.Marshal(val)
	if err != nil {
		// TODO(rog) log an error if this fails and lose the
		// error return, because most callers will need
		// to do that anyway.
		return errgo.Mask(err)
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
	return nil
}

// NotFoundHandler is like http.NotFoundHandler except it
// returns a JSON error response.
func NotFoundHandler() http.Handler {
	return HandleErrors(func(w http.ResponseWriter, req *http.Request) error {
		return params.ErrNotFound
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
		WriteError(w, params.ErrNotFound)
		return
	}
	h.ServeHTTP(w, req)
}
