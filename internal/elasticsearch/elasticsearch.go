// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// elasticsearch package api attempts to name methods to match the
// corresponding elasticsearch endpoint. Methods names like CatIndices are
// named as such because they correspond to /_cat/indices elasticsearch
// endpoint.
// There is no reason to use different vocabulary from that of elasticsearch.
// Use the elasticsearch terminology and avoid mapping names of things.

package elasticsearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
)

var log = loggo.GetLogger("charmstore.elasticsearch")

type ElasticSearchError struct {
	Err    string `json:"error"`
	Status int    `json:"status"`
}

func (e ElasticSearchError) Error() string {
	return e.Err
}

type Database struct {
	Addr string
}

// DeleteIndex deletes the index with the given name from the database.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/indices-delete-index.html
// If the index does not exist or if the database cannot be
// reached, then an error is returned.
func (db *Database) DeleteIndex(index string) error {
	if err := db.delete(db.url(index), nil, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// EnsureID tests to see a document of the given index, type_, and id exists
// in ElasticSearch.
func (db *Database) EnsureID(index, type_, id string) (bool, error) {
	if err := db.get(db.url(index, type_, id)+"?_source=false", nil, nil); err != nil {
		if ese, ok := err.(ElasticSearchError); ok && ese.Status == http.StatusNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetDocument retrieves the document with the given index, type_ and id and
// unmarshals the json response into v.
func (db *Database) GetDocument(index, type_, id string, v interface{}) error {
	if err := db.get(db.url(index, type_, id, "_source"), nil, v); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// ListAllIndexes retreieves the list of all user indexes in the elasticsearch database.
// indexes that are generated to to support plugins are filtered out of the list that
// is returned.
func (db *Database) ListAllIndexes() ([]string, error) {
	var result map[string]interface{}
	if err := db.get(db.url("_aliases"), nil, &result); err != nil {
		return nil, errgo.Mask(err)
	}
	var indexes []string
	for key := range result {
		// Some ElasticSearch plugins create indexes (e.g. ".marvel...") for their
		// use.  Ignore any that start with a dot.
		if !strings.HasPrefix(key, ".") {
			indexes = append(indexes, key)
		}
	}
	return indexes, nil
}

// PostDocument creates a new auto id document with the given index and _type
// and returns the generated id of the document.
func (db *Database) PostDocument(index, type_ string, doc interface{}) (string, error) {
	var resp struct {
		ID string `json:"_id"`
	}
	if err := db.post(db.url(index, type_), doc, &resp); err != nil {
		return "", errgo.Mask(err)
	}
	return resp.ID, nil
}

// PutDocument creates or updates the document with the given index, type_ and
// id.
func (db *Database) PutDocument(index, type_, id string, doc interface{}) error {
	if err := db.put(db.url(index, type_, id), doc, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// PutIndex creates the index with the given configuration.
func (db *Database) PutIndex(index string, config interface{}) error {
	if err := db.put(db.url(index), config, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// PutMapping creates or updates the mapping with the given configuration.
func (db *Database) PutMapping(index, type_ string, config interface{}) error {
	if err := db.put(db.url(index, "_mapping", type_), config, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// RefreshIndex posts a _refresh to the index in the database.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/indices-refresh.html
func (db *Database) RefreshIndex(index string) error {
	if err := db.post(db.url(index, "_refresh"), nil, nil); err != nil {
		return errgo.Mask(err)
	}
	return nil
}

// Search performs the query specified in q on the values in index/type_ and returns a
// SearchResult.
func (db *Database) Search(index, type_ string, q QueryDSL) (SearchResult, error) {
	var sr SearchResult
	if err := db.get(db.url(index, type_, "_search"), q, &sr); err != nil {
		return SearchResult{}, errgo.Notef(err, "search failed")
	}
	return sr, nil
}

// do performs a request on the elasticsearch server. If body is not nil it will be
// marsheled as a json object and sent with the request. If v is non nil the response
// body will be unmarshalled into the value it points to.
func (db *Database) do(method, url string, body, v interface{}) error {
	log.Debugf(">>> %s %s", method, url)
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return errgo.Notef(err, "can not marshaling body")
		}
		log.Debugf(">>> %s", b)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		log.Debugf("*** %s", err)
		return errgo.Notef(err, "cannot create request")
	}
	if body != nil {
		req.Header.Add("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Debugf("*** %s", err)
		return errgo.Mask(err)
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Debugf("*** %s", err)
		return errgo.Notef(err, "cannot read response")
	}
	log.Debugf("<<< %s", resp.Status)
	log.Debugf("<<< %s", b)
	if resp.StatusCode >= http.StatusBadRequest {
		var eserr ElasticSearchError
		if err = json.Unmarshal(b, &eserr); err != nil {
			log.Debugf("*** %s", err)
			eserr.Err = fmt.Sprintf(`elasticsearch status "%s"`, resp.Status)
			eserr.Status = resp.StatusCode
		}
		return eserr
	}
	if v != nil {
		if err = json.Unmarshal(b, v); err != nil {
			log.Debugf("*** %s", err)
			return errgo.Notef(err, "cannot unmarshal response")
		}
	}
	return nil
}

// delete makes a DELETE request to the database url. A non-nil body will be
// sent with the request and if v is not nill then the response will be unmarshaled
// into tha value it points to.
func (db *Database) delete(url string, body, v interface{}) error {
	return db.do("DELETE", url, body, v)
}

// get makes a GET request to the database url. A non-nil body will be
// sent with the request and if v is not nill then the response will be unmarshaled
// into tha value it points to.
func (db *Database) get(url string, body, v interface{}) error {
	return db.do("GET", url, body, v)
}

// post makes a POST request to the database url. A non-nil body will be
// sent with the request and if v is not nill then the response will be unmarshaled
// into tha value it points to.
func (db *Database) post(url string, body, v interface{}) error {
	return db.do("POST", url, body, v)
}

// put makes a PUT request to the database url. A non-nil body will be
// sent with the request and if v is not nill then the response will be unmarshaled
// into tha value it points to.
func (db *Database) put(url string, body, v interface{}) error {
	return db.do("PUT", url, body, v)
}

// url constructs the URL for accessing the database.
func (db *Database) url(pathParts ...string) string {
	for i, part := range pathParts {
		pathParts[i] = url.QueryEscape(part)
	}
	path := path.Join(pathParts...)
	url := &url.URL{
		Scheme: "http",
		Host:   db.Addr,
		Path:   path,
	}
	return url.String()

}

// Index creates a reference to an index in the elasticsearch database.
func (db *Database) Index(name string) *Index {
	return &Index{Database: db, Index: name}
}

// Index represents an index in the elasticsearch database.
type Index struct {
	Database *Database
	Index    string
}

func (i *Index) PutDocument(type_, id string, doc interface{}) error {
	return i.Database.PutDocument(i.Index, type_, id, doc)
}

func (i *Index) GetDocument(type_, id string, doc interface{}) error {
	return i.Database.GetDocument(i.Index, type_, id, doc)
}

func (i *Index) Search(type_ string, q QueryDSL) (SearchResult, error) {
	return i.Database.Search(i.Index, type_, q)
}

func (i *Index) Delete() error {
	return i.Database.DeleteIndex(i.Index)
}

// SearchResult is the result returned after performing a search in elasticsearch
type SearchResult struct {
	Hits struct {
		Total    int     `json:"total"`
		MaxScore float64 `json:"max_score"`
		Hits     []Hit   `json:"hits"`
	} `json:"hits"`
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
}

// Hit represents an individual search hit returned from elasticsearch
type Hit struct {
	Index  string          `json:"_index"`
	Type   string          `json:"_type"`
	ID     string          `json:"_id"`
	Score  float64         `json:"_score"`
	Source json.RawMessage `json:"_source"`
	Fields Fields          `json:"fields"`
}

type Fields map[string][]interface{}

// Get retrieves the first value of key in the fields map. If no such value
// exists then it will return nil.
func (f Fields) Get(key string) interface{} {
	if len(f[key]) < 1 {
		return nil
	}
	return f[key][0]
}

// Get retrieves the first value of key in the fields map, and coerces it into a
// string. If no such value exists or the value is not a string, then "" will be returned.
func (f Fields) GetString(key string) string {
	s, ok := f.Get(key).(string)
	if !ok {
		return ""
	}
	return s
}

// EscapeRegexp returns the supplied string with any special characters escaped.
// A regular expression match on the returned string will match exactly the characters
// in the supplied string.
func EscapeRegexp(s string) string {
	return regexpReplacer.Replace(s)
}

var regexpReplacer = strings.NewReplacer(
	`.`, `\.`,
	`?`, `\?`,
	`+`, `\+`,
	`*`, `\*`,
	`|`, `\|`,
	`{`, `\{`,
	`}`, `\}`,
	`[`, `\[`,
	`]`, `\]`,
	`(`, `\(`,
	`)`, `\)`,
	`"`, `\"`,
	`\`, `\\`,
	`#`, `\#`,
	`@`, `\@`,
	`&`, `\&`,
	`<`, `\<`,
	`>`, `\>`,
	`~`, `\~`,
)
