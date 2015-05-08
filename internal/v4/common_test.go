package v4_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"

	gc "gopkg.in/check.v1"

	"github.com/julienschmidt/httprouter"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"

	"gopkg.in/juju/charmstore.v4/internal/charmstore"
	"gopkg.in/juju/charmstore.v4/internal/storetesting"
	"gopkg.in/juju/charmstore.v4/internal/v4"
)

type commonSuite struct {
	storetesting.IsolatedMgoSuite

	// srv holds the store HTTP handler.
	srv http.Handler

	// srvParams holds the parameters that the
	// srv handler was started with
	srvParams charmstore.ServerParams

	// noMacaroonSrv holds the store HTTP handler
	// for an instance of the store without identity
	// enabled. If enableIdentity is false, this is
	// the same as srv.
	noMacaroonSrv http.Handler

	// noMacaroonSrvParams holds the parameters that the
	// noMacaroonSrv handler was started with
	noMacaroonSrvParams charmstore.ServerParams

	// store holds an instance of *charm.Store
	// that can be used to access the charmstore database
	// directly.
	store *charmstore.Store

	// esSuite is set only when enableES is set to true.
	esSuite *storetesting.ElasticSearchSuite

	// discharge holds the function that will be used
	// to check third party caveats by the mock
	// discharger. This will be ignored if enableIdentity was
	// not true before commonSuite.SetUpTest is invoked.
	//
	// It may be set by tests to influence the behavior of the
	// discharger.
	discharge func(cav, arg string) ([]checkers.Caveat, error)

	discharger *bakerytest.Discharger
	idM        *idM
	idMServer  *httptest.Server

	// The following fields may be set before
	// SetUpSuite is invoked on commonSuite
	// and influences how the suite sets itself up.

	// enableIdentity holds whether the charmstore server
	// will be started with a configured identity service.
	enableIdentity bool

	// enableES holds whether the charmstore server will be
	// started with Elastic Search enabled.
	enableES bool
}

func (s *commonSuite) SetUpSuite(c *gc.C) {
	s.IsolatedMgoSuite.SetUpSuite(c)
	if s.enableES {
		s.esSuite = new(storetesting.ElasticSearchSuite)
		s.esSuite.SetUpSuite(c)
	}
}

func (s *commonSuite) TearDownSuite(c *gc.C) {
	if s.esSuite != nil {
		s.esSuite.TearDownSuite(c)
	}
}

func (s *commonSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	if s.esSuite != nil {
		s.esSuite.SetUpTest(c)
	}
	if s.enableIdentity {
		s.idM = newIdM()
		s.idMServer = httptest.NewServer(s.idM)
	}
	s.startServer(c)
}

func (s *commonSuite) TearDownTest(c *gc.C) {
	s.store.Close()
	if s.esSuite != nil {
		s.esSuite.TearDownTest(c)
	}
	if s.discharger != nil {
		s.discharger.Close()
		s.idMServer.Close()
	}
	s.IsolatedMgoSuite.TearDownTest(c)
}

// startServer creates a new charmstore server.
func (s *commonSuite) startServer(c *gc.C) {
	config := charmstore.ServerParams{
		AuthUsername: testUsername,
		AuthPassword: testPassword,
	}
	if s.enableIdentity {
		s.discharge = func(_, _ string) ([]checkers.Caveat, error) {
			return nil, errgo.New("no discharge")
		}
		discharger := bakerytest.NewDischarger(nil, func(_ *http.Request, cond string, arg string) ([]checkers.Caveat, error) {
			return s.discharge(cond, arg)
		})
		config.IdentityLocation = discharger.Location()
		config.PublicKeyLocator = discharger
		config.IdentityAPIURL = s.idMServer.URL
	}
	var si *charmstore.SearchIndex
	if s.enableES {
		si = &charmstore.SearchIndex{
			Database: s.esSuite.ES,
			Index:    s.esSuite.TestIndex,
		}
	}
	db := s.Session.DB("charmstore")
	var err error
	s.srv, err = charmstore.NewServer(db, si, config, map[string]charmstore.NewAPIHandlerFunc{"v4": v4.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	s.srvParams = config

	if s.enableIdentity {
		config.IdentityLocation = ""
		config.PublicKeyLocator = nil
		config.IdentityAPIURL = ""
		s.noMacaroonSrv, err = charmstore.NewServer(db, si, config, map[string]charmstore.NewAPIHandlerFunc{"v4": v4.NewAPIHandler})
		c.Assert(err, gc.IsNil)
	} else {
		s.noMacaroonSrv = s.srv
	}
	s.noMacaroonSrvParams = config

	pool, err := charmstore.NewPool(db, si, &bakery.NewServiceParams{})
	c.Assert(err, gc.IsNil)
	s.store = pool.Store()
}

func storeURL(path string) string {
	return "/v4/" + path
}

func bakeryDo(client *http.Client) func(*http.Request) (*http.Response, error) {
	if client == nil {
		client = httpbakery.NewHTTPClient()
	}
	return func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			return httpbakery.DoWithBody(client, req, httpbakery.SeekerBody(req.Body.(io.ReadSeeker)), noInteraction)
		}
		return httpbakery.Do(client, req, noInteraction)
	}
}

type idM struct {
	// groups may be set to determine the mapping
	// from user to groups for that user.
	groups map[string][]string

	// body may be set to cause serveGroups to return
	// an arbitrary HTTP response body.
	body string

	// status may be set to indicate the HTTP status code
	// when body is not nil.
	status int

	router *httprouter.Router
}

func newIdM() *idM {
	idM := &idM{
		groups: make(map[string][]string),
		router: httprouter.New(),
	}
	idM.router.GET("/v1/u/:user/idpgroups", idM.serveGroups)
	return idM
}

func (idM *idM) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	idM.router.ServeHTTP(w, req)
}

func (idM *idM) serveGroups(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	if idM.body != "" {
		if idM.status != 0 {
			w.WriteHeader(idM.status)
		}
		w.Write([]byte(idM.body))
		return
	}
	u := p.ByName("user")
	if u == "" {
		panic("no user")
	}
	enc := json.NewEncoder(w)
	if err := enc.Encode(idM.groups[u]); err != nil {
		panic(err)
	}
}
