// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/juju/utils/debugstatus"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2/bson"

	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/params"
)

// GET /debug/status
// https://github.com/juju/charmstore/blob/v4/docs/API.md#get-debugstatus
func (h *Handler) serveDebugStatus(_ http.Header, req *http.Request) (interface{}, error) {
	return debugstatus.Check(
		debugstatus.ServerStartTime,
		debugstatus.Connection(h.store.DB.Session),
		debugstatus.MongoCollections(h.store.DB),
		h.checkElasticSearch,
		h.checkEntities,
		h.checkBaseEntities,
		h.checkLogs(
			"ingestion", "Ingestion",
			mongodoc.IngestionType, params.IngestionStart, params.IngestionComplete),
		h.checkLogs(
			"legacy_statistics", "Legacy Statistics Load",
			mongodoc.LegacyStatisticsType, params.LegacyStatisticsImportStart, params.LegacyStatisticsImportComplete),
	), nil
}

func (h *Handler) checkElasticSearch() (key string, result debugstatus.CheckResult) {
	key = "elasticsearch"
	result.Name = "Elastic search is running"
	if h.store.ES == nil || h.store.ES.Database == nil {
		result.Value = "Elastic search is not configured"
		result.Passed = true
		return key, result
	}
	health, err := h.store.ES.Health()
	if err != nil {
		result.Value = "Connection issues to Elastic Search: " + err.Error()
		return key, result
	}
	result.Value = health.String()
	result.Passed = health.Status == "green"
	return key, result
}

func (h *Handler) checkEntities() (key string, result debugstatus.CheckResult) {
	result.Name = "Entities in charm store"
	charms, err := h.store.DB.Entities().Find(bson.D{{"series", bson.D{{"$ne", "bundle"}}}}).Count()
	if err != nil {
		result.Value = "Cannot count charms for consistency check: " + err.Error()
		return "entities", result
	}
	bundles, err := h.store.DB.Entities().Find(bson.D{{"series", "bundle"}}).Count()
	if err != nil {
		result.Value = "Cannot count bundles for consistency check: " + err.Error()
		return "entities", result
	}
	promulgated, err := h.store.DB.Entities().Find(bson.D{{"promulgated-url", bson.D{{"$exists", true}}}}).Count()
	if err != nil {
		result.Value = "Cannot count promulgated for consistency check: " + err.Error()
		return "entities", result
	}
	result.Value = fmt.Sprintf("%d charms; %d bundles; %d promulgated", charms, bundles, promulgated)
	result.Passed = true
	return "entities", result
}

func (h *Handler) checkBaseEntities() (key string, result debugstatus.CheckResult) {
	resultKey := "base_entities"
	result.Name = "Base entities in charm store"

	// Retrieve the number of base entities.
	baseNum, err := h.store.DB.BaseEntities().Count()
	if err != nil {
		result.Value = "Cannot count base entities: " + err.Error()
		return resultKey, result
	}

	// Retrieve the number of entities.
	num, err := h.store.DB.Entities().Count()
	if err != nil {
		result.Value = "Cannot count entities for consistency check: " + err.Error()
		return resultKey, result
	}

	result.Value = fmt.Sprintf("count: %d", baseNum)
	result.Passed = num >= baseNum
	return resultKey, result
}

func (h *Handler) checkLogs(resultKey, resultName string, logType mongodoc.LogType, startPrefix, endPrefix string) debugstatus.CheckerFunc {
	return func() (key string, result debugstatus.CheckResult) {
		result.Name = resultName
		start, end, err := h.findTimesInLogs(logType, startPrefix, endPrefix)
		if err != nil {
			result.Value = err.Error()
			return resultKey, result
		}
		result.Value = fmt.Sprintf("started: %s, completed: %s", start.Format(time.RFC3339), end.Format(time.RFC3339))
		result.Passed = !(start.IsZero() || end.IsZero())
		return resultKey, result
	}
}

// findTimesInLogs goes through logs in reverse order finding when the start and
// end messages were last added.
func (h *Handler) findTimesInLogs(logType mongodoc.LogType, startPrefix, endPrefix string) (start, end time.Time, err error) {
	var log mongodoc.Log
	iter := h.store.DB.Logs().
		Find(bson.D{
		{"level", mongodoc.InfoLevel},
		{"type", logType},
	}).Sort("-time", "-id").Iter()
	for iter.Next(&log) {
		var msg string
		if err := json.Unmarshal(log.Data, &msg); err != nil {
			// an error here probably means the log isn't in the form we are looking for.
			continue
		}
		if start.IsZero() && strings.HasPrefix(msg, startPrefix) {
			start = log.Time
		}
		if end.IsZero() && strings.HasPrefix(msg, endPrefix) {
			end = log.Time
		}
		if !start.IsZero() && !end.IsZero() {
			break
		}
	}
	if err = iter.Close(); err != nil {
		return time.Time{}, time.Time{}, errgo.Notef(err, "Cannot query logs")
	}
	return
}
