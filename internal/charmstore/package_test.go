// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"testing"

	jujutesting "github.com/juju/testing"
)

func TestPackage(t *testing.T) {
	jujutesting.MgoTestPackage(t, nil)
}
