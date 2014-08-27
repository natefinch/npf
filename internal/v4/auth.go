// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/juju/errgo"
	"gopkg.in/juju/charm.v3"

	"github.com/juju/charmstore/internal/router"
	"github.com/juju/charmstore/params"
)

const basicRealm = "CharmStore4"

// authRequired decorates the given function so that an "unathorized"
// response is returned if the HTTP basic auth in the request fails.
// For the time being, authentication is done by simply checking that the
// provided user/password match the superuser credentials stored in the
// API handler.
func (h *handler) authRequired(f router.IdHandler) router.IdHandler {
	return func(charmId *charm.Reference, w http.ResponseWriter, req *http.Request) error {
		if err := authenticate(req, h.config.AuthUsername, h.config.AuthPassword); err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+basicRealm+`"`)
			return err
		}
		return f(charmId, w, req)
	}
}

// authenticate checks that the request's headers HTTP basic auth credentials
// match the given username and password.
// A params.ErrUnauthorized is returned if the authentication fails.
func authenticate(req *http.Request, username, password string) error {
	user, passwd, err := parseCredentials(req)
	if err != nil {
		return errgo.WithCausef(err, params.ErrUnauthorized, "authentication failed")
	}
	if (user != username) || (passwd != password) {
		return errgo.WithCausef(nil, params.ErrUnauthorized, "invalid user name or password")
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
