// Copyright 2012, 2013, 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package config_test

import (
	"io/ioutil"
	"path"
	"testing"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/config"
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

func (s *ConfigSuite) readConfig(c *gc.C, content string) (*config.Config, error) {
	// Write the configuration content to file.
	path := path.Join(c.MkDir(), "charmd.conf")
	err := ioutil.WriteFile(path, []byte(content), 0666)
	c.Assert(err, gc.IsNil)

	// Read the configuration.
	return config.Read(path)
}

func (s *ConfigSuite) TestRead(c *gc.C) {
	conf, err := s.readConfig(c, testConfig)
	c.Assert(err, gc.IsNil)
	c.Assert(conf, jc.DeepEquals, &config.Config{
		MongoURL:     "localhost:23456",
		APIAddr:      "blah:2324",
		AuthUsername: "myuser",
		AuthPassword: "mypasswd",
	})
}

func (s *ConfigSuite) TestReadConfigError(c *gc.C) {
	cfg, err := config.Read(path.Join(c.MkDir(), "charmd.conf"))
	c.Assert(err, gc.ErrorMatches, ".* no such file or directory")
	c.Assert(cfg, gc.IsNil)
}

func (s *ConfigSuite) TestValidateConfigError(c *gc.C) {
	cfg, err := s.readConfig(c, "")
	c.Assert(err, gc.ErrorMatches, "missing fields mongo-url, api-addr, auth-username, auth-password in config file")
	c.Assert(cfg, gc.IsNil)
}
