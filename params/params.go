// Copyright 2014 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// The params package holds types that are a part of the charm store's external
// contract - they will be marshalled (or unmarshalled) as JSON
// and delivered through the HTTP API.
package params

import (
	"encoding/json"
	"time"

	"gopkg.in/juju/charm.v4"
)

const (
	// ContentHashHeader specifies the header attribute
	// that will hold the content hash for archive GET responses.
	ContentHashHeader = "Content-Sha384"

	// EntityIdHeader specifies the header attribute that will hold the
	// id of the entity for archive GET responses.
	EntityIdHeader = "Entity-Id"
)

// MetaAnyResponse holds the result of a meta/any
// request. See http://tinyurl.com/q5vcjpk
type MetaAnyResponse struct {
	Id   *charm.Reference
	Meta map[string]interface{} `json:",omitempty"`
}

// ArchiveUploadResponse holds the result of
// a post or a put to /$id/archive. See http://tinyurl.com/lzrzrgb
type ArchiveUploadResponse struct {
	Id *charm.Reference
}

// ExpandedId holds a charm or bundle fully qualified id.
// A slice of ExpandedId is used as response for
// id/expand-id GET requests.
type ExpandedId struct {
	Id string
}

// ArchiveSizeResponse holds the result of an
// id/meta/archive-size GET request. See http://tinyurl.com/m8b9geq
type ArchiveSizeResponse struct {
	Size int64
}

// ManifestFile holds information about a charm or bundle file.
// A slice of ManifestFile is used as response for
// id/meta/manifest GET requests. See http://tinyurl.com/p3xdcto
type ManifestFile struct {
	Name string
	Size int64
}

// ArchiveUploadTimeResponse holds the result of an
// id/meta/archive-upload-time GET request. See http://tinyurl.com/nmujuqk
type ArchiveUploadTimeResponse struct {
	UploadTime time.Time
}

// RelatedResponse holds the result of an
// id/meta/charm-related GET request. See http://tinyurl.com/q7vdmzl
type RelatedResponse struct {
	// Requires holds an entry for each interface provided by
	// the charm, containing all charms that require that interface.
	Requires map[string][]MetaAnyResponse `json:",omitempty"`

	// Provides holds an entry for each interface required by the
	// the charm, containing all charms that provide that interface.
	Provides map[string][]MetaAnyResponse `json:",omitempty"`
}

// RevisionInfoResponse holds the result of an
// id/meta/revision-info GET request. See http://tinyurl.com/q6xos7f
type RevisionInfoResponse struct {
	Revisions []*charm.Reference
}

// BundleCount holds the result of an id/meta/bundle-unit-count
// or bundle-machine-count GET request. See http://tinyurl.com/mkvowub
// and http://tinyurl.com/qfuubrv
type BundleCount struct {
	Count int
}

// TagsResponse holds the result of an
// id/meta/tags GET request. See http://tinyurl.com/njyqwj2
type TagsResponse struct {
	Tags []string
}

type Published struct {
	Id          *charm.Reference
	PublishTime time.Time
}

// DebugStatus holds the result of the status checks
type DebugStatus struct {
	Name   string
	Value  string
	Passed bool
}

// SearchResult holds a single result from a search operation
type SearchResult struct {
	Id *charm.Reference
	// Meta holds at most one entry for each meta value
	// specified in the include flags, holding the
	// data that would be returned by reading /meta/meta?id=id.
	// Metadata not relevant to a particular result will not
	// be included.
	Meta map[string]interface{} `json:",omitempty"`
}

// SearchResponse holds the response from a search operation.
type SearchResponse struct {
	SearchTime time.Duration
	Total      int
	Results    []SearchResult
}

// IdUserResponse holds the result of an id/meta/id-user GET request.
// See http://tinyurl.com/o7xmhz2
type IdUserResponse struct {
	User string
}

// IdSeriesResponse holds the result of an id/meta/id-series GET request.
// See http://tinyurl.com/pnwmr6j
type IdSeriesResponse struct {
	Series string
}

// IdNameResponse holds the result of an id/meta/id-name GET request.
// See http://tinyurl.com/m5q8gcy
type IdNameResponse struct {
	Name string
}

// IdRevisionResponse holds the result of an id/meta/id-revision GET request.
// See http://tinyurl.com/ntd3coz
type IdRevisionResponse struct {
	Revision int
}

// BzrDigestKey is the extra-info key used to store the Bazaar digest
const BzrDigestKey = "bzr-digest"

// Log holds the representation of a log message.
// This is used by clients to store log events in the charm store.
type Log struct {
	// Data holds the log message as a JSON-encoded value.
	Data *json.RawMessage

	// Level holds the log level as a string.
	Level LogLevel

	// Type holds the log type as a string.
	Type LogType

	// URLs holds a slice of entity URLs associated with the log message.
	URLs []*charm.Reference `json:",omitempty"`
}

// LogResponse represents a single log message and is used in the responses
// to /log GET requests.
// See http://tinyurl.com/nj77rcr
type LogResponse struct {
	// Data holds the log message as a JSON-encoded value.
	Data json.RawMessage

	// Level holds the log level as a string.
	Level LogLevel

	// Type holds the log type as a string.
	Type LogType

	// URLs holds a slice of entity URLs associated with the log message.
	URLs []*charm.Reference `json:",omitempty"`

	// Time holds the time of the log.
	Time time.Time
}

// LogLevel defines log levels (e.g. "info" or "error") to be used in log
// requests and responses.
type LogLevel string

const (
	InfoLevel    LogLevel = "info"
	WarningLevel LogLevel = "warning"
	ErrorLevel   LogLevel = "error"
)

// LogType defines log types (e.g. "ingestion") to be used in log requests and
// responses.
type LogType string

const IngestionType LogType = "ingestion"
