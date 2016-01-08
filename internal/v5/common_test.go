package v5_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v5"

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/juju/loggo"
	jujutesting "github.com/juju/testing"
	"github.com/julienschmidt/httprouter"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/bakerytest"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/mgo.v2"

	"gopkg.in/juju/charmstore.v5-unstable/internal/charmstore"
	"gopkg.in/juju/charmstore.v5-unstable/internal/router"
	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v5"
)

var mgoLogger = loggo.GetLogger("mgo")

func init() {
	mgo.SetLogger(mgoLog{})
}

type mgoLog struct{}

func (mgoLog) Output(calldepth int, s string) error {
	mgoLogger.LogCallf(calldepth+1, loggo.INFO, "%s", s)
	return nil
}

type commonSuite struct {
	jujutesting.IsolatedMgoSuite

	// srv holds the store HTTP handler.
	srv *charmstore.Server

	// srvParams holds the parameters that the
	// srv handler was started with
	srvParams charmstore.ServerParams

	// noMacaroonSrv holds the store HTTP handler
	// for an instance of the store without identity
	// enabled. If enableIdentity is false, this is
	// the same as srv.
	noMacaroonSrv *charmstore.Server

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

	dischargeTerms  func(cav, arg string) ([]checkers.Caveat, error)
	termsDischarger *bakerytest.Discharger
	enableTerms     bool

	// The following fields may be set before
	// SetUpSuite is invoked on commonSuite
	// and influences how the suite sets itself up.

	// enableIdentity holds whether the charmstore server
	// will be started with a configured identity service.
	enableIdentity bool

	// enableES holds whether the charmstore server will be
	// started with Elastic Search enabled.
	enableES bool

	// maxMgoSessions specifies the value that will be given
	// to config.MaxMgoSessions when calling charmstore.NewServer.
	maxMgoSessions int
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
	s.store.Pool().Close()
	s.store.Close()
	s.srv.Close()
	s.noMacaroonSrv.Close()
	if s.esSuite != nil {
		s.esSuite.TearDownTest(c)
	}
	if s.discharger != nil {
		s.discharger.Close()
		s.idMServer.Close()
	}
	if s.termsDischarger != nil {
		s.termsDischarger.Close()
	}
	s.IsolatedMgoSuite.TearDownTest(c)
}

// startServer creates a new charmstore server.
func (s *commonSuite) startServer(c *gc.C) {
	config := charmstore.ServerParams{
		AuthUsername:     testUsername,
		AuthPassword:     testPassword,
		StatsCacheMaxAge: time.Nanosecond,
		MaxMgoSessions:   s.maxMgoSessions,
	}
	keyring := bakery.NewPublicKeyRing()
	if s.enableIdentity {
		s.discharge = func(_, _ string) ([]checkers.Caveat, error) {
			return nil, errgo.New("no discharge")
		}
		discharger := bakerytest.NewDischarger(nil, func(_ *http.Request, cond string, arg string) ([]checkers.Caveat, error) {
			return s.discharge(cond, arg)
		})
		config.IdentityLocation = discharger.Location()
		config.IdentityAPIURL = s.idMServer.URL
		pk, err := httpbakery.PublicKeyForLocation(http.DefaultClient, discharger.Location())
		c.Assert(err, gc.IsNil)
		err = keyring.AddPublicKeyForLocation(discharger.Location(), true, pk)
		c.Assert(err, gc.IsNil)
	}
	if s.enableTerms {
		s.dischargeTerms = func(_, _ string) ([]checkers.Caveat, error) {
			return nil, errgo.New("no discharge")
		}
		termsDischarger := bakerytest.NewDischarger(nil, func(_ *http.Request, cond string, arg string) ([]checkers.Caveat, error) {
			return s.dischargeTerms(cond, arg)
		})
		config.TermsLocation = termsDischarger.Location()
		pk, err := httpbakery.PublicKeyForLocation(http.DefaultClient, termsDischarger.Location())
		c.Assert(err, gc.IsNil)
		err = keyring.AddPublicKeyForLocation(termsDischarger.Location(), true, pk)
		c.Assert(err, gc.IsNil)
	}
	config.PublicKeyLocator = keyring
	var si *charmstore.SearchIndex
	if s.enableES {
		si = &charmstore.SearchIndex{
			Database: s.esSuite.ES,
			Index:    s.esSuite.TestIndex,
		}
	}
	db := s.Session.DB("charmstore")
	var err error
	s.srv, err = charmstore.NewServer(db, si, config, map[string]charmstore.NewAPIHandlerFunc{"v5": v5.NewAPIHandler})
	c.Assert(err, gc.IsNil)
	s.srvParams = config

	if s.enableIdentity {
		config.IdentityLocation = ""
		config.PublicKeyLocator = nil
		config.IdentityAPIURL = ""
		s.noMacaroonSrv, err = charmstore.NewServer(db, si, config, map[string]charmstore.NewAPIHandlerFunc{"v5": v5.NewAPIHandler})
		c.Assert(err, gc.IsNil)
	} else {
		s.noMacaroonSrv = s.srv
	}
	s.noMacaroonSrvParams = config
	s.store = s.srv.Pool().Store()
}

func (s *commonSuite) addPublicCharm(c *gc.C, charmName string, rurl *router.ResolvedURL) (*router.ResolvedURL, charm.Charm) {
	ch := storetesting.Charms.CharmDir(charmName)
	err := s.store.AddCharmWithArchive(rurl, ch)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&rurl.URL, "read", params.Everyone, rurl.URL.User)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(rurl.URL.WithChannel(charm.DevelopmentChannel), "read", params.Everyone, rurl.URL.User)
	c.Assert(err, gc.IsNil)
	return rurl, ch
}

func (s *commonSuite) addPublicBundle(c *gc.C, bundleName string, rurl *router.ResolvedURL) (*router.ResolvedURL, charm.Bundle) {
	bundle := storetesting.Charms.BundleDir(bundleName)
	err := s.store.AddBundleWithArchive(rurl, bundle)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&rurl.URL, "read", params.Everyone, rurl.URL.User)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(rurl.URL.WithChannel(charm.DevelopmentChannel), "read", params.Everyone, rurl.URL.User)
	c.Assert(err, gc.IsNil)
	return rurl, bundle
}

// addCharms adds all the given charms to s.store. The
// map key is the id of the charm.
func (s *commonSuite) addCharms(c *gc.C, charms map[string]charm.Charm) {
	for id, ch := range charms {
		url := mustParseResolvedURL(id)
		// The blob related info are not used in these tests.
		// The related charms are retrieved from the entities collection,
		// without accessing the blob store.
		err := s.store.AddCharm(ch, charmstore.AddParams{
			URL:      url,
			BlobName: "blobName",
			BlobHash: fakeBlobHash,
			BlobSize: fakeBlobSize,
		})
		c.Assert(err, gc.IsNil, gc.Commentf("id %q", id))
		err = s.store.SetPerms(&url.URL, "read", params.Everyone, url.URL.User)
		c.Assert(err, gc.IsNil)
		if url.Development {
			err = s.store.SetPerms(url.UserOwnedURL(), "read", params.Everyone, url.URL.User)
		}
	}
}

// setPerms sets the read permissions of a set of entities.
// The map key is the is the id of each entity; its
// associated value is its read ACL.
func (s *commonSuite) setPerms(c *gc.C, readACLs map[string][]string) {
	for url, acl := range readACLs {
		err := s.store.SetPerms(charm.MustParseURL(url), "read", acl...)
		c.Assert(err, gc.IsNil)
	}
}

// handler returns a request handler that can be
// used to invoke private methods. The caller
// is responsible for calling Put on the returned handler.
func (s *commonSuite) handler(c *gc.C) *v5.ReqHandler {
	h := v5.New(s.store.Pool(), s.srvParams)
	defer h.Close()
	rh, err := h.NewReqHandler()
	c.Assert(err, gc.IsNil)
	// It would be nice if we could call s.AddCleanup here
	// to call rh.Put when the test has completed, but
	// unfortunately CleanupSuite.TearDownTest runs
	// after MgoSuite.TearDownTest, so that's not an option.
	return rh
}

func storeURL(path string) string {
	return "/v5/" + path
}

func bakeryDo(client *http.Client) func(*http.Request) (*http.Response, error) {
	if client == nil {
		client = httpbakery.NewHTTPClient()
	}
	bclient := httpbakery.NewClient()
	bclient.Client = client
	return func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			body := req.Body.(io.ReadSeeker)
			req.Body = nil
			return bclient.DoWithBody(req, body)
		}
		return bclient.Do(req)
	}
}

type idM struct {
	// groups may be set to determine the mapping
	// from user to groups for that user.
	groups map[string][]string

	// body may be set to cause serveGroups to return
	// an arbitrary HTTP response body.
	body string

	// contentType is the contentType to use when body is not ""
	contentType string

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
	idM.router.GET("/v1/u/:user/groups", idM.serveGroups)
	idM.router.GET("/v1/u/:user/idpgroups", idM.serveGroups)
	return idM
}

func (idM *idM) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	idM.router.ServeHTTP(w, req)
}

func (idM *idM) serveGroups(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	if idM.body != "" {
		if idM.contentType != "" {
			w.Header().Set("Content-Type", idM.contentType)
		}
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
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(idM.groups[u]); err != nil {
		panic(err)
	}
}
