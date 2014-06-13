// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"io/ioutil"
	"path/filepath"

	"github.com/juju/charm"
	charmtesting "github.com/juju/charm/testing"
	"github.com/juju/cmd/cmdtesting"
	gitjujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"

	"github.com/juju/charmstore"
)

type deleteCharmSuite struct {
	gitjujutesting.IsolationSuite
}

var _ = gc.Suite(&deleteCharmSuite{})

func (s *deleteCharmSuite) createConfigFile(c *gc.C) string {
	configPath := filepath.Join(c.MkDir(), "charmd.conf")
	// Derive config file from test mongo port.
	contents := "mongo-url: " + gitjujutesting.MgoServer.Addr() + "\n"
	err := ioutil.WriteFile(configPath, []byte(contents), 0666)
	c.Assert(err, gc.IsNil)
	return configPath
}

func (s *deleteCharmSuite) TestInit(c *gc.C) {
	config := &DeleteCharmCommand{}
	err := cmdtesting.InitCommand(config, []string{"--config", "/etc/charmd.conf", "--url", "cs:go"})
	c.Assert(err, gc.IsNil)
	c.Assert(config.ConfigPath, gc.Equals, "/etc/charmd.conf")
	c.Assert(config.Url, gc.Equals, "cs:go")
}

func (s *deleteCharmSuite) TestRunNotFound(c *gc.C) {
	configPath := s.createConfigFile(c)

	// Deleting charm that does not exist returns a not found error.
	config := &DeleteCharmCommand{}
	_, err := cmdtesting.RunCommand(c, config, "--config", configPath, "--url", "cs:unreleased/foo")
	c.Assert(err, gc.Equals, charmstore.ErrNotFound)
}

func (s *deleteCharmSuite) TestRunFound(c *gc.C) {
	configPath := s.createConfigFile(c)

	// Publish that charm.
	url := charm.MustParseURL("cs:unreleased/foo")
	store, err := charmstore.Open(gitjujutesting.MgoServer.Addr())
	c.Assert(err, gc.IsNil)
	defer store.Close()
	pub, err := store.CharmPublisher([]*charm.URL{url}, "such-digest-much-unique")
	c.Assert(err, gc.IsNil)
	err = pub.Publish(charmtesting.Charms.ClonedDir(c.MkDir(), "dummy"))
	c.Assert(err, gc.IsNil)

	// The charm is successfully deleted.
	config := &DeleteCharmCommand{}
	_, err = cmdtesting.RunCommand(c, config, "--config", configPath, "--url", "cs:unreleased/foo")
	c.Assert(err, gc.IsNil)
	c.Assert(config.Config, gc.NotNil)

	// Confirm that the charm is gone
	_, err = store.CharmInfo(url)
	c.Assert(err, gc.Equals, charmstore.ErrNotFound)
}
