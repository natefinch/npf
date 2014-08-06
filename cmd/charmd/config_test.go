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
`

func (s *ConfigSuite) TestReadConfig(c *gc.C) {
	path := path.Join(c.MkDir(), "charmd.conf")
	err := ioutil.WriteFile(path, []byte(testConfig), 0666)
	c.Assert(err, gc.IsNil)

	conf, err := readConfig(path)
	c.Assert(err, gc.IsNil)
	c.Assert(conf, jc.DeepEquals, &config{
		MongoURL: "localhost:23456",
		APIAddr:  "blah:2324",
	})
}
