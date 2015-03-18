// Copyright 2015 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The csclient package provides access to the charm store API.
package csclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"unicode"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v5-unstable"
	"gopkg.in/macaroon-bakery.v0/httpbakery"

	"gopkg.in/juju/charmstore.v4/params"
)

const apiVersion = "v4"

// ServerURL holds the default location of the global charm store.
// An alternate location can be configured by changing the URL field in the
// Params struct.
// For live testing or QAing the application, a different charm store
// location should be used, for instance "https://api.staging.jujucharms.com".
var ServerURL = "https://api.jujucharms.com/charmstore"

// Client represents the client side of a charm store.
type Client struct {
	params        Params
	statsDisabled bool
}

// Params holds parameters for creating a new charm store client.
type Params struct {
	// URL holds the root endpoint URL of the charmstore,
	// with no trailing slash, not including the version.
	// For example https://api.jujucharms.com/charmstore
	// If empty, the default charm store client location is used.
	URL string

	// User and Password hold the authentication credentials
	// for the client. If User is empty, no credentials will be
	// sent.
	User     string
	Password string

	// HTTPClient holds the HTTP client to use when making
	// requests to the store. If nil, httpbakery.NewHTTPClient will
	// be used.
	HTTPClient *http.Client

	// VisitWebPage is called when authorization requires that
	// the user visits a web page to authenticate themselves.
	// If nil, a default function that returns an error will be used.
	VisitWebPage func(url *url.URL) error
}

// New returns a new charm store client.
func New(p Params) *Client {
	if p.URL == "" {
		p.URL = ServerURL
	}
	if p.VisitWebPage == nil {
		p.VisitWebPage = noVisit
	}
	if p.HTTPClient == nil {
		p.HTTPClient = httpbakery.NewHTTPClient()
	}
	return &Client{
		params: p,
	}
}

func noVisit(url *url.URL) error {
	return errgo.New("interaction required but no web browser configured")
}

// ServerURL returns the charm store URL used by the client.
func (c *Client) ServerURL() string {
	return c.params.URL
}

// DisableStats disables incrementing download stats when retrieving archives
// from the charm store.
func (c *Client) DisableStats() {
	c.statsDisabled = true
}

// GetArchive retrieves the archive for the given charm or bundle, returning a
// reader its data can be read from, the fully qualified id of the
// corresponding entity, the SHA384 hash of the data and its size.
func (c *Client) GetArchive(id *charm.Reference) (r io.ReadCloser, eid *charm.Reference, hash string, size int64, err error) {
	// Create the request.
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return nil, nil, "", 0, errgo.Notef(err, "cannot make new request")
	}

	// Send the request.
	v := url.Values{}
	if c.statsDisabled {
		v.Set("stats", "0")
	}
	u := url.URL{
		Path:     "/" + id.Path() + "/archive",
		RawQuery: v.Encode(),
	}
	resp, err := c.Do(req, u.String())
	if err != nil {
		return nil, nil, "", 0, errgo.Notef(err, "cannot get archive")
	}

	// Validate the response headers.
	entityId := resp.Header.Get(params.EntityIdHeader)
	if entityId == "" {
		resp.Body.Close()
		return nil, nil, "", 0, errgo.Newf("no %s header found in response", params.EntityIdHeader)
	}
	eid, err = charm.ParseReference(entityId)
	if err != nil {
		// The server did not return a valid id.
		resp.Body.Close()
		return nil, nil, "", 0, errgo.Notef(err, "invalid entity id found in response")
	}
	if eid.Series == "" || eid.Revision == -1 {
		// The server did not return a fully qualified entity id.
		resp.Body.Close()
		return nil, nil, "", 0, errgo.Newf("archive get returned not fully qualified entity id %q", eid)
	}
	hash = resp.Header.Get(params.ContentHashHeader)
	if hash == "" {
		resp.Body.Close()
		return nil, nil, "", 0, errgo.Newf("no %s header found in response", params.ContentHashHeader)
	}

	// Validate the response contents.
	if resp.ContentLength < 0 {
		// TODO frankban: handle the case the contents are chunked.
		resp.Body.Close()
		return nil, nil, "", 0, errgo.Newf("no content length found in response")
	}
	return resp.Body, eid, hash, resp.ContentLength, nil
}

// UploadCharm uploads the given charm to the charm store with the given id.
// The id should include the series and should not include the revision. The
// accepted charm implementations are charm.CharmDir and charm.CharmArchive.
func (c *Client) UploadCharm(id *charm.Reference, ch charm.Charm) (*charm.Reference, error) {
	r, hash, size, err := openArchive(ch)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open charm archive")
	}
	defer r.Close()
	return c.uploadArchive(id, r, hash, size)
}

// UploadBundle uploads the given bundle to the charm store with the given id.
// The id should include the "bundle" series and should not include the
// revision. The accepted bundle implementations are charm.BundleDir and
// charm.BundleArchive.
func (c *Client) UploadBundle(id *charm.Reference, b charm.Bundle) (*charm.Reference, error) {
	r, hash, size, err := openArchive(b)
	if err != nil {
		return nil, errgo.Notef(err, "cannot open bundle archive")
	}
	defer r.Close()
	return c.uploadArchive(id, r, hash, size)
}

// uploadArchive pushes the archive for the charm or bundle represented by
// the given body, its SHA384 hash and its size. It returns the resulting
// entity reference. The given id should include the series and should not
// include the revision.
func (c *Client) uploadArchive(id *charm.Reference, body io.ReadSeeker, hash string, size int64) (*charm.Reference, error) {
	// Validate the entity id.
	if id.Series == "" {
		return nil, errgo.Newf("no series specified in %q", id)
	}
	if id.Revision != -1 {
		return nil, errgo.Newf("revision specified in %q, but should not be specified", id)
	}

	// Prepare the request.
	req, err := http.NewRequest("POST", "", nil)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make new request")
	}
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = size

	// Send the request.
	resp, err := c.DoWithBody(req, "/"+id.Path()+"/archive?hash="+hash, httpbakery.SeekerBody(body))
	if err != nil {
		return nil, errgo.Notef(err, "cannot post archive")
	}
	defer resp.Body.Close()

	// Parse the response.
	var result params.ArchiveUploadResponse
	if err := parseResponseBody(resp.Body, &result); err != nil {
		return nil, errgo.Mask(err)
	}
	return result.Id, nil
}

// PutExtraInfo puts extra-info data for the given id.
// Each entry in the info map causes a value in extra-info with
// that key to be set to the associated value.
// Entries not set in the map will be unchanged.
func (c *Client) PutExtraInfo(id *charm.Reference, info map[string]interface{}) error {
	req, _ := http.NewRequest("PUT", "", nil)
	req.Header.Set("Content-Type", "application/json")
	data, err := json.Marshal(info)
	if err != nil {
		return errgo.Notef(err, "cannot marshal extra-info")
	}
	body := bytes.NewReader(data)
	resp, err := c.DoWithBody(req, "/"+id.Path()+"/meta/extra-info", httpbakery.SeekerBody(body))
	if err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	resp.Body.Close()
	return nil
}

// Meta fetches metadata on the charm or bundle with the
// given id. The result value provides a value
// to be filled in with the result, which must be
// a pointer to a struct containing members corresponding
// to possible metadata include parameters
// (see https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmeta).
//
// It returns the fully qualified id of the entity.
//
// The name of the struct member is translated to
// a lower case hyphen-separated form; for example,
// ArchiveSize becomes "archive-size", and BundleMachineCount
// becomes "bundle-machine-count", but may also
// be specified in the field's tag
//
// This example will fill in the result structure with information
// about the given id, including information on its archive
// size (include archive-size), upload time (include archive-upload-time)
// and digest (include extra-info/digest).
//
//	var result struct {
//		ArchiveSize params.ArchiveSizeResponse
//		ArchiveUploadTime params.ArchiveUploadTimeResponse
//		Digest string `csclient:"extra-info/digest"`
//	}
//	id, err := client.Meta(id, &result)
func (c *Client) Meta(id *charm.Reference, result interface{}) (*charm.Reference, error) {
	if result == nil {
		return nil, fmt.Errorf("expected valid result pointer, not nil")
	}
	resultv := reflect.ValueOf(result)
	resultt := resultv.Type()
	if resultt.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("expected pointer, not %T", result)
	}
	resultt = resultt.Elem()
	if resultt.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected pointer to struct, not %T", result)
	}
	resultv = resultv.Elem()

	// At this point, resultv refers to the struct value pointed
	// to by result, and resultt is its type.

	numField := resultt.NumField()
	includes := make([]string, 0, numField)

	// results holds an entry for each field in the result value,
	// pointing to the value for that field.
	results := make(map[string]reflect.Value)
	for i := 0; i < numField; i++ {
		field := resultt.Field(i)
		if field.PkgPath != "" {
			// Field is private; ignore it.
			continue
		}
		if field.Anonymous {
			// At some point in the future, it might be nice to
			// support anonymous fields, but for now the
			// additional complexity doesn't seem worth it.
			return nil, fmt.Errorf("anonymous fields not supported")
		}
		apiName := field.Tag.Get("csclient")
		if apiName == "" {
			apiName = hyphenate(field.Name)
		}
		includes = append(includes, "include="+apiName)
		results[apiName] = resultv.FieldByName(field.Name).Addr()
	}
	// We unmarshal into rawResult, then unmarshal each field
	// separately into its place in the final result value.
	// Note that we can't use params.MetaAnyResponse because
	// that will unpack all the values inside the Meta field,
	// but we want to keep them raw so that we can unmarshal
	// them ourselves.
	var rawResult struct {
		Id   *charm.Reference
		Meta map[string]json.RawMessage
	}
	path := "/" + id.Path() + "/meta/any"
	if len(includes) > 0 {
		path += "?" + strings.Join(includes, "&")
	}
	if err := c.Get(path, &rawResult); err != nil {
		return nil, errgo.NoteMask(err, fmt.Sprintf("cannot get %q", path), errgo.Any)
	}
	// Note that the server is not required to send back values
	// for all fields. "If there is no metadata for the given meta path, the
	// element will be omitted"
	// See https://github.com/juju/charmstore/blob/v4/docs/API.md#get-idmetaany
	for name, r := range rawResult.Meta {
		v, ok := results[name]
		if !ok {
			// The server has produced a result that we
			// don't know about. Ignore it.
			continue
		}
		// Unmarshal the raw JSON into the final struct field.
		err := json.Unmarshal(r, v.Interface())
		if err != nil {
			return nil, errgo.Notef(err, "cannot unmarshal %s", name)
		}
	}
	return rawResult.Id, nil
}

// hyphenate returns the hyphenated version of the given
// field name, as specified in the Client.Meta method.
func hyphenate(s string) string {
	// TODO hyphenate FooHTTPBar as foo-http-bar?
	var buf bytes.Buffer
	var prevLower bool
	for _, r := range s {
		if !unicode.IsUpper(r) {
			prevLower = true
			buf.WriteRune(r)
			continue
		}
		if prevLower {
			buf.WriteRune('-')
		}
		buf.WriteRune(unicode.ToLower(r))
		prevLower = false
	}
	return buf.String()
}

// Get makes a GET request to the charm store, parsing the
// result as JSON into the given result value, which should be
// a pointer to the expected data, but may be nil if no result is
// desired.
func (c *Client) Get(path string, result interface{}) error {
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return errgo.Notef(err, "cannot make new request")
	}
	resp, err := c.Do(req, path)
	if err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	defer resp.Body.Close()
	// Parse the response.
	if err := parseResponseBody(resp.Body, result); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

func parseResponseBody(body io.Reader, result interface{}) error {
	data, err := ioutil.ReadAll(body)
	if err != nil {
		return errgo.Notef(err, "cannot read response body")
	}
	if result == nil {
		// The caller doesn't care about the response body.
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return errgo.Notef(err, "cannot unmarshal response %q", sizeLimit(data))
	}
	return nil
}

// DoWithBody is like Do except that the given getBody function is
// called to obtain the body for the HTTP request. Any body returned
// by getBody will be closed before DoWithBody returns.
func (c *Client) DoWithBody(req *http.Request, path string, getBody httpbakery.BodyGetter) (*http.Response, error) {
	if c.params.User != "" {
		userPass := c.params.User + ":" + c.params.Password
		authBasic := base64.StdEncoding.EncodeToString([]byte(userPass))
		req.Header.Set("Authorization", "Basic "+authBasic)
	}

	// Prepare the request.
	if !strings.HasPrefix(path, "/") {
		return nil, errgo.Newf("path %q is not absolute", path)
	}
	u, err := url.Parse(c.params.URL + "/" + apiVersion + path)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	req.URL = u

	// Send the request.
	resp, err := httpbakery.DoWithBody(c.params.HTTPClient, req, getBody, c.params.VisitWebPage)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()

	// Parse the response error.
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errgo.Notef(err, "cannot read response body")
	}
	var perr params.Error
	if err := json.Unmarshal(data, &perr); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal error response %q", sizeLimit(data))
	}
	if perr.Message == "" {
		return nil, errgo.Newf("error response with empty message %s", sizeLimit(data))
	}
	return nil, &perr
}

// Do makes an arbitrary request to the charm store.
// It adds appropriate headers to the given HTTP request,
// sends it to the charm store, and returns the resulting
// response. Do never returns a response with a status
// that is not http.StatusOK.
//
// The URL field in the request is ignored and overwritten.
//
// This is a low level method - more specific Client methods
// should be used when possible.
//
// For requests with a body (for example PUT or POST) use DoWithBody
// instead.
func (c *Client) Do(req *http.Request, path string) (*http.Response, error) {
	if req.Body != nil {
		return nil, errgo.New("body unexpectedly provided in http request - use DoWithBody")
	}
	return c.DoWithBody(req, path, noBody)
}

func noBody() (io.ReadCloser, error) {
	return nil, nil
}

func sizeLimit(data []byte) []byte {
	const max = 1024
	if len(data) < max {
		return data
	}
	return append(data[0:max], fmt.Sprintf(" ... [%d bytes omitted]", len(data)-max)...)
}

// Log sends a log message to the charmstore's log database.
func (cs *Client) Log(typ params.LogType, level params.LogLevel, message string, urls ...*charm.Reference) error {
	b, err := json.Marshal(message)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log message")
	}

	// Prepare and send the log.
	// TODO (frankban): we might want to buffer logs in order to reduce
	// requests.
	logs := []params.Log{{
		Data:  (*json.RawMessage)(&b),
		Level: level,
		Type:  typ,
		URLs:  urls,
	}}
	b, err = json.Marshal(logs)
	if err != nil {
		return errgo.Notef(err, "cannot marshal log message")
	}

	req, err := http.NewRequest("POST", "", nil)
	if err != nil {
		return errgo.Notef(err, "cannot create log request")
	}
	req.Header.Set("Content-Type", "application/json")
	body := bytes.NewReader(b)
	resp, err := cs.DoWithBody(req, "/log", httpbakery.SeekerBody(body))
	if err != nil {
		return errgo.Notef(err, "cannot send log message")
	}
	resp.Body.Close()
	return nil
}
