// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/base64"
	"net/http"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/macaroon.v1"

	"github.com/juju/charmstore/params"
)

const basicRealm = "CharmStore4"

type operation string

const (
	opChange operation = "change"
	opGet    operation = "get"
)

// authenticate checks that the request's headers HTTP basic auth credentials
// match the superuser credentials stored in the API handler.
// A params.ErrUnauthorized is returned if the authentication fails.
func (h *Handler) authenticate(w http.ResponseWriter, req *http.Request, id *charm.Reference, op operation) error {
	logger.Infof("authenticate, bakery %p, auth location %q", h.store.Bakery, h.config.AuthLocation)

	// If basic auth credentials are presented, use them,
	// otherwise use macaroon third-party caveat verification.

	user, passwd, err := parseCredentials(req)
	if err == nil {
		if user != h.config.AuthUsername || passwd != h.config.AuthPassword {
			return unauthorizedBasic(w, "invalid user name or password", nil)
		}
		return nil
	}
	if err != errNoCreds || h.store.Bakery == nil || h.config.AuthLocation == "" {
		return unauthorizedBasic(w, "authentication failed", err)
	}
	attrs, verr := httpbakery.CheckRequest(h.store.Bakery, req, nil, checkers.New())
	if verr == nil {
		logger.Infof("authenticated with attrs %q", attrs)
		return nil
	}
	if _, ok := errgo.Cause(verr).(*bakery.VerificationError); !ok {
		return errgo.Mask(verr)
	}
	m, err := h.newMacaroon()
	if err != nil {
		return errgo.Notef(err, "cannot mint macaroon")
	}
	return httpbakery.NewDischargeRequiredError(m, verr)
}

func (h *Handler) newMacaroon() (*macaroon.Macaroon, error) {
	// TODO generate different caveats depending on the requested operation
	// and whether there's a charm id or not.
	// Mint an appropriate macaroon and send it back to the client.
	return h.store.Bakery.NewMacaroon("", nil, []checkers.Caveat{{
		Location: h.config.AuthLocation,
		// TODO needs-declared user is-authenticated-user
		Condition: "is-authenticated-user",
	}})
}

var errNoCreds = errgo.New("missing HTTP auth header")

// parseCredentials parses the given request and returns the HTTP basic auth
// credentials included in its header.
func parseCredentials(req *http.Request) (username, password string, err error) {
	auth := req.Header.Get("Authorization")
	if auth == "" {
		return "", "", errNoCreds
	}
	parts := strings.Fields(auth)
	if len(parts) != 2 || parts[0] != "Basic" {
		return "", "", errgo.New("invalid HTTP auth header")
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

func unauthorizedBasic(w http.ResponseWriter, message string, underlying error) error {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+basicRealm+`"`)
	return errgo.WithCausef(underlying, params.ErrUnauthorized, message)
}
