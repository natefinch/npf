// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package agent_test

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"

	"gopkg.in/juju/charmstore.v5-unstable/internal/agent"
)

type discharge struct {
	id string
	c  chan error
}

// idM provides a mock identity server that can be used to test agent login.
// the following endpoints are provided:
//     /public-key
//     /discharge
//     /protected
//     /login
//     /agent
//     /wait
// Most tests will intiate with a call to /protected.
type idM struct {
	*httptest.Server
	*http.ServeMux
	svc        *bakery.Service
	discharges map[string]discharge
	key        *bakery.KeyPair
}

func newIdM(c *gc.C) *idM {
	i := &idM{
		ServeMux:   http.NewServeMux(),
		discharges: make(map[string]discharge),
	}
	i.Server = httptest.NewServer(i)
	var err error
	i.key, err = bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	i.svc, err = bakery.NewService(bakery.NewServiceParams{
		Key: i.key,
		Locator: bakery.PublicKeyLocatorMap{
			i.URL: &i.key.Public,
		},
	})
	c.Assert(err, gc.IsNil)
	httpbakery.AddDischargeHandler(i.ServeMux, "/", i.svc, i.checker)
	i.Handle("/", http.HandlerFunc(i.notFound))
	i.Handle("/protected", http.HandlerFunc(i.serveProtected))
	i.Handle("/login", http.HandlerFunc(i.serveLogin))
	i.Handle("/wait", http.HandlerFunc(i.serveWait))
	i.Handle("/agent", http.HandlerFunc(i.serveAgent))
	return i
}

func (i *idM) notFound(w http.ResponseWriter, req *http.Request) {
	i.error(w, http.StatusNotFound, "not found", "%s not found", req.URL.Path)
}

func (i *idM) write(w http.ResponseWriter, v interface{}) {
	body, err := json.Marshal(v)
	if err != nil {
		i.error(w, http.StatusInternalServerError, "cannot marshal response: %s", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

func (i *idM) error(w http.ResponseWriter, status int, format string, a ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, err := json.Marshal(&agent.Error{
		Message: fmt.Sprintf(format, a...),
	})
	if err != nil {
		panic(err)
	}
	w.Write(body)
}

// serveProtected provides the /protected endpoint. When /protected is
// called two parameters should be provided:
//     test = test id this id uniquely identifies the test
//     cav = the caveat to put in the third party caveat.
//
// The cav parameter determines what will happen in the test and can be one of
//     allow = the macaroon is discharged straight away
//     agent = successful agent authentication
//     agent-fail = unsuccessful agent authentication
//     interactive = login does not return a JSON object
//     no-agent = login does return a JSON object, but agent authentication is not specified.
func (i *idM) serveProtected(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("test") == "" {
		i.error(w, http.StatusBadRequest, "test id not specified")
		return
	}
	attrs, err := httpbakery.CheckRequest(i.svc, r, nil, checkers.OperationChecker(r.Form.Get("test")))
	if err == nil {
		i.write(w, attrs)
		return
	}
	verr, ok := err.(*bakery.VerificationError)
	if !ok {
		i.error(w, http.StatusInternalServerError, "error checking macaroon: %s", err)
		return
	}
	m, err := i.svc.NewMacaroon("", nil, []checkers.Caveat{
		{
			Location:  i.URL,
			Condition: r.Form.Get("c") + " " + r.Form.Get("test"),
		},
		checkers.AllowCaveat(r.Form.Get("test")),
	})
	if err != nil {
		i.error(w, http.StatusInternalServerError, "cannot create macaroon: %s", err)
		return
	}
	httpbakery.WriteDischargeRequiredError(w, m, "/", verr)
}

// serveLogin provides the /login endpoint. When /login is called it should
// be provided with a test id. /login also supports some additional parameters:
//     a = if set to "true" an agent URL will be added to the json response.
//     i = if set to "true" a plaintext response will be sent to simulate interaction.
func (i *idM) serveLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("i") == "true" || r.Header.Get("Accept") != "application/json" {
		w.Write([]byte("Let's interact!"))
		return
	}
	var lm agent.LoginMethods
	if r.Form.Get("a") == "true" {
		lm.Agent = i.URL + "/agent?test=" + r.Form.Get("test") + "&f=" + r.Form.Get("f")
	}
	i.write(w, lm)
}

// serveWait provides the /wait endpoint. When /wait is called it should
// be provided with a test id. This then matches the wait to the login
// being tested.
func (i *idM) serveWait(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("test") == "" {
		i.error(w, http.StatusBadRequest, "test id not specified")
		return
	}
	d := i.discharges[r.Form.Get("test")]
	derr := <-d.c
	if derr != nil {
		// do something with the error
		return
	}
	m, err := i.svc.Discharge(
		bakery.ThirdPartyCheckerFunc(
			func(cavId, cav string) ([]checkers.Caveat, error) {
				return nil, nil
			},
		),
		d.id,
	)
	if err != nil {
		i.error(w, http.StatusInternalServerError, "cannot discharge caveat: %s", err)
		return
	}
	i.write(w, httpbakery.WaitResponse{
		Macaroon: m,
	})
}

// serveAgent provides the /agent endpoint. When /agent is called it
// should be provided with a test id. This then matches the current login
// to the correct wait. If the optional f query variable is set to "true"
// then a failure will be simulated.
func (i *idM) serveAgent(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("f") == "true" {
		i.error(w, http.StatusTeapot, "forced failure")
		return
	}
	test := r.Form.Get("test")
	op := "agent-login-" + test
	_, err := httpbakery.CheckRequest(i.svc, r, nil, checkers.OperationChecker(op))
	if err == nil {
		d := i.discharges[test]
		d.c <- nil
		return
	}
	verr, ok := err.(*bakery.VerificationError)
	if !ok {
		d := i.discharges[test]
		d.c <- err
		i.error(w, http.StatusInternalServerError, "cannot check request: %s", err)
		return
	}
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		d := i.discharges[test]
		d.c <- err
		i.error(w, http.StatusInternalServerError, "cannot read agent login request: %s", err)
		return
	}
	var login agent.AgentLoginRequest
	err = json.Unmarshal(body, &login)
	if err != nil {
		d := i.discharges[test]
		d.c <- err
		i.error(w, http.StatusBadRequest, "cannot unmarshal login request: %s", err)
		return
	}
	m, err := i.svc.NewMacaroon("", nil, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(login.PublicKey),
		checkers.AllowCaveat(op),
	})
	if err != nil {
		d := i.discharges[test]
		d.c <- err
		i.error(w, http.StatusInternalServerError, "cannot create macaroon: %s", err)
		return
	}
	httpbakery.WriteDischargeRequiredError(w, m, "/", verr)
}

func (i *idM) checker(r *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
	cond, arg, err := checkers.ParseCaveat(cav)
	if err != nil {
		return nil, err
	}
	switch cond {
	case "allow":
		return nil, nil
	case "agent":
		i.discharges[arg] = discharge{
			id: cavId,
			c:  make(chan error, 1),
		}
		return nil, &httpbakery.Error{
			Message: "need login",
			Code:    httpbakery.ErrInteractionRequired,
			Info: &httpbakery.ErrorInfo{
				VisitURL: i.URL + "/login?a=true&test=" + arg,
				WaitURL:  i.URL + "/wait?test=" + arg,
			},
		}
	case "interactive":
		i.discharges[arg] = discharge{
			id: cavId,
			c:  make(chan error, 1),
		}
		return nil, &httpbakery.Error{
			Message: "need login",
			Code:    httpbakery.ErrInteractionRequired,
			Info: &httpbakery.ErrorInfo{
				VisitURL: i.URL + "/login?i=true&test=" + arg,
				WaitURL:  i.URL + "/wait?test=" + arg,
			},
		}
	case "no-agent":
		i.discharges[arg] = discharge{
			id: cavId,
			c:  make(chan error, 1),
		}
		return nil, &httpbakery.Error{
			Message: "need login",
			Code:    httpbakery.ErrInteractionRequired,
			Info: &httpbakery.ErrorInfo{
				VisitURL: i.URL + "/login?test=" + arg,
				WaitURL:  i.URL + "/wait?test=" + arg,
			},
		}
	case "agent-fail":
		i.discharges[arg] = discharge{
			id: cavId,
			c:  make(chan error, 1),
		}
		return nil, &httpbakery.Error{
			Message: "need login",
			Code:    httpbakery.ErrInteractionRequired,
			Info: &httpbakery.ErrorInfo{
				VisitURL: i.URL + "/login?a=true&f=true&test=" + arg,
				WaitURL:  i.URL + "/wait?test=" + arg,
			},
		}
	default:
		return nil, checkers.ErrCaveatNotRecognized
	}
}
