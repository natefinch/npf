// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package csclient_test

import (
	"testing"

	jujutesting "github.com/juju/testing"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}
