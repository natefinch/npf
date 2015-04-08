// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstoretesting

import (
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v5-unstable"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v4"
	"gopkg.in/juju/charmstore.v4/csclient"
	"gopkg.in/juju/charmstore.v4/params"
)

const (
	// If params.AuthUsername or params.AuthPassword are empty,
	// AuthUsername and AuthPassword will be used.
	AuthUsername = "charmstore-testing-user"
	AuthPassword = "charmstore-testing-password"
)

// OpenServer instantiates a new charm store server instance.
// Callers are responsible of closing the server by calling Close().
func OpenServer(c *gc.C, session *mgo.Session, params charmstore.ServerParams) *Server {
	db := session.DB("charmstore-testing")
	if params.AuthUsername == "" {
		params.AuthUsername = AuthUsername
	}
	if params.AuthPassword == "" {
		params.AuthPassword = AuthPassword
	}
	handler, err := charmstore.NewServer(db, nil, "", params, charmstore.V4)
	c.Assert(err, jc.ErrorIsNil)

	return &Server{
		srv:     httptest.NewServer(handler),
		handler: handler,
		params:  params,
	}
}

// Server is a charm store testing server.
type Server struct {
	srv     *httptest.Server
	handler http.Handler
	params  charmstore.ServerParams
}

// URL returns the URL the testing charm store is listening to.
func (s *Server) URL() string {
	return s.srv.URL
}

// Handler returns the HTTP handler used by this server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// Close shuts down the server.
func (s *Server) Close() {
	s.srv.Close()
}

func (s *Server) client() *csclient.Client {
	return csclient.New(csclient.Params{
		URL:      s.srv.URL,
		User:     s.params.AuthUsername,
		Password: s.params.AuthPassword,
	})
}

// UploadCharm uploads the given charm to the testing charm store.
// The given id must include the charm user, series and revision.
// If promulgated is true, the charm will be promulgated.
func (s *Server) UploadCharm(c *gc.C, ch charm.Charm, id *charm.Reference, promulgated bool) *charm.Reference {
	var path string

	// Validate the charm id.
	c.Assert(id.User, gc.Not(gc.Equals), "")
	c.Assert(id.Series, gc.Not(gc.Equals), "")
	c.Assert(id.Series, gc.Not(gc.Equals), "bundle")
	c.Assert(id.Revision, gc.Not(gc.Equals), -1)

	// Retrieve the charm archive path.
	switch ch := ch.(type) {
	case *charm.CharmArchive:
		path = ch.Path
	case *charm.CharmDir:
		f, err := ioutil.TempFile(c.MkDir(), "charm")
		c.Assert(err, jc.ErrorIsNil)
		defer f.Close()
		err = ch.ArchiveTo(f)
		c.Assert(err, jc.ErrorIsNil)
		path = f.Name()
	default:
		c.Errorf("cannot upload charm of entity type %T", ch)
	}

	// Retrieve the charm reader, hash and size.
	body, err := os.Open(path)
	c.Assert(err, jc.ErrorIsNil)
	defer body.Close()
	h := sha512.New384()
	size, err := io.Copy(h, body)
	c.Assert(err, jc.ErrorIsNil)
	hash := fmt.Sprintf("%x", h.Sum(nil))

	// Prepare the request.
	req, err := http.NewRequest("PUT", "", nil)
	c.Assert(err, jc.ErrorIsNil)
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = size
	url := "/" + id.Path() + "/archive?hash=" + hash
	if promulgated {
		pid := *id
		pid.User = ""
		url += "&promulgated=" + pid.String()
	}

	// Upload the charm.
	resp, err := s.client().DoWithBody(req, url, httpbakery.SeekerBody(body))
	c.Assert(err, jc.ErrorIsNil)
	defer resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)

	// Retrieve the uploaded charm id.
	var result params.ArchiveUploadResponse
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&result)
	c.Assert(err, jc.ErrorIsNil)
	if promulgated {
		return result.PromulgatedId
	}
	return result.Id
}
