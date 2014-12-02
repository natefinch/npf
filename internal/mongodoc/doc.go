// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package mongodoc

import (
	"time"

	"gopkg.in/juju/charm.v4"
)

// Entity holds the in-database representation of charm or bundle's
// document in the charms collection.
type Entity struct {
	// URL holds the fully specified URL of the charm or bundle.
	// e.g. cs:precise/wordpress-34, cs:~user/trusty/foo-2
	URL *charm.Reference `bson:"_id"`

	// BaseURL holds the reference URL of the charm or bundle
	// (this omits the series and revision from URL)
	// e.g. cs:wordpress, cs:~user/foo
	BaseURL *charm.Reference

	// User holds the user part of the entity URL (for instance, "joe").
	User string

	// Name holds the name of the entity (for instance "wordpress").
	Name string

	// Revision holds the entity revision (it cannot be -1/unset).
	Revision int

	// Series holds the entity series (for instance "trusty" or "bundle").
	Series string

	// BlobHash holds the hash checksum of the blob, in hexadecimal format,
	// as created by blobstore.NewHash.
	BlobHash string

	// BlobHash256 holds the SHA256 hash checksum of the blob,
	// in hexadecimal format. This is only used by the legacy
	// API, and is calculated lazily the first time it is required.
	BlobHash256 string

	// Size holds the size of the archive blob.
	// TODO(rog) rename this to BlobSize.
	Size int64

	// BlobName holds the name that the archive blob is given in the blob store.
	BlobName string

	UploadTime time.Time

	// ExtraInfo holds arbitrary extra metadata associated with
	// the entity. The byte slices hold JSON-encoded data.
	ExtraInfo map[string][]byte `bson:",omitempty" json:",omitempty"`

	// TODO(rog) verify that all these types marshal to the expected
	// JSON form.
	CharmMeta    *charm.Meta
	CharmConfig  *charm.Config
	CharmActions *charm.Actions

	// CharmProvidedInterfaces holds all the relation
	// interfaces provided by the charm
	CharmProvidedInterfaces []string

	// CharmRequiredInterfaces is similar to CharmProvidedInterfaces
	// for required interfaces.
	CharmRequiredInterfaces []string

	BundleData   *charm.BundleData
	BundleReadMe string

	// BundleCharms includes all the charm URLs referenced
	// by the bundle, including base URLs where they are
	// not already included.
	BundleCharms []*charm.Reference

	// BundleMachineCount counts the machines used or created
	// by the bundle. It is nil for charms.
	BundleMachineCount *int

	// BundleUnitCount counts the units created by the bundle.
	// It is nil for charms.
	BundleUnitCount *int

	// TODO Add fields denormalized for search purposes
	// and search ranking field(s).

	// Contents holds entries for frequently accessed
	// entries in the file's blob. Storing this avoids
	// the need to linearly read the zip file's manifest
	// every time we access one of these files.
	Contents map[FileId]ZipFile `json:",omitempty" bson:",omitempty"`
}

type FileId string

const (
	FileReadMe FileId = "readme"
	FileIcon   FileId = "icon"
)

// ZipFile refers to a specific file in the uploaded archive blob.
type ZipFile struct {
	// Compressed specifies whether the file is compressed or not.
	Compressed bool

	// Offset holds the offset into the zip archive of the start of
	// the file's data.
	Offset int64

	// Size holds the size of the file before decompression.
	Size int64
}

// Valid reports whether f is a valid (non-zero) reference to
// a zip file.
func (f ZipFile) IsValid() bool {
	// Note that no valid zip files can start at offset zero,
	// because that's where the zip header lives.
	return f != ZipFile{}
}

// Log holds the in-database representation of a log message sent to the charm
// store.
type Log struct {
	// Data holds the JSON-encoded log message.
	Data []byte

	// Level holds the log level: whether the log is a warning, an error, etc.
	Level LogLevel

	// Type holds the log type.
	Type LogType

	// URLs holds a slice of entity URLs associated with the log message.
	URLs []*charm.Reference

	// Time holds the time of the log.
	Time time.Time
}

// LogLevel holds the level associated with a log.
type LogLevel int

// When introducing a new log level, do the following:
// 1) add the new level as a constant below;
// 2) add the new level in params as a string for HTTP requests/responses;
// 3) include the new level in the mongodocLogLevels and paramsLogLevels maps
//    in internal/v4.
const (
	_ LogLevel = iota
	InfoLevel
	WarningLevel
	ErrorLevel
)

// LogType holds the type of the log.
type LogType int

// When introducing a new log type, do the following:
// 1) add the new type as a constant below;
// 2) add the new type in params as a string for HTTP requests/responses;
// 3) include the new type in the mongodocLogTypes and paramsLogTypes maps
//    in internal/v4.
const (
	_ LogType = iota
	IngestionType
)

// Migration holds information about the database migration.
type Migration struct {
	// Executed holds the migration names for migrations already executed.
	Executed []string
}
