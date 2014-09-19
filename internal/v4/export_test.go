// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"

	"gopkg.in/juju/charm.v3"
)

func BundleCharms(h http.Handler) func([]string) (map[string]charm.Charm, error) {
	return h.(*Handler).bundleCharms
}

var StartTime = &startTime
