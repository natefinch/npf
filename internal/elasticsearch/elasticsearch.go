package elasticsearch

import (
    "time"

    "gopkg.in/juju/charm.v3"
)

// Entity holds the in-database representation of charm or bundle's
// document in the charms collection.
type Entity struct {
    // URL holds the fully specified URL of the charm or bundle.
    // e.g. cs:precise/wordpress-34, cs:~user/quantal/foo-2
    URL *charm.Reference `bson:"_id"`

    // BaseURL holds the reference URL of the charm or bundle
    // (this omits the series and revision from URL)
    // e.g. cs:wordpress, cs:~user/foo
    BaseURL *charm.Reference

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
    ExtraInfo map[string][]byte `bson:",omitempty"`

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
}
