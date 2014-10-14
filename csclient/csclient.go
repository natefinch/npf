// The csclient package provides access to the charm store API.
package csclient

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/juju/errgo"

	"github.com/juju/charmstore/params"
)

const apiVersion = "v4"

// Client represents the client side of a charm store.
type Client struct {
	params Params
}

// Params holds parameters for creating a new charm store client.
type Params struct {
	// URL holds the root endpoint URL of the charmstore,
	// with no trailing slash, not including the version.
	// For example http://charms.ubuntu.com
	// TODO default this to global charm store address.
	URL string

	// User and Password hold the authentication credentials
	// for the client. If User is empty, no credentials will be
	// sent.
	User     string
	Password string

	// HTTPClient holds the HTTP client to use when making
	// requests to the store. If nil, http.DefaultClient will
	// be used.
	HTTPClient *http.Client
}

// New returns a new charm store client.
func New(p Params) *Client {
	if p.HTTPClient == nil {
		p.HTTPClient = http.DefaultClient
	}
	return &Client{
		params: p,
	}
}

// Do makes an arbitrary request to the charm store.
// It adds appropriate headers to the given HTTP request,
// sends it to the charm store and parses the result
// as JSON into the given result value, which should be a pointer to the
// expected data, but may be nil if no result is expected.
//
// The URL field in the request is ignored and overwritten.
//
// This is a low level method - more specific Client methods
// should be used when possible.
func (c *Client) Do(req *http.Request, path string, result interface{}) error {
	if c.params.User != "" {
		userPass := c.params.User + ":" + c.params.Password
		authBasic := base64.StdEncoding.EncodeToString([]byte(userPass))
		req.Header.Set("Authorization", "Basic "+authBasic)
	}

	// Prepare the request.
	if !strings.HasPrefix(path, "/") {
		return errgo.Newf("path %q is not absolute", path)
	}
	u, err := url.Parse(c.params.URL + "/" + apiVersion + path)
	if err != nil {
		return errgo.Mask(err)
	}
	req.URL = u

	// Send the request.
	resp, err := c.params.HTTPClient.Do(req)
	if err != nil {
		return errgo.Mask(err)
	}
	defer resp.Body.Close()

	// Parse the response.
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errgo.Notef(err, "cannot read response body")
	}
	if resp.StatusCode != http.StatusOK {
		var perr params.Error
		if err := json.Unmarshal(data, &perr); err != nil {
			return errgo.Notef(err, "cannot unmarshal error response %q", sizeLimit(data))
		}
		if perr.Message == "" {
			return errgo.Newf("error response with empty message %s", sizeLimit(data))
		}
		return &perr
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

func sizeLimit(data []byte) []byte {
	const max = 1024
	if len(data) < max {
		return data
	}
	return append(data[0:max], fmt.Sprintf(" ... [%d bytes omitted]", len(data)-max)...)
}
