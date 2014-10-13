// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

package storetesting

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/juju/utils"
	gc "gopkg.in/check.v1"

	"github.com/juju/charmstore/internal/elasticsearch"
)

var serverAddr string

// ElasticSearchTestPackage determines the address of the test elasticsearch
// server, and then calls the given function with t as its argument, or calls
// gocheck.TestingT if t is nil. Its behaviour is dependent on the value of the
// JUJU_TEST_ELASTICSEARCH environment variable, which can be "none" (do not
// start or connect to a server) or host:port holding the address and port of
// the server to connect to. If JUJU_TEST_ELASTICSEARCH is not specified then
// localhost:9200 will be used.
//
// For example:
//     JUJU_TEST_ELASTICSEARCH=localhost:9200 go test
func ElasticSearchTestPackage(t *testing.T, cb func(t *testing.T)) {
	esAddr := os.Getenv("JUJU_TEST_ELASTICSEARCH")
	switch esAddr {
	case "none":
		return
	case "":
		serverAddr = ":9200"
	default:
		serverAddr = esAddr
	}
	if cb != nil {
		cb(t)
	} else {
		gc.TestingT(t)
	}
}

type ElasticSearchSuite struct {
	ES        *elasticsearch.Database
	indexes   []string
	TestIndex string
}

func (s *ElasticSearchSuite) SetUpSuite(c *gc.C) {
	s.ES = &elasticsearch.Database{fmt.Sprintf(serverAddr)}
}

func (s *ElasticSearchSuite) TearDownSuite(c *gc.C) {
}

func (s *ElasticSearchSuite) SetUpTest(c *gc.C) {
	s.TestIndex = s.NewIndex(c)
}

func (s *ElasticSearchSuite) TearDownTest(c *gc.C) {
	for _, index := range s.indexes {
		s.ES.DeleteIndex(index)
	}
	s.indexes = nil
}

// NewIndex creates a new index name and ensures that it will be cleaned up at
// end of the test.
func (s *ElasticSearchSuite) NewIndex(c *gc.C) string {
	uuid, err := utils.NewUUID()
	c.Assert(err, gc.IsNil)
	id := time.Now().Format("20060102") + uuid.String() + "-"
	s.indexes = append(s.indexes, id)
	return id
}
