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

type ingestionSuite struct {
	storetesting.IsolatedMgoSuite
	srv   http.Handler
	store *charmstore.Store
}

var _ = gc.Suite(&ingestionSuite{})

func (s *ingestionSuite) SetUpTest(c *gc.C) {
	s.IsolatedMgoSuite.SetUpTest(c)
	s.srv, s.store = newServer(c, s.Session, nil, serverParams)
}

func (s *ingestionSuite) addLog(c *gc.C, message json.RawMessage, ids []string, level mongodoc.IngestionLogLevel) {
	urls := make([]*charm.Reference, len(ids))
	for i, id := range ids {
		urls[i] = charm.MustParseReference(id)
	}
	err := s.store.AddIngestionLog(message, urls, level)
	c.Assert(err, gc.IsNil)
}

var ingestionLogResponses = map[string]*params.IngestionLogResponse{
	"info1": {
		Message: rawMessage("info message 1"),
		URLs:    nil,
		Level:   params.IngestionInfo,
	},
	"error1": {
		Message: rawMessage("error message 1"),
		URLs:    nil,
		Level:   params.IngestionError,
	},
	"info2": {
		Message: rawMessage("info message 2"),
		URLs: []*charm.Reference{
			charm.MustParseReference("precise/django"),
			charm.MustParseReference("trusty/rails"),
		},
		Level: params.IngestionInfo,
	},
	"error2": {
		Message: rawMessage("error message 2"),
		URLs:    nil,
		Level:   params.IngestionError,
	},
	"info3": {
		Message: rawMessage("info message 3"),
		URLs: []*charm.Reference{
			charm.MustParseReference("trusty/django"),
			charm.MustParseReference("utopic/hadoop"),
		},
		Level: params.IngestionInfo,
	},
	"error3": {
		Message: rawMessage("error message 3"),
		URLs: []*charm.Reference{
			charm.MustParseReference("utopic/hadoop"),
			charm.MustParseReference("precise/django"),
		},
		Level: params.IngestionError,
	},
}

var getIngestionLogsTests = []struct {
	about       string
	querystring string
	expectBody  []*params.IngestionLogResponse
}{{
	about: "retrieve logs",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["error3"],
		ingestionLogResponses["info3"],
		ingestionLogResponses["error2"],
		ingestionLogResponses["info2"],
		ingestionLogResponses["error1"],
		ingestionLogResponses["info1"],
	},
}, {
	about:       "use limit",
	querystring: "?limit=2",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["error3"],
		ingestionLogResponses["info3"],
	},
}, {
	about:       "use offset",
	querystring: "?offset=3",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["info2"],
		ingestionLogResponses["error1"],
		ingestionLogResponses["info1"],
	},
}, {
	about:       "use both limit and offset",
	querystring: "?limit=3&offset=1",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["info3"],
		ingestionLogResponses["error2"],
		ingestionLogResponses["info2"],
	},
}, {
	about:       "filter by level",
	querystring: "?level=info",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["info3"],
		ingestionLogResponses["info2"],
		ingestionLogResponses["info1"],
	},
}, {
	about:       "filter by level with a limit",
	querystring: "?level=error&limit=2",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["error3"],
		ingestionLogResponses["error2"],
	},
}, {
	about:       "filter by id",
	querystring: "?id=precise/django",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["error3"],
		ingestionLogResponses["info2"],
	},
}, {
	about:       "multiple query",
	querystring: "?id=utopic/hadoop&limit=1&level=error",
	expectBody: []*params.IngestionLogResponse{
		ingestionLogResponses["error3"],
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

func (s *ingestionSuite) TestGetIngestionLogs(c *gc.C) {
	// Add ingestion logs to the database.
	beforeAdding := time.Now().Add(-time.Second)
	for _, key := range []string{"info1", "error1", "info2", "error2", "info3", "error3"} {
		resp := ingestionLogResponses[key]
		s.store.AddIngestionLog(resp.Message, resp.URLs, v4.ParamsLevels[resp.Level])
	}
	afterAdding := time.Now().Add(time.Second)

	// Run the tests.
	for i, test := range getIngestionLogsTests {
		c.Logf("test %d: %s", i, test.about)
		rec := storetesting.DoRequest(c, storetesting.DoRequestParams{
			Handler:  s.srv,
			URL:      storeURL("debug/ingestion" + test.querystring),
			Username: serverParams.AuthUsername,
			Password: serverParams.AuthPassword,
		})

		// Ensure the response is what we expect.
		c.Assert(rec.Code, gc.Equals, http.StatusOK)
		c.Assert(rec.Header().Get("Content-Type"), gc.Equals, "application/json")
		// Decode the response stream.
		decoder := json.NewDecoder(rec.Body)
		var responses []*params.IngestionLogResponse
		for {
			var response params.IngestionLogResponse
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

var getIngestionLogsErrorsTests = []struct {
	about         string
	querystring   string
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "invalid limit (negative number)",
	querystring:   "?limit=-100",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid query: invalid value for limit",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid limit (not a number)",
	querystring:   "?limit=foo",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid query: invalid value for limit",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid offset (negative number)",
	querystring:   "?offset=-100",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid query: invalid value for offset",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid offset (not a number)",
	querystring:   "?offset=bar",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid query: invalid value for offset",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid id",
	querystring:   "?id=no-such:reference",
	expectStatus:  http.StatusBadRequest,
	expectMessage: `invalid query: cannot parse id: charm URL has invalid schema: "no-such:reference"`,
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log level",
	querystring:   "?level=bar",
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid query: invalid log level",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log message",
	expectStatus:  http.StatusInternalServerError,
	expectMessage: "cannot unmarshal the log message: invalid character '!' looking for beginning of value",
}}

func (s *ingestionSuite) TestGetIngestionLogsErrors(c *gc.C) {
	// Add a non-parsable log message to the db.
	s.store.AddIngestionLog([]byte("!"), nil, 0)
	for i, test := range getIngestionLogsErrorsTests {
		c.Logf("test %d: %s", i, test.about)
		storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
			Handler:      s.srv,
			URL:          storeURL("debug/ingestion" + test.querystring),
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

func (s *ingestionSuite) TestGetIngestionLogsUnauthorizedError(c *gc.C) {
	// Add a non-parsable log message to the db.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/ingestion"),
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: invalid or missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func (s *ingestionSuite) TestPostIngestionLog(c *gc.C) {
	// Prepare the request body.
	urls := []*charm.Reference{
		charm.MustParseReference("trusty/django"),
		charm.MustParseReference("utopic/rails"),
	}
	body := makeByteLog(rawMessage("info message"), urls, params.IngestionInfo)

	// Send the request.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:  s.srv,
		URL:      storeURL("debug/ingestion"),
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
	var doc mongodoc.IngestionLog
	err := s.store.DB.IngestionLogs().Find(nil).One(&doc)
	c.Assert(err, gc.IsNil)
	c.Assert(string(doc.Message), gc.Equals, `"info message"`)
	c.Assert(doc.Level, gc.Equals, mongodoc.IngestionInfo)
	c.Assert(doc.URLs, jc.DeepEquals, urls)
}

var postIngestionLogErrorsTests = []struct {
	about         string
	contentType   string
	body          []byte
	expectStatus  int
	expectMessage string
	expectCode    params.ErrorCode
}{{
	about:         "no content type",
	expectStatus:  http.StatusBadRequest,
	expectMessage: `unexpected Content-Type ""; expected 'application/json'`,
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid content type",
	contentType:   "application/zip",
	expectStatus:  http.StatusBadRequest,
	expectMessage: `unexpected Content-Type "application/zip"; expected 'application/json'`,
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid body",
	contentType:   "application/json",
	body:          []byte("!"),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "cannot unmarshal body: invalid character '!' looking for beginning of value",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log message",
	contentType:   "application/json",
	body:          makeByteLog([]byte("!"), nil, params.IngestionInfo),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "cannot unmarshal the ingestion log message: invalid character '!' looking for beginning of value",
	expectCode:    params.ErrBadRequest,
}, {
	about:         "invalid log level",
	contentType:   "application/json",
	body:          makeByteLog(rawMessage("message"), nil, params.IngestionLogLevel(42)),
	expectStatus:  http.StatusBadRequest,
	expectMessage: "invalid ingestion log level",
	expectCode:    params.ErrBadRequest,
}}

func (s *ingestionSuite) TestPostIngestionLogErrors(c *gc.C) {
	url := storeURL("debug/ingestion")
	for i, test := range postIngestionLogErrorsTests {
		c.Logf("test %d: %s", i, test.about)
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

func (s *ingestionSuite) TestPostIngestionLogUnauthorizedError(c *gc.C) {
	// Add a non-parsable log message to the db.
	storetesting.AssertJSONCall(c, storetesting.JSONCallParams{
		Handler:      s.srv,
		URL:          storeURL("debug/ingestion"),
		Method:       "POST",
		ExpectStatus: http.StatusUnauthorized,
		ExpectBody: params.Error{
			Message: "authentication failed: invalid or missing HTTP auth header",
			Code:    params.ErrUnauthorized,
		},
	})
}

func makeByteLog(message json.RawMessage, urls []*charm.Reference, level params.IngestionLogLevel) []byte {
	log := &params.IngestionLog{
		Message: message,
		Level:   level,
		URLs: []*charm.Reference{
			charm.MustParseReference("trusty/django"),
			charm.MustParseReference("utopic/rails"),
		},
	}
	b, err := json.Marshal(log)
	if err != nil {
		panic(err)
	}
	return b
}
