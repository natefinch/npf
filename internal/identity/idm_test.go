// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package identity_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"

	gc "gopkg.in/check.v1"

	"gopkg.in/juju/charmstore.v5-unstable/internal/identity"
)

type discharge struct {
	id string
	c  chan error
}

// idM is a mock identity manager that can be used to test the client.
type idM struct {
	*httptest.Server
	*http.ServeMux
}

func newIdM(c *gc.C) *idM {
	i := &idM{
		ServeMux: http.NewServeMux(),
	}
	i.Server = httptest.NewServer(i)
	i.Handle("/", http.HandlerFunc(i.notFound))
	i.Handle("/test", http.HandlerFunc(i.serveTest))
	i.Handle("/v1/u/user1/groups", i.serveGroups("g1", "g2"))
	i.Handle("/v1/u/user2/groups", i.serveGroups())
	return i
}

func (i *idM) notFound(w http.ResponseWriter, req *http.Request) {
	i.error(w, http.StatusNotFound, "not found", "%s not found", req.URL.Path)
}

// serveTest serves a /test endpoint that can return a number of things
// depending on the query parameters:
//     ct = Content-Type to use (application/json)
//     s = Status code to use (200)
//     b = body content ({"method": method used})
func (i *idM) serveTest(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	if req.Form.Get("ct") != "" {
		w.Header().Set("Content-Type", req.Form.Get("ct"))
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	if req.Form.Get("s") != "" {
		s, err := strconv.Atoi(req.Form.Get("s"))
		if err != nil {
			i.error(w, http.StatusBadRequest, "ERROR", "cannot read status: %s", err)
			return
		}
		w.WriteHeader(s)
	}
	if req.Form.Get("b") != "" {
		w.Write([]byte(req.Form.Get("b")))
	} else {
		data := map[string]interface{}{
			"method": req.Method,
		}
		resp, err := json.Marshal(data)
		if err != nil {
			i.error(w, http.StatusInternalServerError, "ERROR", "cannot marshal response: %s", err)
			return
		}
		w.Write(resp)
	}
}

func (i *idM) write(w http.ResponseWriter, v interface{}) {
	body, err := json.Marshal(v)
	if err != nil {
		i.error(w, http.StatusInternalServerError, "ERROR", "cannot marshal response: %s", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (i *idM) error(w http.ResponseWriter, status int, code, format string, a ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(&identity.IdmError{
		Message: fmt.Sprintf(format, a...),
		Code:    code,
	})
	if err != nil {
		panic(err)
	}
	w.Write(body)
}

func (i *idM) serveGroups(groups ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		i.write(w, groups)
	}
}
