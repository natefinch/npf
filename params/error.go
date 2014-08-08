package params

import (
	"fmt"
)

// ErrorCode holds the class of an error in machine-readable format.
type ErrorCode string

func (code ErrorCode) Error() string {
	return string(code)
}

const (
	ErrNotFound         ErrorCode = "not found"
	ErrMetadataNotFound ErrorCode = "metadata not found"
	ErrForbidden        ErrorCode = "forbidden"
	ErrBadRequest       ErrorCode = "bad request"
	ErrDuplicateUpload  ErrorCode = "duplicate upload"
)

// Error represents an error - it is returned for any response
// that fails.
// See http://tinyurl.com/knr3csp .
type Error struct {
	Message string
	Code    ErrorCode
}

// NewError returns a new *Error with the given error code
// and message.
func NewError(code ErrorCode, f string, a ...interface{}) error {
	return &Error{
		Message: fmt.Sprintf(f, a...),
		Code:    code,
	}
}

// Error implements error.Error.
func (e *Error) Error() string {
	return e.Message
}

// ErrorCode holds the class of the error in
// machine readable format.
func (e *Error) ErrorCode() string {
	return e.Code.Error()
}

// Cause implements errgo.Causer.Cause.
func (e *Error) Cause() error {
	if e.Code != "" {
		return e.Code
	}
	return nil
}
