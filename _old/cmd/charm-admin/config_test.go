// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"io/ioutil"
	"path/filepath"

	"github.com/juju/cmd"
	"github.com/juju/cmd/cmdtesting"
	gitjujutesting "github.com/juju/testing"
	gc "launchpad.net/gocheck"
)

type configSuite struct {
	gitjujutesting.IsolationSuite
}

var _ = gc.Suite(&configSuite{})

const testConfig = `
mongo-url: localhost:23456
foo: 1
bar: false
`

type someConfigCommand struct {
	ConfigCommand
}

func (c *someConfigCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "some-cmd",
		Purpose: "something in particular that requires configuration",
	}
}

func (c *someConfigCommand) Run(ctx *cmd.Context) error {
	return c.ReadConfig(ctx)
}

func (s *configSuite) TestReadConfig(c *gc.C) {
	configPath := filepath.Join(c.MkDir(), "charmd.conf")
	err := ioutil.WriteFile(configPath, []byte(testConfig), 0666)
	c.Assert(err, gc.IsNil)

	config := &someConfigCommand{}
	args := []string{"--config", configPath}
	err = cmdtesting.InitCommand(config, args)
	c.Assert(err, gc.IsNil)
	_, err = cmdtesting.RunCommand(c, config, args...)
	c.Assert(err, gc.IsNil)

	c.Assert(config.Config, gc.NotNil)
	c.Assert(config.Config.MongoURL, gc.Equals, "localhost:23456")
}
