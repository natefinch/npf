// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package charmstore

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v4/internal/router"
	appver "gopkg.in/juju/charmstore.v4/version"
)

// GET /debug/info .
func serveDebugInfo(http.Header, *http.Request) (interface{}, error) {
	return appver.VersionInfo, nil
}

// GET /debug/check.
func debugCheck(checks map[string]func() error) http.Handler {
	return router.HandleJSON(func(http.Header, *http.Request) (interface{}, error) {
		n := len(checks)
		type result struct {
			name string
			err  error
		}
		c := make(chan result)
		for name, check := range checks {
			name, check := name, check
			go func() {
				c <- result{name: name, err: check()}
			}()
		}
		results := make(map[string]string, n)
		var failed bool
		for ; n > 0; n-- {
			res := <-c
			if res.err == nil {
				results[res.name] = "OK"
			} else {
				failed = true
				results[res.name] = res.err.Error()
			}
		}
		if failed {
			keys := make([]string, 0, len(results))
			for k := range results {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			msgs := make([]string, len(results))
			for i, k := range keys {
				msgs[i] = fmt.Sprintf("[%s: %s]", k, results[k])
			}
			return nil, errgo.Newf("check failure: %s", strings.Join(msgs, " "))
		}
		return results, nil
	})
}

func checkDB(db *mgo.Database) func() error {
	return func() error {
		s := db.Session.Copy()
		s.SetSyncTimeout(500 * time.Millisecond)
		defer s.Close()
		return s.Ping()
	}
}

func checkES(si *SearchIndex) func() error {
	if si == nil || si.Database == nil {
		return func() error {
			return nil
		}
	}
	return func() error {
		_, err := si.Health()
		return err
	}
}

func newServiceDebugHandler(db *mgo.Database, si *SearchIndex) http.Handler {
	mux := router.NewServeMux()
	mux.Handle("/info", router.HandleJSON(serveDebugInfo))
	mux.Handle("/check", debugCheck(map[string]func() error{
		"mongodb":       checkDB(db),
		"elasticsearch": checkES(si),
	}))
	return mux
}
