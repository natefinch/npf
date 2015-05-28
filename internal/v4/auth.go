// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"encoding/base64"
	"net/http"
	"strings"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charmrepo.v0/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

const (
	basicRealm        = "CharmStore4"
	promulgatorsGroup = "promulgators"
)

// authorize checks that the current user is authorized based on the provided
// ACL and optional entity. If an authenticated user is required, authorize tries to retrieve the
// current user in the following ways:
// - by checking that the request's headers HTTP basic auth credentials match
//   the superuser credentials stored in the API handler;
// - by checking that there is a valid macaroon in the request's cookies.
// A params.ErrUnauthorized error is returned if superuser credentials fail;
// otherwise a macaroon is minted and a httpbakery discharge-required
// error is returned holding the macaroon.
func (h *Handler) authorize(req *http.Request, acl []string, alwaysAuth bool, entityId *router.ResolvedURL) (authorization, error) {
	logger.Infof(
		"authorize, bakery %p, auth location %q, acl %q, path: %q, method: %q",
		h.pool.Bakery,
		h.config.IdentityLocation,
		acl,
		req.URL.Path,
		req.Method)

	if !alwaysAuth {
		// No need to authenticate if the ACL is open to everyone.
		for _, name := range acl {
			if name == params.Everyone {
				return authorization{}, nil
			}
		}
	}

	auth, verr := h.checkRequest(req, entityId)
	if verr == nil {
		if err := h.checkACLMembership(auth, acl); err != nil {
			return authorization{}, errgo.WithCausef(err, params.ErrUnauthorized, "")
		}
		return auth, nil
	}
	if _, ok := errgo.Cause(verr).(*bakery.VerificationError); !ok {
		return authorization{}, errgo.Mask(verr, errgo.Is(params.ErrUnauthorized))
	}

	// Macaroon verification failed: mint a new macaroon.
	m, err := h.newMacaroon()
	if err != nil {
		return authorization{}, errgo.Notef(err, "cannot mint macaroon")
	}
	// Request that this macaroon be supplied for all requests
	// to the whole handler.
	// TODO use a relative URL here: router.RelativeURLPath(req.RequestURI, "/")
	cookiePath := "/"
	return authorization{}, httpbakery.NewDischargeRequiredError(m, cookiePath, verr)
}

// checkRequest checks for any authorization tokens in the request and returns any
// found as an authorization. If no suitable credentials are found, or an error occurs,
// then a zero valued authorization is returned.
// It also checks any first party caveats. If the entityId is provided, it will
// be used to check any "is-entity" first party caveat.
func (h *Handler) checkRequest(req *http.Request, entityId *router.ResolvedURL) (authorization, error) {
	user, passwd, err := parseCredentials(req)
	if err == nil {
		if user != h.config.AuthUsername || passwd != h.config.AuthPassword {
			return authorization{}, errgo.WithCausef(nil, params.ErrUnauthorized, "invalid user name or password")
		}
		return authorization{Admin: true}, nil
	}
	if errgo.Cause(err) != errNoCreds || h.pool.Bakery == nil || h.config.IdentityLocation == "" {
		return authorization{}, errgo.WithCausef(err, params.ErrUnauthorized, "authentication failed")
	}
	attrMap, err := httpbakery.CheckRequest(h.pool.Bakery, req, nil, checkers.New(
		checkers.CheckerFunc{
			Condition_: "is-entity",
			Check_: func(_, arg string) error {
				if entityId == nil {
					return errgo.Newf("API operation does not involve expected entity %v", arg)
				}
				purl := entityId.PromulgatedURL()
				if entityId.URL.String() == arg || purl != nil && purl.String() == arg {
					// We allow either the non-promulgated or the promulgated
					// URL form.
					return nil
				}
				return errgo.Newf("API operation on entity %v, want %v", entityId, arg)
			},
		},
	))
	if err != nil {
		return authorization{}, errgo.Mask(err, errgo.Any)
	}
	return authorization{
		Admin:    false,
		Username: attrMap[usernameAttr],
	}, nil
}

// AuthorizeEntity checks that the given HTTP request
// can access the entity with the given id.
func (h *Handler) AuthorizeEntity(id *router.ResolvedURL, req *http.Request) error {
	store := h.pool.Store()
	defer store.Close()
	baseEntity, err := store.FindBaseEntity(&id.URL, "acls")
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			return errgo.WithCausef(nil, params.ErrNotFound, "entity %q not found", id)
		}
		return errgo.Notef(err, "cannot retrieve entity %q for authorization", id)
	}
	return h.authorizeWithPerms(req, baseEntity.ACLs.Read, baseEntity.ACLs.Write, id)
}

func (h *Handler) authorizeWithPerms(req *http.Request, read, write []string, entityId *router.ResolvedURL) error {
	var acl []string
	switch req.Method {
	case "DELETE", "PATCH", "POST", "PUT":
		acl = write
	default:
		acl = read
	}
	_, err := h.authorize(req, acl, false, entityId)
	return err
}

const usernameAttr = "username"

// authorization conatains authorization information extracted from an HTTP request.
// The zero value for a authorization contains no privileges.
type authorization struct {
	Admin    bool
	Username string
}

func (h *Handler) groupsForUser(username string) ([]string, error) {
	if h.config.IdentityAPIURL == "" {
		logger.Debugf("IdentityAPIURL not configured, not retrieving groups for %s", username)
		return nil, nil
	}
	// TODO cache groups for a user
	return h.identityClient.GroupsForUser(username)
}

func (h *Handler) checkACLMembership(auth authorization, acl []string) error {
	if auth.Admin {
		return nil
	}
	if auth.Username == "" {
		return errgo.New("no username declared")
	}
	// First check if access is granted without querying for groups.
	for _, name := range acl {
		if name == auth.Username || name == params.Everyone {
			return nil
		}
	}
	groups, err := h.groupsForUser(auth.Username)
	if err != nil {
		logger.Errorf("cannot get groups for %q: %v", auth.Username, err)
		return errgo.Newf("access denied for user %q", auth.Username)
	}
	for _, name := range acl {
		for _, g := range groups {
			if g == name {
				return nil
			}
		}
	}
	return errgo.Newf("access denied for user %q", auth.Username)
}

func (h *Handler) newMacaroon() (*macaroon.Macaroon, error) {
	// TODO generate different caveats depending on the requested operation
	// and whether there's a charm id or not.
	// Mint an appropriate macaroon and send it back to the client.
	return h.pool.Bakery.NewMacaroon("", nil, []checkers.Caveat{checkers.NeedDeclaredCaveat(checkers.Caveat{
		Location:  h.config.IdentityLocation,
		Condition: "is-authenticated-user",
	}, usernameAttr)})
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
