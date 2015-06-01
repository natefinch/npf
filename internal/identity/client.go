// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

// Package identity implements a client for identity.
package identity // import "gopkg.in/juju/charmstore.v5-unstable/internal/identity"

import (
	"net/http"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v1/httpbakery"

	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
)

var logger = loggo.GetLogger("charmstore.internal.identity")

// Params provides the parameters to be passed when creating a new
// client.
type Params struct {
	URL    string
	Client *httpbakery.Client
}

// Client provides a client that can be used to query an identity server.
type Client struct {
	p *Params
}

// NewClient creates a new Client.
func NewClient(p *Params) *Client {
	return &Client{
		p: p,
	}
}

// endpoint adds the endpoint to the identity URL.
func (c *Client) endpoint(ep string) string {
	return c.p.URL + ep
}

// get performs an http get using c.Client.
func (c *Client) get(path string) (*http.Response, error) {
	u := c.endpoint(path)
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, errgo.Notef(err, "cannot create request for %q", u)
	}
	resp, err := c.p.Client.Do(req)
	if err != nil {
		return nil, errgo.Notef(err, "cannot GET %q", u)
	}
	return resp, nil
}

// GetJSON performs a JSON request on the identity server at the
// specified path. The returned value is unmarshalled to v.
func (c *Client) GetJSON(path string, v interface{}) error {
	resp, err := c.get(path)
	if err != nil {
		return errgo.Notef(err, "cannot GET %q", path)
	}
	return router.UnmarshalJSONResponse(resp, v, getError)
}

// GroupsForUser gets the list of groups to which the specified user
// belongs.
func (c *Client) GroupsForUser(username string) ([]string, error) {
	var groups []string
	if err := c.GetJSON("/v1/u/"+username+"/groups", &groups); err != nil {
		return nil, errgo.Notef(err, "cannot get groups for %s", username)
	}
	return groups, nil
}

// idmError is the error that might be returned by identity.
type idmError struct {
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

func (e idmError) Error() string {
	return e.Message
}

// getError tries to retrieve the error from a failed query. If the
// response does not contain an error the status line is used to create
// an error.
func getError(r *http.Response) error {
	var ierr idmError
	if err := router.UnmarshalJSONResponse(r, &ierr, nil); err != nil {
		logger.Errorf("could not unmarshal error: %s", err)
		return errgo.Newf("bad status %q", r.Status)
	}
	return ierr
}
