// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"github.com/juju/charmstore/version"
	"net/http"
)

// GET /debug/info .
func (h *Handler) serveDebugInfo(_ http.Header, req *http.Request) (interface{}, error) {
	return version.VersionInfo, nil
}
