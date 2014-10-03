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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/juju/errgo"
)

type Database struct {
	Addr string
}

// GetDocument retrieves the document with the given index, type_ and id and
// unmarshals the json response into doc.
func (db *Database) GetDocument(index, type_, id string, doc interface{}) error {
	response, err := http.Get(db.url(index, type_, id, "_source"))
	if err != nil {
		return errgo.Mask(err)
	}
	defer response.Body.Close()
	dec := json.NewDecoder(response.Body)
	if err := dec.Decode(doc); err != nil {
		return errgo.Notef(err, "cannot unmarshal body")
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
	buf := bytes.NewReader(data)
	response, err := http.Post(db.url(index, type_), "application/json", buf)
	if err != nil {
		return "", errgo.Mask(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(response.Body)
		// Error checking within this error handler is not helpful.
		return "", errgo.Newf("ElasticSearch POST response status: %d %s, body: %s", response.StatusCode, response.Status, body)
	}
	var respdata map[string]interface{}
	dec := json.NewDecoder(response.Body)
	if err := dec.Decode(&respdata); err != nil {
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
	buf := bytes.NewReader(data)
	req, err := http.NewRequest("PUT", db.url(index, type_, id), buf)
	req.Header["Content-Type"] = []string{"application/json"}
	if err != nil {
		return errgo.Mask(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return errgo.Mask(err)
	}
	defer response.Body.Close()
	body, _ := ioutil.ReadAll(response.Body)
	if (response.StatusCode != http.StatusCreated) && (response.StatusCode != http.StatusOK) {
		// Error checking within this error handler is not helpful.
		return errgo.Newf("ElasticSearch PUT response status: %d %s, body: %s", response.StatusCode, response.Status, body)
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
	resp, err := http.Get(db.url("_cat/indices"))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	var indices []string
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) > 1 {
			indices = append(indices, fields[1])
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

type ErrNotFound struct {
	errgo.Err
}
