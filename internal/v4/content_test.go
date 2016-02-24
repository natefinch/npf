// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	"github.com/juju/xml"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v2-unstable/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/storetesting"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v4"
)

var serveDiagramErrorsTests = []struct {
	about        string
	url          string
	expectStatus int
	expectBody   interface{}
}{{
	about:        "entity not found",
	url:          "~charmers/bundle/foo-23/diagram.svg",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: `no matching charm or bundle for "cs:~charmers/bundle/foo-23"`,
	},
}, {
	about:        "diagram for a charm",
	url:          "~charmers/wordpress/diagram.svg",
	expectStatus: http.StatusNotFound,
	expectBody: params.Error{
		Code:    params.ErrNotFound,
		Message: "diagrams not supported for charms",
	},
}}

func (s *APISuite) TestServeDiagramErrors(c *gc.C) {
	id := newResolvedURL("cs:~charmers/trusty/wordpress-42", 42)
	s.addPublicCharm(c, "wordpress", id)
	id = newResolvedURL("cs:~charmers/bundle/nopositionbundle-42", 42)
	s.addPublicBundle(c, "wordpress-simple", id, true)

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
	bundle := storetesting.NewBundle(&charm.BundleData{
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
	)

	url := newResolvedURL("cs:~charmers/bundle/wordpressbundle-42", 42)
	s.addRequiredCharms(c, bundle)
	err := s.store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("bundle/wordpressbundle/diagram.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)

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

func (s *APISuite) TestServeDiagramNoPosition(c *gc.C) {
	bundle := storetesting.NewBundle(
		&charm.BundleData{
			Services: map[string]*charm.ServiceSpec{
				"wordpress": {
					Charm: "wordpress",
				},
				"mysql": {
					Charm: "utopic/mysql-23",
					Annotations: map[string]string{
						"gui-x": "200",
						"gui-y": "200",
					},
				},
			},
		})

	url := newResolvedURL("cs:~charmers/bundle/wordpressbundle-42", 42)
	s.addRequiredCharms(c, bundle)
	err := s.store.AddBundleWithArchive(url, bundle)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("bundle/wordpressbundle/diagram.svg"),
	})
	// Check that the request succeeds and has the expected content type.
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %q", rec.Body.Bytes()))
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
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
	url := newResolvedURL("cs:~charmers/precise/wordpress-0", -1)
	for i, test := range serveReadMeTests {
		c.Logf("test %d: %s", i, test.name)
		wordpress := storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")
		content := fmt.Sprintf("some content %d", i)
		if test.name != "" {
			err := ioutil.WriteFile(filepath.Join(wordpress.Path, test.name), []byte(content), 0666)
			c.Assert(err, gc.IsNil)
		}

		url.URL.Revision = i
		err := s.store.AddCharmWithArchive(url, wordpress)
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
		c.Assert(err, gc.IsNil)

		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(url.URL.Path() + "/readme"),
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

func charmWithExtraFile(c *gc.C, name, file, content string) *charm.CharmDir {
	ch := storetesting.Charms.ClonedDir(c.MkDir(), name)
	err := ioutil.WriteFile(filepath.Join(ch.Path, file), []byte(content), 0666)
	c.Assert(err, gc.IsNil)
	return ch
}

func (s *APISuite) TestServeIcon(c *gc.C) {
	content := `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1">an icon, really</svg>`
	expected := `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1" viewBox="0 0 1 1">an icon, really</svg>`
	wordpress := charmWithExtraFile(c, "wordpress", "icon.svg", content)

	url := newResolvedURL("cs:~charmers/precise/wordpress-0", -1)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.URL.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, expected)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)

	// Test with revision -1
	noRevURL := url.URL
	noRevURL.Revision = -1
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(noRevURL.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, expected)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)

	// Reload the charm with an icon that already has viewBox.
	wordpress = storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")
	err = ioutil.WriteFile(filepath.Join(wordpress.Path, "icon.svg"), []byte(expected), 0666)
	c.Assert(err, gc.IsNil)

	url.URL.Revision++
	err = s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)

	// Check that we still get expected svg.
	rec = httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.URL.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, expected)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
}

func (s *APISuite) TestServeBundleIcon(c *gc.C) {
	s.addPublicBundle(c, "wordpress-simple", newResolvedURL("cs:~charmers/bundle/something-32", 32), true)

	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("~charmers/bundle/something-32/icon.svg"),
		ExpectStatus: http.StatusNotFound,
		ExpectBody: params.Error{
			Code:    params.ErrNotFound,
			Message: "icons not supported for bundles",
		},
	})
}

func (s *APISuite) TestServeDefaultIcon(c *gc.C) {
	wordpress := storetesting.Charms.ClonedDir(c.MkDir(), "wordpress")

	url := newResolvedURL("cs:~charmers/precise/wordpress-0", 0)
	err := s.store.AddCharmWithArchive(url, wordpress)
	c.Assert(err, gc.IsNil)
	err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
	c.Assert(err, gc.IsNil)

	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL(url.URL.Path() + "/icon.svg"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK)
	c.Assert(rec.Body.String(), gc.Equals, v4.DefaultIcon)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
	assertCacheControl(c, rec.Header(), true)
}

func (s *APISuite) TestServeDefaultIconForBadXML(c *gc.C) {

	for i, content := range []string{
		"\x89\x50\x4e\x47\x0d\x0a\x1a\x0a\x00\x00\x00\x0d\x49\x48\x44",
		// Technically this XML is not bad - we just can't parse it because
		// it's got internally defined character entities. Nonetheless, we treat
		// it as "bad" for the time being.
		cloudfoundrySVG,
	} {
		wordpress := charmWithExtraFile(c, "wordpress", "icon.svg", content)

		url := newResolvedURL("cs:~charmers/precise/wordpress-0", -1)
		url.URL.Revision = i
		err := s.store.AddCharmWithArchive(url, wordpress)
		c.Assert(err, gc.IsNil)
		err = s.store.SetPerms(&url.URL, "unpublished.read", params.Everyone, url.URL.User)
		c.Assert(err, gc.IsNil)

		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler: s.srv,
			URL:     storeURL(url.URL.Path() + "/icon.svg"),
		})
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		c.Assert(rec.Body.String(), gc.Equals, v4.DefaultIcon)
		c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "image/svg+xml")
		assertCacheControl(c, rec.Header(), true)
	}
}

// assertXMLEqual assers that the xml contained in the
// two slices is equal, without caring about namespace
// declarations or attribute ordering.
func assertXMLEqual(c *gc.C, body []byte, expect []byte) {
	decBody := xml.NewDecoder(bytes.NewReader(body))
	decExpect := xml.NewDecoder(bytes.NewReader(expect))
	for i := 0; ; i++ {
		tok0, err0 := decBody.Token()
		tok1, err1 := decExpect.Token()
		if err1 != nil {
			c.Assert(err0, gc.NotNil)
			c.Assert(err0.Error(), gc.Equals, err1.Error())
			break
		}
		ok, err := tokenEqual(tok0, tok1)
		if !ok {
			c.Logf("got %#v", tok0)
			c.Logf("want %#v", tok1)
			c.Fatalf("mismatch at token %d: %v", i, err)
		}
	}
}

func tokenEqual(tok0, tok1 xml.Token) (bool, error) {
	tok0 = canonicalXMLToken(tok0)
	tok1 = canonicalXMLToken(tok1)
	return jc.DeepEqual(tok0, tok1)
}

func canonicalXMLToken(tok xml.Token) xml.Token {
	start, ok := tok.(xml.StartElement)
	if !ok {
		return tok
	}
	// Remove all namespace-defining attributes.
	j := 0
	for _, attr := range start.Attr {
		if attr.Name.Local == "xmlns" && attr.Name.Space == "" ||
			attr.Name.Space == "xmlns" {
			continue
		}
		start.Attr[j] = attr
		j++
	}
	start.Attr = start.Attr[0:j]
	sort.Sort(attrByName(start.Attr))
	return start
}

type attrByName []xml.Attr

func (a attrByName) Len() int      { return len(a) }
func (a attrByName) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a attrByName) Less(i, j int) bool {
	if a[i].Name.Space != a[j].Name.Space {
		return a[i].Name.Space < a[j].Name.Space
	}
	return a[i].Name.Local < a[j].Name.Local
}

// assertXMLContains asserts that the XML in body is well formed, and
// contains at least one token that satisfies each of the functions in need.
func assertXMLContains(c *gc.C, body []byte, need map[string]func(xml.Token) bool) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		c.Assert(err, gc.IsNil)
		for what, f := range need {
			if f(tok) {
				delete(need, what)
			}
		}
	}
	c.Assert(need, gc.HasLen, 0, gc.Commentf("body:\n%s", body))
}

func isStartElementWithName(name string) func(xml.Token) bool {
	return func(tok xml.Token) bool {
		startElem, ok := tok.(xml.StartElement)
		return ok && startElem.Name.Local == name
	}
}

func isStartElementWithAttr(name, attr, val string) func(xml.Token) bool {
	return func(tok xml.Token) bool {
		startElem, ok := tok.(xml.StartElement)
		if !ok {
			return false
		}
		for _, a := range startElem.Attr {
			if a.Name.Local == attr && a.Value == val {
				return true
			}
		}
		return false
	}
}

const cloudfoundrySVG = `<?xml version="1.0" encoding="utf-8"?>
<!-- Generator: Adobe Illustrator 18.1.0, SVG Export Plug-In . SVG Version: 6.00 Build 0)  -->
<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [
	<!ENTITY ns_extend "http://ns.adobe.com/Extensibility/1.0/">
	<!ENTITY ns_ai "http://ns.adobe.com/AdobeIllustrator/10.0/">
	<!ENTITY ns_graphs "http://ns.adobe.com/Graphs/1.0/">
	<!ENTITY ns_vars "http://ns.adobe.com/Variables/1.0/">
	<!ENTITY ns_imrep "http://ns.adobe.com/ImageReplacement/1.0/">
	<!ENTITY ns_sfw "http://ns.adobe.com/SaveForWeb/1.0/">
	<!ENTITY ns_custom "http://ns.adobe.com/GenericCustomNamespace/1.0/">
	<!ENTITY ns_adobe_xpath "http://ns.adobe.com/XPath/1.0/">
]>
<svg version="1.1" id="Layer_1" xmlns:x="&ns_extend;" xmlns:i="&ns_ai;" xmlns:graph="&ns_graphs;"
	 xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" x="0px" y="0px" viewBox="0 0 96 96"
	 enable-background="new 0 0 96 96" xml:space="preserve">
content omitted
</svg>
`
