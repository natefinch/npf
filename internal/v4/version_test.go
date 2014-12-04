// Copyright 2012, 2013, 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"io/ioutil"
	"path"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/internal/v4"
)

type VersionSuite struct {
	jujutesting.IsolationSuite
}

const testVersion = `
version: myTestVersion
id: myFakeSHA1NotFromGit
`

var _ = gc.Suite(&VersionSuite{})

func (s *VersionSuite) readVersion(c *gc.C, content string) (*v4.VersionInfo, error) {
	// Write the configuration content to file.
	path := path.Join(c.MkDir(), "version.yaml")
	err := ioutil.WriteFile(path, []byte(content), 0666)
	c.Assert(err, gc.IsNil)

	// Read the configuration.
	return v4.ReadVersion(path)
}

func (s *VersionSuite) TestRead(c *gc.C) {
	conf, err := s.readVersion(c, testVersion)
	c.Assert(err, gc.IsNil)
	c.Assert(conf, jc.DeepEquals, &v4.VersionInfo{
		Version: "myTestVersion",
		Id:      "myFakeSHA1NotFromGit",
	})
}

func (s *VersionSuite) TestReadVersionError(c *gc.C) {
	version, err := v4.ReadVersion(path.Join(c.MkDir(), "version.yaml"))
	c.Assert(err, gc.ErrorMatches, ".* no such file or directory")
	c.Assert(version, gc.IsNil)
}

func (s *VersionSuite) TestValidateConfigError(c *gc.C) {
	version, err := s.readVersion(c, "")
	c.Assert(err, gc.ErrorMatches, "missing fields version, id in version file")
	c.Assert(version, gc.IsNil)
}
