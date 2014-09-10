// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package legacy_test

import (
	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/params"
	"gopkg.in/mgo.v2"
	gc "launchpad.net/gocheck"

	"net/http"

	"github.com/juju/charmstore/internal/legacy"
	"github.com/juju/charmstore/internal/storetesting"
)

var serverParams = charmstore.ServerParams{
	AuthUsername: "test-user",
	AuthPassword: "test-password",
}

type APISuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&APISuite{})

func (s *APISuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, serverParams)
}

func newServer(c *gc.C, session *mgo.Session, config charmstore.ServerParams) (http.Handler, *charmstore.Store) {
	db := session.DB("charmstore")
	store, err := charmstore.NewStore(db)
	c.Assert(err, gc.IsNil)
	srv, err := charmstore.NewServer(db, config, map[string]charmstore.NewAPIHandlerFunc{"": legacy.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	return srv, store
}

func (s *APISuite) TestCharmInfo(c *gc.C) {
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          "/charm-info",
		ExpectStatus: http.StatusInternalServerError,
		ExpectBody: params.Error{
			Message: "charm-info not implemented",
		},
	})
}
