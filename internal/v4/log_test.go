// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package v4_test // import "gopkg.in/juju/charmstore.v5-unstable/internal/v4"

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v6-unstable"
	"gopkg.in/juju/charmrepo.v1/csclient/params"

	"gopkg.in/juju/charmstore.v5-unstable/internal/mongodoc"
	"gopkg.in/juju/charmstore.v5-unstable/internal/v4"
)

type logSuite struct {
	commonSuite
}

var _ = gc.Suite(&logSuite{})

func (s *logSuite) SetUpSuite(c *gc.C) {
	s.enableIdentity = true
	s.commonSuite.SetUpSuite(c)
}

var logResponses = map[string]*params.LogResponse{
	"info1": {
		Data:  rawMessage("info data 1"),
		Level: params.InfoLevel,
		Type:  params.IngestionType,
		URLs:  nil,
	},
	"error1": {
		Data:  rawMessage("error data 1"),
		Level: params.ErrorLevel,
		Type:  params.IngestionType,
		URLs:  nil,
	},
	"info2": {
		Data:  rawMessage("info data 2"),
		Level: params.InfoLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("precise/django"),
			charm.MustParseReference("django"),
			charm.MustParseReference("rails"),
		},
	},
	"warning1": {
		Data:  rawMessage("warning data 1"),
		Level: params.WarningLevel,
		Type:  params.IngestionType,
		URLs:  nil,
	},
	"error2": {
		Data:  rawMessage("error data 2"),
		Level: params.ErrorLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("hadoop"),
		},
	},
	"info3": {
		Data:  rawMessage("info data 3"),
		Level: params.InfoLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("trusty/django"),
			charm.MustParseReference("django"),
			charm.MustParseReference("utopic/hadoop"),
			charm.MustParseReference("hadoop"),
		},
	},
	"error3": {
		Data:  rawMessage("error data 3"),
		Level: params.ErrorLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("utopic/hadoop"),
			charm.MustParseReference("hadoop"),
			charm.MustParseReference("precise/django"),
			charm.MustParseReference("django"),
		},
	},
	"stats": {
		Data:  rawMessage("statistics info data"),
		Level: params.InfoLevel,
		Type:  params.LegacyStatisticsType,
		URLs:  nil,
	},
}

var getLogsTests = []struct {
	about       string
	querystring string
	expectBody  []*params.LogResponse
}{{
	about: "retrieve logs",
	expectBody: []*params.LogResponse{
		logResponses["stats"],
		logResponses["error3"],
		logResponses["info3"],
		logResponses["error2"],
		logResponses["warning1"],
		logResponses["info2"],
		logResponses["error1"],
		logResponses["info1"],
	},
}, {
	about:       "use limit",
	querystring: "?limit=2",
	expectBody: []*params.LogResponse{
		logResponses["stats"],
		logResponses["error3"],
	},
}, {
	about:       "use offset",
	querystring: "?skip=3",
	expectBody: []*params.LogResponse{
		logResponses["error2"],
		logResponses["warning1"],
		logResponses["info2"],
		logResponses["error1"],
		logResponses["info1"],
	},
}, {
	about:       "zero offset",
	querystring: "?skip=0",
	expectBody: []*params.LogResponse{
		logResponses["stats"],
		logResponses["error3"],
		logResponses["info3"],
		logResponses["error2"],
		logResponses["warning1"],
		logResponses["info2"],
		logResponses["error1"],
		logResponses["info1"],
	},
}, {
	about:       "use both limit and offset",
	querystring: "?limit=3&skip=1",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
		logResponses["info3"],
		logResponses["error2"],
	},
}, {
	about:       "filter by level",
	querystring: "?level=info",
	expectBody: []*params.LogResponse{
		logResponses["stats"],
		logResponses["info3"],
		logResponses["info2"],
		logResponses["info1"],
	},
}, {
	about:       "filter by type",
	querystring: "?type=ingestion",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
		logResponses["info3"],
		logResponses["error2"],
		logResponses["warning1"],
		logResponses["info2"],
		logResponses["error1"],
		logResponses["info1"],
	},
}, {
	about:       "filter by level with a limit",
	querystring: "?level=error&limit=2",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
		logResponses["error2"],
	},
}, {
	about:       "filter by id",
	querystring: "?id=precise/django",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
		logResponses["info2"],
	},
}, {
	about:       "multiple query",
	querystring: "?id=utopic/hadoop&limit=1&level=error",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
	},
}, {
	about:       "empty response offset",
	querystring: "?id=utopic/hadoop&skip=10",
}, {
	about:       "empty response id not found",
	querystring: "?id=utopic/mysql",
}, {
	about:       "empty response level",
	querystring: "?id=trusty/rails&level=error",
}, {
	about:       "filter by type - legacyStatistics",
	querystring: "?type=legacyStatistics",
	expectBody: []*params.LogResponse{
		logResponses["stats"],
	},
}}

func (s *logSuite) TestGetLogs(c *gc.C) {
	// Add logs to the database.
	beforeAdding := time.Now().Add(-time.Second)
	for _, key := range []string{"info1", "error1", "info2", "warning1", "error2", "info3", "error3", "stats"} {
		resp := logResponses[key]
		err := s.store.AddLog(&resp.Data, v4.ParamsLogLevels[resp.Level], v4.ParamsLogTypes[resp.Type], resp.URLs)
		c.Assert(err, gc.IsNil)
	}
	afterAdding := time.Now().Add(time.Second)

	// Run the tests.
	for i, test := range getLogsTests {
		c.Logf("test %d: %s", i, test.about)
		rec := httptesting.DoRequest(c, httptesting.DoRequestParams{
			Handler:  s.srv,
			URL:      storeURL("log" + test.querystring),
			Username: testUsername,
			Password: testPassword,
		})

		// Ensure the response is what we expect.
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "application/json")

		// Decode the response.
		var logs []*params.LogResponse
		decoder := json.NewDecoder(rec.Body)
		err := decoder.Decode(&logs)
		c.Assert(err, gc.IsNil)

		// Check and then reset the response time so that the whole body
		// can be more easily compared later.
		for _, log := range logs {
			c.Assert(log.Time, jc.TimeBetween(beforeAdding, afterAdding))
			log.Time = time.Time{}
		}

		// Ensure the response includes the expected logs.
		c.Assert(logs, jc.DeepEquals, test.expectBody)
	}
}

func rawMessage(msg string) json.RawMessage {
	message, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(message)
}

var getLogsErrorsTests = []struct {
	about         string
	querystring   string
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "invalid limit (negative number)",
	querystring:   "?limit=-100",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid limit value: value must be >= 1",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid limit (zero value)",
	querystring:   "?limit=0",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid limit value: value must be >= 1",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid limit (not a number)",
	querystring:   "?limit=foo",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid limit value: value must be a number",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid offset (negative number)",
	querystring:   "?skip=-100",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid skip value: value must be >= 0",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid offset (not a number)",
	querystring:   "?skip=bar",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid skip value: value must be a number",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid id",
	querystring:   "?id=no-such:reference",
	expectStatus:  http.StatusBadRequest,
	expectMessage: `invalid id value: charm URL has invalid schema: "no-such:reference"`,
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log level",
	querystring:   "?level=bar",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log level value",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log type",
	querystring:   "?type=no-such",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log type value",
	expectCode:    params.ErrBadRequest,
}}

func (s *logSuite) TestGetLogsErrors(c *gc.C) {
	for i, test := range getLogsErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("log" + test.querystring),
			Username:     testUsername,
			Password:     testPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *logSuite) TestGetLogsErrorInvalidLog(c *gc.C) {
	// Add a non-parsable log message to the db directly.
	err := s.store.DB.Logs().Insert(mongodoc.Log{
		Data:  []byte("!"),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  time.Now(),
	})
	c.Assert(err, gc.IsNil)
	// The log is just ignored.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("log"),
		Username:     testUsername,
		Password:     testPassword,
		ExpectStatus: http.StatusOK,
		ExpectBody:   []params.LogResponse{},
	})
}

func (s *logSuite) TestPostLogs(c *gc.C) {
	// Prepare the request body.
	body := makeByteLogs(rawMessage("info data"), params.InfoLevel, params.IngestionType, []*charm.Reference{
		charm.MustParseReference("trusty/django"),
		charm.MustParseReference("utopic/rails"),
	})

	// Send the request.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:  s.srv,
		URL:      storeURL("log"),
		Method:   "POST",
		Username: testUsername,
		Password: testPassword,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:         bytes.NewReader(body),
		ExpectStatus: http.StatusOK,
	})

	// Ensure the log message has been added to the database.
	var doc mongodoc.Log
	err := s.store.DB.Logs().Find(nil).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(doc.Data), gc.Equals, `"info data"`)
	c.Assert(doc.Level, gc.Equals, mongodoc.InfoLevel)
	c.Assert(doc.Type, gc.Equals, mongodoc.IngestionType)
	c.Assert(doc.URLs, jc.DeepEquals, []*charm.Reference{
		charm.MustParseReference("trusty/django"),
		charm.MustParseReference("django"),
		charm.MustParseReference("utopic/rails"),
		charm.MustParseReference("rails"),
	})
}

func (s *logSuite) TestPostLogsMultipleEntries(c *gc.C) {
	// Prepare the request body.
	infoData := rawMessage("info data")
	warningData := rawMessage("warning data")
	logs := []params.Log{{
		Data:  &infoData,
		Level: params.InfoLevel,
		Type:  params.IngestionType,
	}, {
		Data:  &warningData,
		Level: params.WarningLevel,
		Type:  params.IngestionType,
	}}
	body, err := json.Marshal(logs)
	c.Assert(err, gc.IsNil)

	// Send the request.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler:  s.srv,
		URL:      storeURL("log"),
		Method:   "POST",
		Username: testUsername,
		Password: testPassword,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body:         bytes.NewReader(body),
		ExpectStatus: http.StatusOK,
	})

	// Ensure the log messages has been added to the database.
	var docs []mongodoc.Log
	err = s.store.DB.Logs().Find(nil).Sort("id").All(&docs)
	c.Assert(err, gc.IsNil)
	c.Assert(docs, gc.HasLen, 2)
	c.Assert(string(docs[0].Data), gc.Equals, string(infoData))
	c.Assert(docs[0].Level, gc.Equals, mongodoc.InfoLevel)
	c.Assert(string(docs[1].Data), gc.Equals, string(warningData))
	c.Assert(docs[1].Level, gc.Equals, mongodoc.WarningLevel)
}

var postLogsErrorsTests = []struct {
	about         string
	contentType   string
	body          []byte
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "invalid content type",
	contentType:   "application/zip",
	expectStatus:  http.StatusBadRequest,
	expectMessage: `unexpected Content-Type "application/zip"; expected 'application/json'`,
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid body",
	body:          []byte("!"),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "cannot unmarshal body: invalid character '!' looking for beginning of value",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log level",
	body:          makeByteLogs(rawMessage("message"), params.LogLevel(42), params.IngestionType, nil),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log level",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log type",
	body:          makeByteLogs(rawMessage("message"), params.WarningLevel, params.LogType(42), nil),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log type",
	expectCode:    params.ErrBadRequest,
}}

func (s *logSuite) TestPostLogsErrors(c *gc.C) {
	url := storeURL("log")
	for i, test := range postLogsErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		if test.contentType == "" {
			test.contentType = "application/json"
		}
		httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
			Handler: s.srv,
			URL:     url,
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {test.contentType},
			},
			Body:         bytes.NewReader(test.body),
			Username:     testUsername,
			Password:     testPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *logSuite) TestGetLogsUnauthorizedError(c *gc.C) {
	s.AssertEndpointAuth(c, httptesting.JSONCallParams{
		URL:          storeURL("log"),
		ExpectStatus: http.StatusOK,
		ExpectBody:   []params.LogResponse{},
	})
}

func (s *logSuite) TestPostLogsUnauthorizedError(c *gc.C) {
	// Add a non-parsable log message to the db.
	httptesting.AssertJSONCall(c, httptesting.JSONCallParams{
		Handler: s.noMacaroonSrv,
		URL:     storeURL("log"),
		Method:  "POST",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func makeByteLogs(data json.RawMessage, logLevel params.LogLevel, logType params.LogType, urls []*charm.Reference) []byte {
	logs := []params.Log{{
		Data:  &data,
		Level: logLevel,
		Type:  logType,
		URLs:  urls,
	}}
	b, err := json.Marshal(logs)
	if err != nil {
		panic(err)
	}
	return b
}
