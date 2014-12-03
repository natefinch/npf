// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

var serveDiagramErrorsTests = []struct {
	about        string
	url          string
	expectStatus int
	expectBody   interface{}
}{{
	about:        "entity not found",
	url:          "bundle/foo-23/diagram.svg",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "entity not found",
	},
}, {
	about:        "diagram for a charm",
	url:          "wordpress/diagram.svg",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "diagrams not supported for charms",
	},
}, {
	about:        "bundle with no position info",
	url:          "nopositionbundle/diagram.svg",
	expectStatus: http.StatusInternalServerError,
	expectBody: params.Error{
		Message: `cannot create canvas: service "mysql" does not have a valid position`,
	},
}}

func (s *APISuite) TestServeDiagramErrors(c *gc.C) {
	s.addCharm(c, "wordpress", "cs:trusty/wordpress-42")
	s.addBundle(c, "wordpress-simple", "cs:bundle/nopositionbundle-42")
	for i, test := range serveDiagramErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL(test.url),
			ExpectStatus: test.expectStatus,
			ExpectBody:   test.expectBody,
		})
	}
}

func (s *APISuite) TestServeDiagram(c *gc.C) {
	patchArchiveCacheAges(s)
	bundle := &testingBundle{
		data: &charm.BundleData{
			Services: map[string]*charm.ServiceSpec{
				"wordpress": {
					Charm: "wordpress",
					Annotations: map[string]string{
						"gui-x": "100",
						"gui-y": "200",
					},
				},
				"mysql": {
					Charm: "utopic/mysql-23",
					Annotations: map[string]string{
						"gui-x": "200",
						"gui-y": "200",
					},
				},
			},
		},
	}

	err := s.store.AddBundle(bundle, charmstore.AddParams{
		URL:      charm.MustParseReference("cs:bundle/wordpressbundle-42"),
		BlobName: "blobName",
		BlobHash: fakeBlobHash,
		BlobSize: fakeBlobSize,
	})
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("bundle/wordpressbundle/diagram.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), false)

	// Check that the output contains valid XML with an SVG tag,
	// but don't check the details of the output so that this test doesn't
	// break every time the jujusvg presentation changes.
	// Also check that we get an image for each service containing the charm
	// icon link.
	assertXMLContains(c, rec.Body.Bytes(), map[string]func(xml.Token) bool{
		"svg element":    isStartElementWithName("svg"),
		"wordpress icon": isStartElementWithAttr("image", "href", "../../wordpress/icon.svg"),
		"mysql icon":     isStartElementWithAttr("image", "href", "../../utopic/mysql-23/icon.svg"),
	})

	// Do the same check again, but with the short form of the id;
	// the relative links should change accordingly.
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("wordpressbundle/diagram.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %q", rec.Body.Bytes()))

	// Check that the output contains valid XML with an SVG tag,
	// but don't check the details of the output so that this test doesn't
	// break every time the jujusvg presentation changes.
	// Also check that we get an image for each service containing the charm
	// icon link.
	assertXMLContains(c, rec.Body.Bytes(), map[string]func(xml.Token) bool{
		"svg element":    isStartElementWithName("svg"),
		"wordpress icon": isStartElementWithAttr("image", "href", "../wordpress/icon.svg"),
		"mysql icon":     isStartElementWithAttr("image", "href", "../utopic/mysql-23/icon.svg"),
	})
}

var serveReadMeTests = []struct {
	name           string
	expectNotFound bool
}{{
	name: "README.md",
}, {
	name: "README.rst",
}, {
	name: "readme",
}, {
	name: "README",
}, {
	name: "ReadMe.Txt",
}, {
	name: "README.ex",
}, {
	name:           "",
	expectNotFound: true,
}, {
	name:           "readme-youtube-subscribe.html",
	expectNotFound: true,
}, {
	name:           "readme Dutch.txt",
	expectNotFound: true,
}, {
	name:           "readme Dutch.txt",
	expectNotFound: true,
}, {
	name:           "README.debugging",
	expectNotFound: true,
}}

func (s *APISuite) TestServeReadMe(c *gc.C) {
	patchArchiveCacheAges(s)
	url := charm.MustParseReference("cs:precise/wordpress-0")
	for i, test := range serveReadMeTests {
		c.Logf("test %d: %s", i, test.name)
		wordpress := storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")
		content := fmt.Sprintf("some content %d", i)
		if test.name != "" {
			err := ioutil.WriteFile(filepath.Join(wordpress.Path, test.name), []byte(content), 0666)
			c.Assert(err, gc.IsNil)
		}

		url.Revision = i
		err := s.store.AddCharmWithArchive(url, wordpress)
		c.Assert(err, gc.IsNil)

		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(url.Path() + "/readme"),
		})
		if test.expectNotFound {
			c.Assert(rec.Code, gc.Equals, http.StatusNotFound)
			c.Assert(rec.Body.String(), jc.JSONEquals, params.Error{
				Code:    params.ErrNotFound,
				Message: "not found",
			})
		} else {
			c.Assert(rec.Code, gc.Equals, http.StatusOK)
			c.Assert(rec.Body.String(), gc.DeepEquals, content)
			assertCacheControl(c, rec.Header(), true)
		}
	}
}

func (s *APISuite) TestServeReadMeEntityNotFound(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/nothingatall-32/readme"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "cannot get README: entity not found",
		},
	})
}

func (s *APISuite) TestServeIconEntityNotFound(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("precise/nothingatall-32/icon.svg"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "cannot get icon: entity not found",
		},
	})
}

func (s *APISuite) TestServeIcon(c *gc.C) {
	patchArchiveCacheAges(s)
	url := charm.MustParseReference("cs:precise/wordpress-0")
	wordpress := storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")
	content := "<xml>an icon, really</xml>"
	err := ioutil.WriteFile(filepath.Join(wordpress.Path, "icon.svg"), []byte(content), 0666)
	c.Assert(err, gc.IsNil)

	err = s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, content)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)

	url.Revision = -1
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, content)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), false)
}

func (s *APISuite) TestServeBundleIcon(c *gc.C) {
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("bundle/something-32/icon.svg"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "icons not supported for bundles",
		},
	})
}

func (s *APISuite) TestServeDefaultIcon(c *gc.C) {
	patchArchiveCacheAges(s)
	url := charm.MustParseReference("cs:precise/wordpress-0")
	wordpress := storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")

	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, v4.DefaultIcon)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)
}
