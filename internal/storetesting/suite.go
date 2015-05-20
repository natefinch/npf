// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package storetesting	// import "gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"

import (
	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
)

type IsolatedMgoSuite struct {
	jujutesting.IsolationSuite
	jujutesting.MgoSuite
}

func (s *IsolatedMgoSuite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	s.MgoSuite.SetUpSuite(c)
}

func (s *IsolatedMgoSuite) TearDownSuite(c *gc.C) {
	s.MgoSuite.TearDownSuite(c)
	s.IsolationSuite.TearDownSuite(c)
}

func (s *IsolatedMgoSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.MgoSuite.SetUpTest(c)
}

func (s *IsolatedMgoSuite) TearDownTest(c *gc.C) {
	s.MgoSuite.TearDownTest(c)
	s.IsolationSuite.TearDownTest(c)
}

type IsolatedMgoESSuite struct {
	jujutesting.IsolationSuite
	jujutesting.MgoSuite
	ElasticSearchSuite
}

func (s *IsolatedMgoESSuite) SetUpSuite(c *gc.C) {
	s.IsolationSuite.SetUpSuite(c)
	s.ElasticSearchSuite.SetUpSuite(c)
	s.MgoSuite.SetUpSuite(c)
}

func (s *IsolatedMgoESSuite) TearDownSuite(c *gc.C) {
	s.MgoSuite.TearDownSuite(c)
	s.ElasticSearchSuite.TearDownSuite(c)
	s.IsolationSuite.TearDownSuite(c)
}

func (s *IsolatedMgoESSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.ElasticSearchSuite.SetUpTest(c)
	s.MgoSuite.SetUpTest(c)
}

func (s *IsolatedMgoESSuite) TearDownTest(c *gc.C) {
	s.MgoSuite.TearDownTest(c)
	s.ElasticSearchSuite.TearDownTest(c)
	s.IsolationSuite.TearDownTest(c)
}
