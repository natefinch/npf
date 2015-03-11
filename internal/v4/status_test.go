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
	"gopkg.in/juju/charm.v4"

	"gopkg.in/juju/charmstore.v4/internal/mongodoc"
	"gopkg.in/juju/charmstore.v4/params"
)

var zeroTimeStr = time.Time{}.Format(time.RFC3339)

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
	s.PatchValue(&debugstatus.StartTime, now)
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
	s.AssertDebugStatus(c, true, map[string]params.DebugStatus{
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
		"base_entities": {
			Name:   "Base entities in charm store",
			Value:  "count: 5",
			Passed: true,
		},
		"server_started": {
			Name:   "Server started",
			Value:  now.String(),
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
	})
}

func (s *APISuite) TestStatusWithElasticSearch(c *gc.C) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv_es,
		URL:     storeURL("debug/status"),
	})
	var results map[string]params.DebugStatus
	err := json.Unmarshal(rec.Body.Bytes(), &results)
	c.Assert(err, gc.IsNil)
	c.Assert(results["elasticsearch"].Name, gc.Equals, "Elastic search is running")
	c.Assert(results["elasticsearch"].Value, jc.Contains, "cluster_name:")
}

func (s *APISuite) TestStatusWithoutCorrectCollections(c *gc.C) {
	s.store.DB.Entities().DropCollection()
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"mongo_collections": {
			Name:   "MongoDB collections",
			Value:  "Missing collections: [" + s.store.DB.Entities().Name + "]",
			Passed: false,
		},
	})
}

func (s *APISuite) TestStatusWithoutIngestion(c *gc.C) {
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"ingestion": {
			Name:   "Ingestion",
			Value:  "started: " + zeroTimeStr + ", completed: " + zeroTimeStr,
			Passed: false,
		},
	})
}

func (s *APISuite) TestStatusIngestionStarted(c *gc.C) {
	now := time.Now()
	start := now.Add(-1 * time.Hour)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"ingestion started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  start,
	})
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"ingestion": {
			Name:   "Ingestion",
			Value:  "started: " + start.Format(time.RFC3339) + ", completed: " + zeroTimeStr,
			Passed: false,
		},
	})
}

func (s *APISuite) TestStatusWithoutLegacyStatistics(c *gc.C) {
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"legacy_statistics": {
			Name:   "Legacy Statistics Load",
			Value:  "started: " + zeroTimeStr + ", completed: " + zeroTimeStr,
			Passed: false,
		},
	})
}

func (s *APISuite) TestStatusLegacyStatisticsStarted(c *gc.C) {
	now := time.Now()
	statisticsStart := now.Add(-1*time.Hour - 30*time.Minute)
	s.addLog(c, &mongodoc.Log{
		Data:  []byte(`"legacy statistics import started"`),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.LegacyStatisticsType,
		Time:  statisticsStart,
	})
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"legacy_statistics": {
			Name:   "Legacy Statistics Load",
			Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + zeroTimeStr,
			Passed: false,
		},
	})
}

func (s *APISuite) TestStatusLegacyStatisticsMultipleLogs(c *gc.C) {
	now := time.Now()
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
	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"legacy_statistics": {
			Name:   "Legacy Statistics Load",
			Value:  "started: " + statisticsStart.Format(time.RFC3339) + ", completed: " + statisticsEnd.Format(time.RFC3339),
			Passed: true,
		},
	})
}

func (s *APISuite) TestStatusBaseEntitiesError(c *gc.C) {
	// Add a base entity without any corresponding entities.
	entity := &mongodoc.BaseEntity{
		URL:  charm.MustParseReference("django"),
		Name: "django",
	}
	err := s.store.DB.BaseEntities().Insert(entity)
	c.Assert(err, gc.IsNil)

	s.AssertDebugStatus(c, false, map[string]params.DebugStatus{
		"base_entities": {
			Name:   "Base entities in charm store",
			Value:  "count: 1",
			Passed: false,
		},
	})
}

// AssertDebugStatus asserts that the current /debug/status endpoint
// matches the given status, ignoring status duration.
// If complete is true, it fails if the results contain
// keys not mentioned in status.
func (s *APISuite) AssertDebugStatus(c *gc.C, complete bool, status map[string]params.DebugStatus) {
	rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
		Handler: s.srv,
		URL:     storeURL("debug/status"),
	})
	c.Assert(rec.Code, gc.Equals, http.StatusOK, gc.Commentf("body: %s", rec.Body.Bytes()))
	c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "application/json")
	var gotStatus map[string]params.DebugStatus
	err := json.Unmarshal(rec.Body.Bytes(), &gotStatus)
	c.Assert(err, gc.IsNil)
	for key, r := range gotStatus {
		if _, found := status[key]; !complete && !found {
			delete(gotStatus, key)
			continue
		}
		r.Duration = 0
		gotStatus[key] = r
	}
	c.Assert(gotStatus, jc.DeepEquals, status)
}
