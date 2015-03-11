// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"net/http"

	"gopkg.in/juju/charmstore.v4/version"
)

// GET /debug/info .
func (h *Handler) serveDebugInfo(_ http.Header, req *http.Request) (interface{}, error) {
	return version.VersionInfo, nil
}
