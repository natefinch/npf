// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET debug/ingestion
// http://tinyurl.com/nj77rcr
//
// POST debug/ingestion
// http://tinyurl.com/o27hqxe
func (h *Handler) serveIngestion(w http.ResponseWriter, req *http.Request) error {
	if err := h.authenticate(w, req); err != nil {
		return err
	}
	switch req.Method {
	case "GET":
		return h.getIngestionLogs(w, req)
	case "POST":
		return h.postIngestionLog(w, req)
	}
	return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s method not allowed", req.Method)
}

func (h *Handler) getIngestionLogs(w http.ResponseWriter, req *http.Request) error {
	w.Header().Set("content-type", "application/json")
	encoder := json.NewEncoder(w)

	// Retrieve values from the query string.
	limit, err := getPositiveIntValue(req.Form, "limit", 1000)
	if err != nil {
		return badRequestf(err, "invalid query")
	}
	offset, err := getPositiveIntValue(req.Form, "offset", 0)
	if err != nil {
		return badRequestf(err, "invalid query")
	}
	id := req.Form.Get("id")
	levelStr := req.Form.Get("level")

	// Build the Mongo query.
	query := bson.D{}
	if id != "" {
		url, err := charm.ParseReference(id)
		if err != nil {
			return badRequestf(err, "invalid query: cannot parse id")
		}
		query = append(query, bson.DocElem{"urls", url})
	}
	if levelStr != "" {
		level, ok := paramsLevels[params.IngestionLogLevel(levelStr)]
		if !ok {
			return badRequestf(nil, "invalid query: invalid log level")
		}
		query = append(query, bson.DocElem{"level", level})
	}

	// Retrieve the ingestion logs.
	var log mongodoc.IngestionLog
	iter := h.store.DB.IngestionLogs().Find(query).Sort("-_id").Skip(offset).Limit(limit).Iter()
	for iter.Next(&log) {
		var message json.RawMessage
		if err := json.Unmarshal(log.Message, &message); err != nil {
			return errgo.Notef(err, "cannot unmarshal the log message")
		}
		logResponse := &params.IngestionLogResponse{
			Message: message,
			Level:   mongodocLevels[log.Level],
			URLs:    log.URLs,
			Time:    log.Time.UTC(),
		}
		if err := encoder.Encode(logResponse); err != nil {
			return errgo.Notef(err, "cannot marshal ingestion log")
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot retrieve ingestion logs")
	}
	return nil
}

func (h *Handler) postIngestionLog(w http.ResponseWriter, req *http.Request) error {
	// Check the request content type.
	if ctype := req.Header.Get("Content-Type"); ctype != "application/json" {
		return badRequestf(nil, "unexpected Content-Type %q; expected 'application/json'", ctype)
	}

	// Unmarshal the request body.
	var log params.IngestionLog
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(&log); err != nil {
		return badRequestf(err, "cannot unmarshal body")
	}

	// Unmarshal the log message.
	var message json.RawMessage
	if err := json.Unmarshal(log.Message, &message); err != nil {
		return badRequestf(err, "cannot unmarshal the ingestion log message")
	}

	// Validate the provided level.
	level, ok := paramsLevels[log.Level]
	if !ok {
		return badRequestf(nil, "invalid ingestion log level")
	}

	// Add the ingestion log to the database.
	if err := h.store.AddIngestionLog(message, log.URLs, level); err != nil {
		return errgo.Notef(err, "cannot add the ingestion log")
	}
	return nil
}

// mongodocLevels maps internal mongodoc ingestion log levels to API ones.
var mongodocLevels = map[mongodoc.IngestionLogLevel]params.IngestionLogLevel{
	mongodoc.IngestionInfo:  params.IngestionInfo,
	mongodoc.IngestionError: params.IngestionError,
}

// paramsLevels maps API params log levels to internal mongodoc ones.
var paramsLevels = map[params.IngestionLogLevel]mongodoc.IngestionLogLevel{
	params.IngestionInfo:  mongodoc.IngestionInfo,
	params.IngestionError: mongodoc.IngestionError,
}

// getPositiveIntValue tries to fetch a positive numeric value corresponding to
// the given name from the given flags. It returns the provided default if the
// value is not set. An error is returned if the value is not valid.
func getPositiveIntValue(flags url.Values, name string, defaultValue int) (int, error) {
	if strValue := flags.Get(name); strValue != "" {
		value, err := strconv.Atoi(strValue)
		if err != nil || value <= 0 {
			return 0, errgo.New("invalid value for " + name)
		}
		return value, nil
	}
	return defaultValue, nil
}
