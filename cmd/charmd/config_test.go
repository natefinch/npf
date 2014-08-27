// Copyright 2012, 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"io/ioutil"
	"path"
	"testing"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "launchpad.net/gocheck"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type ConfigSuite struct {
	jujutesting.IsolationSuite
}

var _ = gc.Suite(&ConfigSuite{})

const testConfig = `
mongo-url: localhost:23456
api-addr: blah:2324
foo: 1
bar: false
auth-username: myuser
auth-password: mypasswd
`

func (s *ConfigSuite) makeConfig(c *gc.C, content string) *config {
	// Write the configuration content to file.
	path := path.Join(c.MkDir(), "charmd.conf")
	err := ioutil.WriteFile(path, []byte(content), 0666)
	c.Assert(err, gc.IsNil)

	// Read the configuration.
	conf, err := readConfig(path)
	c.Assert(err, gc.IsNil)
	return conf
}

func (s *ConfigSuite) TestReadConfig(c *gc.C) {
	conf := s.makeConfig(c, testConfig)
	c.Assert(conf, jc.DeepEquals, &config{
		MongoURL:     "localhost:23456",
		APIAddr:      "blah:2324",
		AuthUsername: "myuser",
		AuthPassword: "mypasswd",
	})
}

func (s *ConfigSuite) TestReadConfigError(c *gc.C) {
	_, err := readConfig(path.Join(c.MkDir(), "charmd.conf"))
	c.Assert(err, gc.ErrorMatches, ".* no such file or directory")
}

func (s *ConfigSuite) TestValidateConfig(c *gc.C) {
	conf := s.makeConfig(c, testConfig)
	err := conf.validate()
	c.Assert(err, gc.IsNil)
}

func (s *ConfigSuite) TestValidateConfigError(c *gc.C) {
	conf := s.makeConfig(c, "")
	err := conf.validate()
	c.Assert(err, gc.ErrorMatches, "missing mongo-url, api-addr, auth-username, auth-password in config file")
}
