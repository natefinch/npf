package mongodoc

import (
	"time"

	"gopkg.in/juju/charm.v2"
)

// Entity holds the in-database representation of charm or bundle's
// document in the charms collection.
type Entity struct {
	// URL holds the fully specified URL of the charm or bundle.
	// e.g. cs:precise/wordpress-34, cs:~user/quantal/foo-2
	URL *charm.URL `bson:"_id"`

	// BaseURL holds the URL of the charm or bundle with the
	// series and revision omitted.
	// e.g. cs:wordpress, cs:~user/foo
	BaseURL *charm.URL

	Revision int
	Sha256   string // This is also used as a blob reference.
	Size     int64

	UploadTime time.Time

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
	BundleCharms []*charm.URL

	// TODO Add fields denormalized for search purposes
	// and search ranking field(s).
}
