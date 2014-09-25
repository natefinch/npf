// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package elasticsearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	//"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/errgo"
)

type Database struct {
	server string
	port   int
	index  string
}

// AddNewEntity takes a mongo document and indexes it in ElasticSearch.
func (db *Database) AddNewEntity(doc interface{}) error {
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
	response, err := http.Post(db.url("entity"), "application/json", buf)
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
func (db *Database) url(entityName string) string {

	return fmt.Sprintf("http://%s:%d/%s/%s", db.server, db.port, db.index, entityName)

}
