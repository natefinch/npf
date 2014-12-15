// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	"github.com/juju/utils/debugstatus"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/internal/mongodoc"
)

func assertStatusCall(c *gc.C, p httptesting.JSONCallParams) {
	// Perform the request.
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: p.Handler,
		URL:     p.URL,
	})

	// Ensure the request succeeded and returned a JSON response.
	c.Assert(rec.Code, gc.Equals, p.ExpectStatus)
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "application/json")

	// Check the response is valid JSON.
	var results map[string]debugstatus.CheckResult
	err := json.Unmarshal(rec.Body.Bytes(), &results)
	c.Assert(err, gc.IsNil)

	// Handle the server_started part of the response body.
	started := results["server_started"]
	c.Assert(started.Name, gc.Equals, "Server started")
	c.Assert(started.Value, gc.Not(gc.Equals), time.Time{})
	c.Assert(started.Passed, jc.IsTrue)
	delete(results, "server_started")

	// Ensure the remaining part of the response body is what we expected.
	c.Assert(results, jc.DeepEquals, p.ExpectBody)
}

func (s *APISuite) TestStatus(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  end,
	})
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := now.Add(-30 * time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd,
	})
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: true,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: true,
			},
		},
	})
}

func (s *APISuite) TestStatusWithElasticSearch(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv_es,
		URL:     storeURL("debug/status"),
	})
	var i map[string]debugstatus.CheckResult
	json.Unmarshal(rec.Body.Bytes(), &i)

	c.Assert(i["elasticsearch"].Name, gc.Equals, "Elastic search is running")
	c.Assert(i["elasticsearch"].Value, jc.Contains, "cluster_name:")
}

func (s *APISuite) TestStatusWithoutCorrectCollections(c *gc.C) {
	s.store.DB.Entities().DropCollection()
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  end,
	})
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := now.Add(-30 * time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd,
	})
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "Missing collections: [" + s.store.DB.Entities().Name + "]",
				Passed: false,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "0 charms; 0 bundles; 0 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: true,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: true,
			},
		},
	})
}

func (s *APISuite) TestStatusWithoutIngestion(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := time.Time{}
	end := time.Time{}
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := now.Add(-30 * time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd,
	})
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: false,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: true,
			},
		},
	})
}

func (s *APISuite) TestStatusIngestionStarted(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := time.Time{}
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := now.Add(-30 * time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd,
	})
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: false,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: true,
			},
		},
	})
}

func (s *APISuite) TestStatusWithoutLegacyStatistics(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  end,
	})
	statisticsStart := time.Time{}
	statisticsEnd := time.Time{}
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: true,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: false,
			},
		},
	})
}

func (s *APISuite) TestStatusLegacyStatisticsStarted(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  end,
	})
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := time.Time{}
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: true,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: false,
			},
		},
	})
}

func (s *APISuite) TestStatusLegacyStatisticsMultipleLogs(c *gc.C) {
	for _, id := range []string{
		"cs:precise/wordpress-2",
		"cs:precise/wordpress-3",
		"cs:~foo/precise/arble-9",
		"cs:~bar/utopic/arble-10",
		"cs:bundle/oflaughs-3",
		"cs:~bar/bundle/oflaughs-4",
	} {
		if strings.Contains(id, "bundle") {
			s.addBundle(c, "wordpress-simple", id)
		} else {
			s.addCharm(c, "wordpress", id)
		}
	}
	now := time.Now()
	start := now.Add(-2 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	end := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  end,
	})
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart.Add(-1 * time.Hour),
	})
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	statisticsEnd := now.Add(-30 * time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd.Add(-1 * time.Hour),
	})
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import completed"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsEnd,
	})
	assertStatusCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/status"),
		ExpectStatus: http.StatusOK,
		ExpectBody: map[string]debugstatus.CheckResult{
			"mongo_connected": {
				Name:   "MongoDB is connected",
				Value:  "Connected",
				Passed: true,
			},
			"mongo_collections": {
				Name:   "MongoDB collections",
				Value:  "All required collections exist",
				Passed: true,
			},
			"elasticsearch": {
				Name:   "Elastic search is running",
				Value:  "Elastic search is not configured",
				Passed: true,
			},
			"entities": {
				Name:   "Entities in charm store",
				Value:  "4 charms; 2 bundles; 3 promulgated",
				Passed: true,
			},
			"ingestion": {
				Name:   "Ingestion",
				Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + end.Format(time.RFC3339),
				Passed: true,
			},
			"legacy_statistics": {
				Name:   "Legacy Statistics Load",
				Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
				Passed: true,
			},
		},
	})
}
