// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/base64"
	"net/http"
	"strings"

	"gopkg.in/errgo.v1"

	"github.com/juju/charmstore/params"
)

const basicRealm = "CharmStore4"

// authenticate checks that the request's headers HTTP basic auth credentials
// match the superuser credentials stored in the API handler.
// A params.ErrUnauthorized is returned if the authentication fails.
func (h *Handler) authenticate(w http.ResponseWriter, req *http.Request) error {
	user, passwd, err := parseCredentials(req)
	if err != nil {
		return unauthorized(w, "authentication failed", err)
	}
	if user != h.config.AuthUsername || passwd != h.config.AuthPassword {
		return unauthorized(w, "invalid user name or password", nil)
	}
	return nil
}

// parseCredentials parses the given request and returns the HTTP basic auth
// credentials included in its header.
func parseCredentials(req *http.Request) (username, password string, err error) {
	parts := strings.Fields(req.Header.Get("Authorization"))
	if len(parts) != 2 || parts[0] != "Basic" {
		return "", "", errgo.New("invalid or missing HTTP auth header")
	}
	// Challenge is a base64-encoded "tag:pass" string.
	// See RFC 2617, Section 2.
	challenge, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errgo.New("invalid HTTP auth encoding")
	}
	tokens := strings.SplitN(string(challenge), ":", 2)
	if len(tokens) != 2 {
		return "", "", errgo.New("invalid HTTP auth contents")
	}
	return tokens[0], tokens[1], nil
}

func unauthorized(w http.ResponseWriter, message string, underlying error) error {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+basicRealm+`"`)
	return errgo.WithCausef(underlying, params.ErrUnauthorized, message)
}
