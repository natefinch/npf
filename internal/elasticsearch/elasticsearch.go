// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	//"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/errgo"
)

type Database struct {
	server string
	port   int
}

// AddNewEntity takes a mongo document and indexes it in ElasticSearch.
func (db *Database) AddNewEntity(index string, doc interface{}) error {
	// DELETE ME
	// An example auto-index document creation:
	//$ curl -XPOST 'http://localhost:9200/twitter/tweet' -d '{
	//"user" : "kimchy",
	//"post_date" : "2009-11-15T14:12:12",
	//"message" : "trying out Elasticsearch"
	//}'
	json, err := json.Marshal(doc)
	if err != nil {
		return errgo.Mask(err)
	}
	buf := bytes.NewBuffer(json)
	response, err := http.Post(db.url(index, "entity"), "application/json", buf)
	if err != nil {
		return errgo.Mask(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(response.Body)
		// Error checking within this error handler is not helpful.
		return fmt.Errorf("ElasticSearch POST response status: %d, body: %s", response.StatusCode, body)
	}
	return nil
}

// url constructs the URL for accessing the database.
// entityName is the ElasticSearch "type"
func (db *Database) url(index, entityName string) string {

	return fmt.Sprintf("http://%s:%d/%s/%s/", db.server, db.port, index, entityName)

}

// Get makes a GET request to the Database using the path parameter
// and returns the response
func (db *Database) Get(path string) (*http.Response, error) {
	return http.Get(fmt.Sprintf("http://%s:%d/%s", db.server, db.port, path))
}

// Delete makes a DELETE request to the database using the path parameter
func (db *Database) Delete(path string) (*http.Response, error) {
	req, err := http.NewRequest("DELETE", fmt.Sprintf("http://%s:%d/%s", db.server, db.port, path), nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// CatIndices does a GET request to the Database and parses the string
// response returning a slice containing the name of each index
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/_list_all_indexes.html
func (db *Database) CatIndices() ([]string, error) {
	resp, err := db.Get("_cat/indices")
	if err != nil {
		return nil, err
	}
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

// DeleteIndex does a DELETE request to the database for the given index
// http://www.elasticsearch.org/guide/en/elasticsearch/reference/current/indices-delete-index.html
// If the index does not exist or if the database cannot be
// reached, then an error is returned.
func (db *Database) DeleteIndex(index string) error {
	resp, err := db.Delete(index)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("index %s not found", index)
	}
	return fmt.Errorf("unexpected http response trying to delete index %s: %d", index, resp.StatusCode)
}
