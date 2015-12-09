// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v5 // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

const (
	PromulgatorsGroup = "charmers"
	// opAccessCharmWitTerms indicates an operation of accessing the archive of
	// a charm that requires agreement to certain terms and conditions.
	opAccessCharmWithTerms = "op-get-with-terms"
	// opOther indicated all other operations.
	opOther               = "op-other"
	defaultMacaroonExpiry = 24 * time.Hour
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
//
// This method also sets h.auth to the returned authorization info.
func (h *ReqHandler) authorize(req *http.Request, acl []string, alwaysAuth bool, entityId *router.ResolvedURL) (authorization, error) {
	logger.Infof(
		"authorize, auth location %q, acl %q, path: %q, method: %q, entity: %#v",
		h.handler.config.IdentityLocation,
		acl,
		req.URL.Path,
		req.Method,
		entityId)

	if !alwaysAuth {
		// No need to authenticate if the ACL is open to everyone.
		for _, name := range acl {
			if name == params.Everyone {
				return authorization{}, nil
			}
		}
	}
	entities := []*router.ResolvedURL{}
	if entityId != nil {
		entities = append(entities, entityId)
	}

	auth, verr := h.checkRequest(req, entities, opOther)
	if verr == nil {
		if err := h.checkACLMembership(auth, acl); err != nil {
			return authorization{}, errgo.WithCausef(err, params.ErrUnauthorized, "")
		}
		h.auth = auth
		return auth, nil
	}
	if _, ok := errgo.Cause(verr).(*bakery.VerificationError); !ok {
		return authorization{}, errgo.Mask(verr, errgo.Is(params.ErrUnauthorized))
	}

	// Macaroon verification failed: mint a new macaroon.
	// We need to deny access for opAccessCharmWithTerms operations because they
	// may require more specific checks that terms and conditions have been
	// satisfied.
	m, err := h.newMacaroon(checkers.DenyCaveat(opAccessCharmWithTerms))
	if err != nil {
		return authorization{}, errgo.Notef(err, "cannot mint macaroon")
	}

	// Request that this macaroon be supplied for all requests
	// to the whole handler.
	// TODO use a relative URL here: router.RelativeURLPath(req.RequestURI, "/")
	cookiePath := "/"
	return authorization{}, httpbakery.NewDischargeRequiredErrorForRequest(m, cookiePath, verr, req)
}

// authorizeEntityAndTerms is similar to the authorize method, but
// in addition it also checks if the entity meta data specifies
// and terms and conditions that the user needs to agree to. If so,
// it will require the user to agree to those terms and conditions
// by adding a third party caveat addressed to the terms service
// requiring the user to have agreements to specified terms.
func (h *ReqHandler) authorizeEntityAndTerms(req *http.Request, entityIds []*router.ResolvedURL) (authorization, error) {
	logger.Infof(
		"authorize entity and terms, auth location %q, terms location %q, path: %q, method: %q, entities: %#v",
		h.handler.config.IdentityLocation,
		h.handler.config.TermsLocation,
		req.URL.Path,
		req.Method,
		entityIds)

	if len(entityIds) == 0 {
		return authorization{}, errgo.WithCausef(nil, params.ErrUnauthorized, "entity id not specified")
	}

	ACLs := make([][]string, len(entityIds))
	requiredTerms := make(map[string]struct{})
	allowAuth := true
	for i, entityId := range entityIds {
		entity, err := h.Store.FindEntity(entityId)
		if err != nil {
			return authorization{}, errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		if entity == nil {
			return authorization{}, errgo.WithCausef(nil, params.ErrNotFound, "could not find entity %q", entityId.String())
		}

		baseEntity, err := h.Store.FindBaseEntity(&entityId.URL, "acls", "developmentacls")
		if err != nil {
			return authorization{}, errgo.Mask(err, errgo.Is(params.ErrNotFound))
		}
		if baseEntity == nil {
			return authorization{}, errgo.WithCausef(nil, params.ErrNotFound, "could not find the base entity %v", entityId.URL.String())
		}

		ACLs[i] = baseEntity.ACLs.Read
		if entityId.Development {
			ACLs[i] = baseEntity.DevelopmentACLs.Read
		}

		if (entity.CharmMeta == nil) || len(entity.CharmMeta.Terms) == 0 {
			// No need to authenticate if the ACL is open to everyone.
			open := false
			for _, name := range ACLs[i] {
				if name == params.Everyone {
					open = true
					break
				}
			}
			allowAuth = allowAuth && open
		} else {
			allowAuth = false
			for _, term := range entity.CharmMeta.Terms {
				requiredTerms[term] = struct{}{}
			}
		}
	}
	// if all entities are open to everyone and non of the entities defines any Terms, then we return nil
	if allowAuth {
		return authorization{}, nil
	}

	if len(requiredTerms) > 0 && h.handler.config.TermsLocation == "" {
		return authorization{}, errgo.WithCausef(nil, params.ErrUnauthorized, "charmstore not configured to serve charms with terms and conditions")
	}

	operation := opOther
	if len(requiredTerms) > 0 {
		operation = opAccessCharmWithTerms
	}

	auth, verr := h.checkRequest(req, entityIds, operation)
	if verr == nil {
		for _, acl := range ACLs {
			if err := h.checkACLMembership(auth, acl); err != nil {
				return authorization{}, errgo.WithCausef(err, params.ErrUnauthorized, "")
			}
		}
		h.auth = auth
		return auth, nil
	}
	if _, ok := errgo.Cause(verr).(*bakery.VerificationError); !ok {
		return authorization{}, errgo.Mask(verr, errgo.Is(params.ErrUnauthorized))
	}

	caveats := []checkers.Caveat{}
	if len(requiredTerms) > 0 {
		terms := []string{}
		for term, _ := range requiredTerms {
			terms = append(terms, term)
		}
		caveats = append(caveats,
			checkers.Caveat{h.handler.config.TermsLocation, "has-agreed " + strings.Join(terms, " ")},
		)
	}

	// Macaroon verification failed: mint a new macaroon.
	m, err := h.newMacaroon(caveats...)
	if err != nil {
		return authorization{}, errgo.Notef(err, "cannot mint macaroon")
	}

	// Request that this macaroon be supplied for all requests
	// to the whole handler.
	// TODO use a relative URL here: router.RelativeURLPath(req.RequestURI, "/")
	cookiePath := "/"
	return authorization{}, httpbakery.NewDischargeRequiredErrorForRequest(m, cookiePath, verr, req)
}

// checkRequest checks for any authorization tokens in the request and returns any
// found as an authorization. If no suitable credentials are found, or an error occurs,
// then a zero valued authorization is returned.
// It also checks any first party caveats. If the entityId is provided, it will
// be used to check any "is-entity" first party caveat.
// In addition it adds a checker that checks if operation specified
// by the operation parameters is allowed.
func (h *ReqHandler) checkRequest(req *http.Request, entityIds []*router.ResolvedURL, operation string) (authorization, error) {
	user, passwd, err := parseCredentials(req)
	if err == nil {
		if user != h.handler.config.AuthUsername || passwd != h.handler.config.AuthPassword {
			return authorization{}, errgo.WithCausef(nil, params.ErrUnauthorized, "invalid user name or password")
		}
		return authorization{Admin: true}, nil
	}
	bk := h.Store.Bakery
	if errgo.Cause(err) != errNoCreds || bk == nil || h.handler.config.IdentityLocation == "" {
		return authorization{}, errgo.WithCausef(err, params.ErrUnauthorized, "authentication failed")
	}

	attrMap, err := httpbakery.CheckRequest(bk, req, nil, checkers.New(
		checkers.CheckerFunc{
			Condition_: "is-entity",
			Check_: func(_, args string) error {
				return areAllowedEntities(entityIds, args)
			},
		},
		checkers.OperationChecker(operation),
	))
	if err != nil {
		return authorization{}, errgo.Mask(err, errgo.Any)
	}
	return authorization{
		Admin:    false,
		Username: attrMap[UsernameAttr],
	}, nil
}

// areAllowedEntities checks if all entityIds are in the allowedEntities list (space
// separated).
func areAllowedEntities(entityIds []*router.ResolvedURL, allowedEntities string) error {
	allowedEntitiesMap := make(map[string]bool)
	for _, curl := range strings.Fields(allowedEntities) {
		allowedEntitiesMap[curl] = true
	}
	if len(entityIds) == 0 {
		return errgo.Newf("API operation does not involve expected entity %v", allowedEntities)
	}

	for _, entityId := range entityIds {
		if allowedEntitiesMap[entityId.URL.String()] {
			continue
		}
		purl := entityId.PromulgatedURL()
		if purl != nil {
			if allowedEntitiesMap[purl.String()] {
				continue
			}
		}
		return errgo.Newf("API operation on entity %v not allowed", entityId.String())
	}
	return nil
}

// AuthorizeEntity checks that the given HTTP request
// can access the entity with the given id.
func (h *ReqHandler) AuthorizeEntity(id *router.ResolvedURL, req *http.Request) error {
	baseEntity, err := h.Store.FindBaseEntity(&id.URL, "acls", "developmentacls")
	if err != nil {
		if errgo.Cause(err) == params.ErrNotFound {
			return errgo.WithCausef(nil, params.ErrNotFound, "entity %q not found", id)
		}
		return errgo.Notef(err, "cannot retrieve entity %q for authorization", id)
	}
	acls := baseEntity.ACLs
	if id.Development {
		acls = baseEntity.DevelopmentACLs
	}
	return h.authorizeWithPerms(req, acls.Read, acls.Write, id)
}

func (h *ReqHandler) authorizeWithPerms(req *http.Request, read, write []string, entityId *router.ResolvedURL) error {
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

const UsernameAttr = "username"

// authorization conatains authorization information extracted from an HTTP request.
// The zero value for a authorization contains no privileges.
type authorization struct {
	Admin    bool
	Username string
}

func (h *ReqHandler) groupsForUser(username string) ([]string, error) {
	if h.handler.config.IdentityAPIURL == "" {
		logger.Debugf("IdentityAPIURL not configured, not retrieving groups for %s", username)
		return nil, nil
	}
	// TODO cache groups for a user
	return h.handler.identityClient.GroupsForUser(username)
}

func (h *ReqHandler) checkACLMembership(auth authorization, acl []string) error {
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

func (h *ReqHandler) newMacaroon(caveats ...checkers.Caveat) (*macaroon.Macaroon, error) {
	caveats = append(caveats,
		checkers.NeedDeclaredCaveat(
			checkers.Caveat{
				Location:  h.handler.config.IdentityLocation,
				Condition: "is-authenticated-user",
			},
			UsernameAttr,
		),
		checkers.TimeBeforeCaveat(time.Now().Add(defaultMacaroonExpiry)),
	)
	// TODO generate different caveats depending on the requested operation
	// and whether there's a charm id or not.
	// Mint an appropriate macaroon and send it back to the client.
	return h.Store.Bakery.NewMacaroon("", nil, caveats)
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
