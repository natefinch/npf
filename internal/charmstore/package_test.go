// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package charmstore_test

import (
	"testing"

	"github.com/juju/charmstore/internal/storetesting"
	jujutesting "github.com/juju/testing"
)

func TestPackage(t *testing.T) {
	storetesting.ElasticSearchTestPackage(t, func(t2 *testing.T) {
		jujutesting.MgoTestPackage(t2, nil)
	})
}
