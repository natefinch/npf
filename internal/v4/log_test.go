// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package v4_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/charm.v4"

	"github.com/juju/charmstore/internal/charmstore"
	"github.com/juju/charmstore/internal/mongodoc"
	"github.com/juju/charmstore/internal/storetesting"
	"github.com/juju/charmstore/internal/v4"
	"github.com/juju/charmstore/params"
)

type logSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&logSuite{})

func (s *logSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, nil, serverParams)
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
			charm.MustParseReference("trusty/rails"),
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
		URLs:  nil,
	},
	"info3": {
		Data:  rawMessage("info data 3"),
		Level: params.InfoLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("trusty/django"),
			charm.MustParseReference("utopic/hadoop"),
		},
	},
	"error3": {
		Data:  rawMessage("error data 3"),
		Level: params.ErrorLevel,
		Type:  params.IngestionType,
		URLs: []*charm.Reference{
			charm.MustParseReference("utopic/hadoop"),
			charm.MustParseReference("precise/django"),
		},
	},
}

var getLogsTests = []struct {
	about       string
	querystring string
	expectBody  []*params.LogResponse
}{{
	about: "retrieve logs",
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
	about:       "use limit",
	querystring: "?limit=2",
	expectBody: []*params.LogResponse{
		logResponses["error3"],
		logResponses["info3"],
	},
}, {
	about:       "use offset",
	querystring: "?offset=3",
	expectBody: []*params.LogResponse{
		logResponses["warning1"],
		logResponses["info2"],
		logResponses["error1"],
		logResponses["info1"],
	},
}, {
	about:       "zero offset",
	querystring: "?offset=0",
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
	about:       "use both limit and offset",
	querystring: "?limit=3&offset=1",
	expectBody: []*params.LogResponse{
		logResponses["info3"],
		logResponses["error2"],
		logResponses["warning1"],
	},
}, {
	about:       "filter by level",
	querystring: "?level=info",
	expectBody: []*params.LogResponse{
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
	querystring: "?id=utopic/hadoop&offset=10",
}, {
	about:       "empty response id not found",
	querystring: "?id=utopic/mysql",
}, {
	about:       "empty response level",
	querystring: "?id=trusty/rails&level=error",
}}

func (s *logSuite) TestGetLogs(c *gc.C) {
	// Add logs to the database.
	beforeAdding := time.Now().Add(-time.Second)
	for _, key := range []string{"info1", "error1", "info2", "warning1", "error2", "info3", "error3"} {
		resp := logResponses[key]
		err := s.store.AddLog(&resp.Data, v4.ParamsLogLevels[resp.Level], v4.ParamsLogTypes[resp.Type], resp.URLs)
		c.Assert(err, gc.IsNil)
	}
	afterAdding := time.Now().Add(time.Second)

	// Run the tests.
	for i, test := range getLogsTests {
		c.Logf("test %d: %s", i, test.about)
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler:  s.srv,
			URL:      storeURL("log" + test.querystring),
			Username: serverParams.AuthUsername,
			Password: serverParams.AuthPassword,
		})

		// Ensure the response is what we expect.
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "application/json")
		// Decode the response stream.
		decoder := json.NewDecoder(rec.Body)
		var responses []*params.LogResponse
		for {
			var response params.LogResponse
			err := decoder.Decode(&response)
			if err == io.EOF {
				break
			}
			c.Assert(err, gc.IsNil)
			// Check and then reset the response time so that the whole body
			// can be more easily compared later.
			c.Assert(response.Time, jc.TimeBetween(beforeAdding, afterAdding))
			response.Time = time.Time{}
			responses = append(responses, &response)
		}
		c.Assert(responses, jc.DeepEquals, test.expectBody)
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
	querystring:   "?offset=-100",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid offset value: value must be >= 0",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid offset (not a number)",
	querystring:   "?offset=bar",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid offset value: value must be a number",
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
}, {
	about:         "invalid log message",
	expectStatus:  http.StatusInternalServerError,
	expectMessage: "cannot unmarshal log data: invalid character '!' looking for beginning of value",
}}

func (s *logSuite) TestGetLogsErrors(c *gc.C) {
	// Add a non-parsable log message to the db.
	err := s.store.DB.Logs().Insert(mongodoc.Log{
		Data:  []byte("!"),
		Level: mongodoc.InfoLevel,
		Type:  mongodoc.IngestionType,
		Time:  time.Now(),
	})
	c.Assert(err, gc.IsNil)
	for i, test := range getLogsErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("log" + test.querystring),
			Username:     serverParams.AuthUsername,
			Password:     serverParams.AuthPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *logSuite) TestGetLogsUnauthorizedError(c *gc.C) {
	// Add a non-parsable log message to the db.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("log"),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: invalid or missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func (s *logSuite) TestPostLog(c *gc.C) {
	// Prepare the request body.
	urls := []*charm.Reference{
		charm.MustParseReference("trusty/django"),
		charm.MustParseReference("utopic/rails"),
	}
	body := makeByteLog(rawMessage("info message"), params.InfoLevel, params.IngestionType, urls)

	// Send the request.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:  s.srv,
		URL:      storeURL("log"),
		Method:   "POST",
		Username: serverParams.AuthUsername,
		Password: serverParams.AuthPassword,
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
	c.Assert(string(doc.Data), gc.Equals, `"info message"`)
	c.Assert(doc.Level, gc.Equals, mongodoc.InfoLevel)
	c.Assert(doc.Type, gc.Equals, mongodoc.IngestionType)
	c.Assert(doc.URLs, jc.DeepEquals, urls)
}

var postLogErrorsTests = []struct {
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
	body:          makeByteLog(rawMessage("message"), params.LogLevel(42), params.IngestionType, nil),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log level",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log type",
	body:          makeByteLog(rawMessage("message"), params.WarningLevel, params.LogType(42), nil),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid log type",
	expectCode:    params.ErrBadRequest,
}}

func (s *logSuite) TestPostLogErrors(c *gc.C) {
	url := storeURL("log")
	for i, test := range postLogErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		if test.contentType == "" {
			test.contentType = "application/json"
		}
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler: s.srv,
			URL:     url,
			Method:  "POST",
			Header: http.Header{
				"Content-Type": {test.contentType},
			},
			Body:         bytes.NewReader(test.body),
			Username:     serverParams.AuthUsername,
			Password:     serverParams.AuthPassword,
			ExpectStatus: test.expectStatus,
			ExpectBody: params.Error{
				Message: test.expectMessage,
				Code:    test.expectCode,
			},
		})
	}
}

func (s *logSuite) TestPostLogUnauthorizedError(c *gc.C) {
	// Add a non-parsable log message to the db.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler: s.srv,
		URL:     storeURL("log"),
		Method:  "POST",
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: invalid or missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func makeByteLog(data json.RawMessage, logLevel params.LogLevel, logType params.LogType, urls []*charm.Reference) []byte {
	log := &params.Log{
		Data:  &data,
		Level: logLevel,
		Type:  logType,
		URLs:  urls,
	}
	b, err := json.Marshal(log)
	if err != nil {
		panic(err)
	}
	return b
}
