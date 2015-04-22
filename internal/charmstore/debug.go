// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"net/http"

	"gopkg.in/juju/charmstore.v4/internal/router"
	appver "gopkg.in/juju/charmstore.v4/version"
)

// GET /debug/info .
func serveDebugInfo(http.Header, *http.Request) (interface{}, error) {
	return appver.VersionInfo, nil
}

func newServiceDebugHandler() http.Handler {
	mux := router.NewServeMux()
	mux.Handle("/info", router.HandleJSON(serveDebugInfo))
	return mux
}
