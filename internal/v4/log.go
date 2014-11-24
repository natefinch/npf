// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/json"
	"net/http"
	"strconv"

	"gopkg.in/errgo.v1"
	"gopkg.in/juju/charm.v4"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET /log
// http://tinyurl.com/nj77rcr
//
// POST /log
// http://tinyurl.com/o27hqxe
func (h *Handler) serveLog(w http.ResponseWriter, req *http.Request) error {
	if err := h.authenticate(w, req); err != nil {
		return err
	}
	switch req.Method {
	case "GET":
		return h.getLogs(w, req)
	case "POST":
		return h.postLog(w, req)
	}
	return errgo.WithCausef(nil, params.ErrMethodNotAllowed, "%s method not allowed", req.Method)
}

func (h *Handler) getLogs(w http.ResponseWriter, req *http.Request) error {
	w.Header().Set("content-type", "application/json")
	encoder := json.NewEncoder(w)

	// Retrieve values from the query string.
	limit, err := intValue(req.Form.Get("limit"), 1, 1000)
	if err != nil {
		return badRequestf(err, "invalid limit value")
	}
	offset, err := intValue(req.Form.Get("offset"), 0, 0)
	if err != nil {
		return badRequestf(err, "invalid offset value")
	}
	id := req.Form.Get("id")
	strLevel := req.Form.Get("level")
	strType := req.Form.Get("type")

	// Build the Mongo query.
	query := make(bson.D, 0, 3)
	if id != "" {
		url, err := charm.ParseReference(id)
		if err != nil {
			return badRequestf(err, "invalid id value")
		}
		query = append(query, bson.DocElem{"urls", url})
	}
	if strLevel != "" {
		logLevel, ok := paramsLogLevels[params.LogLevel(strLevel)]
		if !ok {
			return badRequestf(nil, "invalid log level value")
		}
		query = append(query, bson.DocElem{"level", logLevel})
	}
	if strType != "" {
		logType, ok := paramsLogTypes[params.LogType(strType)]
		if !ok {
			return badRequestf(nil, "invalid log type value")
		}
		query = append(query, bson.DocElem{"type", logType})
	}

	// Retrieve the logs.
	var log mongodoc.Log
	iter := h.store.DB.Logs().Find(query).Sort("-_id").Skip(offset).Limit(limit).Iter()
	for iter.Next(&log) {
		var data json.RawMessage
		if err := json.Unmarshal(log.Data, &data); err != nil {
			return errgo.Notef(err, "cannot unmarshal log data")
		}
		logResponse := &params.LogResponse{
			Data:  data,
			Level: mongodocLogLevels[log.Level],
			Type:  mongodocLogTypes[log.Type],
			URLs:  log.URLs,
			Time:  log.Time.UTC(),
		}
		if err := encoder.Encode(logResponse); err != nil {
			return errgo.Notef(err, "cannot marshal log")
		}
	}
	if err := iter.Close(); err != nil {
		return errgo.Notef(err, "cannot retrieve logs")
	}
	return nil
}

func (h *Handler) postLog(w http.ResponseWriter, req *http.Request) error {
	// Check the request content type.
	if ctype := req.Header.Get("Content-Type"); ctype != "application/json" {
		return badRequestf(nil, "unexpected Content-Type %q; expected 'application/json'", ctype)
	}

	// Unmarshal the request body.
	var log params.Log
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(&log); err != nil {
		return badRequestf(err, "cannot unmarshal body")
	}

	// Validate the provided level and type.
	logLevel, ok := paramsLogLevels[log.Level]
	if !ok {
		return badRequestf(nil, "invalid log level")
	}
	logType, ok := paramsLogTypes[log.Type]
	if !ok {
		return badRequestf(nil, "invalid log type")
	}

	// Add the log to the database.
	if err := h.store.AddLog(log.Data, logLevel, logType, log.URLs); err != nil {
		return errgo.Notef(err, "cannot add log")
	}
	return nil
}

var (
	// mongodocLogLevels maps internal mongodoc log levels to API ones.
	mongodocLogLevels = map[mongodoc.LogLevel]params.LogLevel{
		mongodoc.InfoLevel:    params.InfoLevel,
		mongodoc.WarningLevel: params.WarningLevel,
		mongodoc.ErrorLevel:   params.ErrorLevel,
	}
	// paramsLogLevels maps API params log levels to internal mongodoc ones.
	paramsLogLevels = map[params.LogLevel]mongodoc.LogLevel{
		params.InfoLevel:    mongodoc.InfoLevel,
		params.WarningLevel: mongodoc.WarningLevel,
		params.ErrorLevel:   mongodoc.ErrorLevel,
	}
	// mongodocLogTypes maps internal mongodoc log types to API ones.
	mongodocLogTypes = map[mongodoc.LogType]params.LogType{
		mongodoc.IngestionType: params.IngestionType,
	}
	// paramsLogTypes maps API params log types to internal mongodoc ones.
	paramsLogTypes = map[params.LogType]mongodoc.LogType{
		params.IngestionType: mongodoc.IngestionType,
	}
)

// intValue checks that the given string value is a number greater than the
// given minValue. If the provided value is an empty string, the defaultValue
// is returned without errors.
func intValue(strValue string, minValue, defaultValue int) (int, error) {
	if strValue == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(strValue)
	if err != nil {
		return 0, errgo.New("value must be a number")
	}
	if value < minValue {
		return 0, errgo.Newf("value must be >= %d", minValue)
	}
	return value, nil
}
