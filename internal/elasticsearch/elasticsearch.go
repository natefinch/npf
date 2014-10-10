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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/juju/errgo"
	"github.com/juju/loggo"
)

var log = loggo.GetLogger("charmstore.elasticsearch")

type Database struct {
	Addr string
}

// EnsureID tests to see a document of the given index, type_, and id exists
// in ElasticSearch.
func (db *Database) EnsureID(index, type_, id string) (bool, error) {
	// TODO (bac) We should limit the fields to the id to avoid retrieving
	// data we don't use.
	response, err := http.Get(db.url(index, type_, id, "_source"))
	if err != nil {
		return false, errgo.Mask(err)
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK, nil
}

// GetDocument retrieves the document with the given index, type_ and id and
// unmarshals the json response into doc.
func (db *Database) GetDocument(index, type_, id string, doc interface{}) error {
	status, _, body, err := db.request("GET", db.url(index, type_, id, "_source"), "", "")
	if err != nil {
		return errgo.Mask(err)
	}
	if status != http.StatusOK {
		return errgo.Newf("ElasticSearch GET response status: %d, body: %s", status, body)
	}
	err = json.Unmarshal(body, doc)
	if err != nil {
		return errgo.Notef(err, "cannot unmarshal body: %s", body)
	}
	return nil
}

// PostDocument creates a new auto id document with the given index and _type
// and returns the generated id of the document.
func (db *Database) PostDocument(index, type_ string, doc interface{}) (string, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return "", errgo.Mask(err)
	}
	// CONSIDER: err := db.post(db.url(index, type_), doc, &respData)
	status, _, body, err := db.request("POST", db.url(index, type_), "application/json", string(data))
	if err != nil {
		return "", errgo.Mask(err)
	}
	if status != http.StatusCreated {
		return "", errgo.Newf("ElasticSearch POST response status: %d, body: %s", status, body)
	}
	var respdata map[string]interface{}
	if err := json.Unmarshal(body, &respdata); err != nil {
		return "", errgo.Notef(err, "cannot unmarshal body")
	}
	return respdata["_id"].(string), nil
}

// PutDocument creates or updates the document with the given index, type_ and
// id.
func (db *Database) PutDocument(index, type_, id string, doc interface{}) error {
	data, err := json.Marshal(doc)
	if err != nil {
		return errgo.Mask(err)
	}
	status, _, body, err := db.request("PUT", db.url(index, type_, id), "application/json", string(data))
	if err != nil {
		return errgo.Mask(err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return errgo.Newf("ElasticSearch PUT response status: %d, body: %s", status, body)
	}
	var respdata map[string]interface{}
	if err := json.Unmarshal(body, &respdata); err != nil {
		return errgo.Notef(err, "cannot unmarshal body")
	}
	return nil
}

// PutIndex creates or updates the index with the given configuration.
func (db *Database) PutIndex(index string, config interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return errgo.Mask(err)
	}
	status, _, body, err := db.request("PUT", db.url(index), "application/json", string(data))
	if err != nil {
		return errgo.Mask(err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return errgo.Newf("ElasticSearch PUT response status: %d, body: %s", status, body)
	}
	var respdata map[string]interface{}
	if err := json.Unmarshal(body, &respdata); err != nil {
		return errgo.Notef(err, "cannot unmarshal body")
	}
	return nil
}

// PutMapping creates or updates the mapping with the given configuration.
func (db *Database) PutMapping(index, type_ string, config interface{}) error {
	data, err := json.Marshal(config)
	if err != nil {
		return errgo.Mask(err)
	}
	status, _, body, err := db.request("PUT", db.url(index, type_, "_mapping"), "application/json", string(data))
	if err != nil {
		return errgo.Mask(err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return errgo.Newf("ElasticSearch PUT response status: %d, body: %s", status, body)
	}
	var respdata map[string]interface{}
	if err := json.Unmarshal(body, &respdata); err != nil {
		return errgo.Notef(err, "cannot unmarshal body")
	}
	return nil
}

// url constructs the URL for accessing the database.
func (db *Database) url(pathParts ...string) string {
	path := path.Join(pathParts...)
	url := &url.URL{
		Scheme: "http",
		Host:   db.Addr,
		Path:   path,
	}
	return url.String()

}

// Delete makes a DELETE request to the database using the path parameter.
func (db *Database) delete(path string) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", db.url(path), nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// ListAllIndexes does a GET request to the Database and parses the string
// response returning a slice containing the name of each index.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/_list_all_indexes.html
func (db *Database) ListAllIndexes() ([]string, error) {
	response, err := http.Get(db.url("_aliases"))
	if err != nil {
		return nil, errgo.Mask(err)
	}
	defer response.Body.Close()

	body, _ := ioutil.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		return nil, errgo.Newf("ElasticSearch GET response status: %d %s, body: %s", response.StatusCode, response.Status, body)
	}
	var result map[string]interface{}
	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal body: %s", body)
	}
	var indices []string
	for key := range result {
		// Some ElasticSearch plugins create indices (e.g. ".marvel...") for their
		// use.  Ignore any that start with a dot.
		if !strings.HasPrefix(key, ".") {
			indices = append(indices, key)
		}
	}
	return indices, nil
}

// DeleteIndex deletes the index with the given name from the database.
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/indices-delete-index.html
// If the index does not exist or if the database cannot be
// reached, then an error is returned.
func (db *Database) DeleteIndex(index string) error {
	resp, err := db.delete(index)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		notFound := &ErrNotFound{}
		notFound.Message_ = fmt.Sprintf("index %s not found", index)
		notFound.SetLocation(0)
		return notFound
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return errgo.Mask(err)
	}
	return errgo.Newf("unexpected http response trying to delete index %s: %s(%d) body:%s", index, resp.Status, resp.StatusCode, body)
}

type SearchResult struct {
	Hits struct {
		Total    int     `json:"total"`
		MaxScore float64 `json:"max_score"`
		Hits     []struct {
			Index  string          `json:"_index"`
			Type   string          `json:"_type"`
			ID     string          `json:"_id"`
			Score  float64         `json:"_score"`
			Source json.RawMessage `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
}

func (db *Database) Search(index, type_, query string) (SearchResult, error) {
	_, _, result, err := db.request("GET", db.url(index, type_, "_search"), "application/json", query)
	if err != nil {
		return SearchResult{}, errgo.Mask(err)
	}
	var data SearchResult
	if err := json.Unmarshal(result, &data); err != nil {
		return SearchResult{}, errgo.Notef(err, "cannot unmarshal body")
	}
	return data, nil
}

// request performs a request on the elasticsearch server. request will
// log to debug the details of all performed requests.
func (db *Database) request(method, url string, contentType, body string) (int, string, []byte, error) {
	log.Debugf(">>> %s %s", method, url)
	if len(body) > 0 {
		log.Debugf(">>> Content-Type: %s", contentType)
		log.Debugf(">>> %s", body)
	}
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		log.Debugf("*** %s", err)
		return 0, "", nil, errgo.Notef(err, "cannot create request")
	}
	if len(body) > 0 && len(contentType) > 0 {
		req.Header.Add("Content-Type", contentType)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Debugf("*** %s", err)
		return 0, "", nil, errgo.Mask(err)
	}
	defer response.Body.Close()
	log.Debugf("<<< %s", response.Status)
	log.Debugf("<<< Content-Type: %s", response.Header.Get("Content-Type"))
	data, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Debugf("*** %s", err)
		return 0, "", nil, errgo.Mask(err)
	}
	log.Debugf("<<< %s", data)
	return response.StatusCode, response.Header.Get("Content-Type"), data, nil
}

// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/indices-refresh.html
func (db *Database) RefreshIndex(index string) error {
	status, _, body, err := db.request("POST", db.url(index, "_refresh"), "", "")
	if err != nil {
		return errgo.Mask(err)
	}
	if status != http.StatusCreated {
		return errgo.Newf("ElasticSearch POST response status: %d, body: %s", status, body)
	}
	return nil
}

type ErrNotFound struct {
	errgo.Err
}
