// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The params package holds types that are a part of the charm store's external
// contract - they will be marshalled (or unmarshalled) as JSON
// and delivered through the HTTP API.
package params

import (
	"gopkg.in/juju/charm.v2"
)

// Error represents an error - it is returned for any response
// that fails.
// See http://tinyurl.com/knr3csp .
type Error struct {
	Message string
	Code    string
}

// Error implements error.Error.
func (e *Error) Error() string {
	return e.Message
}

// ErrorCode holds the class of the error in
// machine readable format.
// TODO list of possible error codes.
func (e *Error) ErrorCode() string {
	return e.Code
}

// ErrorCoder is the type of any error that is
// associated with an error code.
type ErrorCoder interface {
	ErrorCode() string
}

// MetaAnyResponse holds the result of a meta/any
// request. See http://tinyurl.com/q5vcjpk
type MetaAnyResponse struct {
	Id   *charm.URL
	Meta map[string]interface{} `json:",omitempty"`
}
