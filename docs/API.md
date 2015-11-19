# Charm store API

The current live api lives at https://api.jujucharms.com/charmstore/v4

## Intro

The charm store stores and indexes charms and bundles. A charm or bundle is
referred to by a charm store id which can take one of the following two forms:

* ~*owner*/*series*/*name*(-*revision*)
* *series*/*name*(-*revision*)

*Owner* is the name of the user that owns the charm.
*Series* is one of a small number of known possible series for charms
(currently just the Ubuntu series names) or the special name bundle to signify
that the charm id refers to a charm bundle.

A charm store id referring to a charm (not a bundle) can also use one of the
following two forms, omitting the series:

* ~*owner*/*name*(-*revision*)
* *name*(-*revision*)

In this case the store will look at all charms with the same *owner* and
*name*, and choose one according to its preference (for example, it currently
prefers the latest LTS series).

### Data format

All endpoints that do not produce binary data produce a single JSON object as
their result. These will be described in terms of the Go types that produce and
consume the format, along with an example. A charm id is represented as a
`charm.URL type`.


### Errors

If any request returns an error, it will produce it in the following form:

```go
type Error struct {
        Message string
        Code string
        Info map[string] Error `json:",omitempty"`
}
```

Example:

```json
{
  "Message": "unexpected Content-Type \"image/jpeg\"; expected \"application/json\"",
  "Code": "bad request"
}
```

Note: this format is compatible with the error results used by juju-core.
Currently defined codes are the following:

* not found
* metadata not found
* forbidden
* bad request
* duplicate upload
* multiple errors
* unauthorized
* method not allowed

The `Info` field is set when a request returns a "multiple errors" error code;
currently the only two endpoints that can are "/meta" and "*id*/meta/any".
Each element in `Info` corresponds to an element in the PUT request, and holds
the error for that element. See those endpoints for examples.

### Bulk requests and missing metadata

There are two forms of "bulk" API request that can return information about
several items at once. The `/meta/any` endpoint (along with some others) have a
set of "include" flags that specify metadata to return. The `/meta` endpoint
has a set of "id" flags that specify a set of ids to return data on.

In both of these cases, when the relevant data does not exist, the result will
be omitted from the returned map. For example a GET of
`/meta/archive-size?id=something` will return an empty map if the id
"something" is not found; a GET of
`/precise/wordpress-34/meta/any?include=bundle-metadata` will return an empty
map if the id "precise/wordpress-34" refers to a bundle rather than a charm.

For the singular forms of these endpoints, a 404 "metadata not found" error
will be returned when this happens.

In the `meta/any` GET bulk request, if some data requires authorization, the
default behavior is to return an authorization required response. Clients
interested in public data only can include a `ignore-auth=1` query so that only
public information is returned. In this case, results requiring authorization
(if any) will be omitted.

### Versioning

The version of the API is indicated by an initial "vN" prefix to the path.
Later versions will increment this number. This also means we can potentially
serve backwardly compatible paths to juju-core. All paths in this document
should be read as if they had a "v4" prefix. For example, the
`wordpress/meta/charm-metadata` path is actually at
`v4/wordpress/meta/charm-metadata`.


### Boolean values

Where a flag specifies a boolean property, the value must be either "1",
signifying true, or empty or "0", signifying false.

## Requests

### Expand-id

#### GET *id*/expand-id

The expand-id path expands a general id into a set of specific ids. It strips
any revision number and series from id, and returns a slice of all the possible
ids matched by that, including all the versions and series.

```go
[]Id

type Id struct {
        Id string
}
```

Example: `GET wordpress/expand-id`

```json
[
    {"Id": "precise/wordpress-1"},
    {"Id": "precise/wordpress-2"},
    {"Id": "trusty/wordpress-1"},
    {"Id": "trusty/wordpress-2"}
]
```

Example: `GET precise/wordpress-34/expand-id`

```json
[
    {"Id": "precise/wordpress-1"},
    {"Id": "precise/wordpress-2"},
    {"Id": "trusty/wordpress-1"},
    {"Id": "trusty/wordpress-2"}
]
```


### Archive

#### GET *id*/archive

The `/archive` path returns the raw archive zip file for the charm with the
given charm id. The response header includes the SHA 384 hash of the archive
(Content-Sha384) and the fully qualified entity id (Entity-Id).

Example: `GET wordpress/archive`

Any additional elements attached to the `/charm` path retrieve the file from
the charm or bundle's zip file. The `Content-Sha384` header field in the
response will hold the hash checksum of the archive.

#### GET *id*/archive/*path*

Retrieve a file corresponding to *path* in the charm or bundle's zip archive.

Example: `GET trusty/wordpress/archive/config.yaml`

#### POST *id*/archive

This uploads the given charm or bundle in zip format.

<pre>
POST <i>id</i>/archive?hash=<i>sha384hash</i>
</pre>

The id specified must specify the series and must not contain a revision
number. The hash flag must specify the SHA384 hash of the uploaded archive in
hexadecimal format. If the same content has already been uploaded, the response
will return immediately without reading the entire body.

The charm or bundle is verified before being made available.

The response holds the full charm/bundle id including the revision number.

```go
type UploadedId struct {
        Id string
}
```

Example response body:

```json
{
    "Id": "precise/wordpress-24"
}
```

#### DELETE *id*/archive

This deletes the given charm or bundle with the given id. If the ID is not
fully specified, the charm series or revisions are not resolved and the charm
is not deleted. In order to delete the charm, the ID must include series as
well as revisions. In order to delete all versions of the charm, use
`/expand-id` and iterate on all elements in the result.

### Visual diagram

#### GET *id*/diagram.svg

This returns a scalable vector-graphics image representing the entity with the
given id. This will return a not-found error for charms.

#### GET *id*/icon.svg

This returns the SVG image of the charm's icon. This reports a not-found error
for bundles. Unlike the `archive/icon.svg` where 404 is returned in case an
icon does not exist, this endpoint returns the default icon.

#### GET *id*/readme

This returns the README.

### Promulgation

#### PUT *id*/promulgate

A PUT to ~*user*/*anyseries*/*name*-*anyrevision* sets whether entities
with the id *x*/*name* are considered to be aliases
for ~*user*/*x*/*name* for all series *x*. The series
and revision in the id are ignored (except that an
entity must exist that matches the id).

If Promulgate is true, it means that any new charms published
to ~*user*/*x*/*name* will also be given the alias
*x*/*name*. The latest revision for all ids ~*user*/*anyseries*/*name*
will also be aliased likewise.

If Promulgate is false, any new charms published
to ~*user*/*anyseries*/*name* will not be given a promulgated
alias, but no change is made to any existing aliases.

The promulgated status can be retrieved from the
promulgated meta endpoint.

```go
type PromulgateRequest struct {
	Promulgate bool
}
```

Example: `PUT ~charmers/precise/wordpress-23/promulgate`

Request body:
```json
{
    "Promulgate" : true,
}
```

### Charm and bundle publishing

#### PUT *id*/publish

A PUT to ~*user*/*anyseries*/*name*-*anyrevision* sets whether the
corresponding charm or bundle is published and can be accessed through a URL
with no channel. If the revision number is not specified, the id is resolved to
the charm or bundle with the latest development revision number when
publishing, and to the charm or bundle with the latest non-development revision
number when unpublishing. The id must not include the development channel.

```go
type PublishRequest struct {
    Published bool
}
```

If Published is true, the charm or bundle is made available at the
non-development URL with the same revision number. If Published is false, the
id is unpublished.

The response includes the id and promulgated id of the entity after the action
is performed:

```go
type PublishResponse struct {
    Id            *charm.URL
    PromulgatedId *charm.URL `json:",omitempty"`
}
```

If the charm or bundle have been unpublished, the identifiers in the response
will represent the corresponding development charm or bundle.

Example: `PUT ~charmers/trusty/django-42/publish`

Request body:
```json
{
    "Published" : true,
}
```

Response body:
```json
{
    "Id" : "cs:~charmers/trusty/django-42",
    "PromulgatedId": "cs:trusty/django-10",
}
```

### Stats

#### GET stats/counter/...

This endpoint can be used to retrieve stats related to entities.

<pre>
GET stats/counter/<i>key</i>[:<i>key</i>]...?[by=<i>unit</i>]&start=<i>date</i>][&stop=<i>date</i>][&list=1]
</pre>

The stats path allows the retrieval of counts of operations in a general way. A
statistic is composed of an ordered tuple of keys:

<pre>
<i>kind</i>:<i>series</i>:<i>name</i>:<i>user</i>
</pre>
Operations on the store increment counts associated with a specific tuple,
determined by the operation and the charm being operated on.

When querying statistics, it is possible to aggregate statistics by using a
`\*` as the last tuple element, standing for all tuples with the given prefix.
For example, `missing:\*` will retrieve the counts for all operations of kind
"missing", regardless of the series, name or user.

If the list flag is specified, counts for all next level keys will be listed.
 For example, a query for `stats/counter/download:*?list=1&by=week` will show
 all the download counts for each series for each week.

If a date range is specified, the returned counts will be restricted to the
given date range. Dates are specified in the form "yyyy-mm-dd". If the `by`
flag is specified, one count is shown for each unit in the specified period,
where unit can be `week` or `day`.

Possible kinds are:

* archive-download
* archive-delete
* archive-upload
* archive-failed-upload

```go
[]Statistic

type Statistic struct {
        Key string      `json:",omitempty"`
        Date string     `json:",omitempty"`
        Count int64
}
```

Example: `GET "stats/counter/missing:trusty:*"`

```json
[
    {"Count": 1917}
]
```

Example:
`GET stats/counter/download/archive-download:*?by=week&list=1&start=2014-03-01`

```json
[
    {
        "Key": "charm-bundle:precise:*",
        "Date": "2014-06-08",
        "Count": 2715
    }, {
        "Key": "charm-bundle:trusty:*",
        "Date": "2014-06-08",
        "Count": 2672
    }, {
        "Key": "charm-bundle:oneiric:*",
        "Date": "2014-06-08",
        "Count": 14
    }, {
        "Key": "charm-bundle:quantal:*",
        "Date": "2014-06-08",
        "Count": 1
    }, {
        "Key": "charm-bundle:trusty:*",
        "Date": "2014-06-15",
        "Count": 3835
    }, {
        "Key": "charm-bundle:precise:*",
        "Date": "2014-06-15",
        "Count": 3389
    }
]
```

**Update**:
We need to provide aggregated stats for downloads:
* promulgated and ~user counterpart charms should have the same download stats.

#### PUT stats/update

This endpoint can be used to increase the stats related to an entity.
This will increase the download stats by one for the entity provided and at the time stamp provided.
It can for future purpose include the client issuing the requests.
This is used when charmstore is in front of a cache server that will not call the real /archive endpoint and
as such will not increase the download counts.

<pre>
PUT stats/counter
</pre>

Request body:
```go
type StatsUpdateRequest struct {
	Timestamp      time.Time
	Type           string
	CharmReference *charm.URL
}
```
Example: `PUT stats/update`


Request body:
```json
{
    "Timestamp":"2015-08-06T06:46:13Z",
    "Type":"deploy",
    "CharmReference":"cs:~charmers/utopic/wordpress-42"
}
```

### Meta

#### GET meta

The meta path returns an array of all the path names under meta, excluding the
`meta/any` path, as suitable for passing as "include=" flags to paths that
allow those. Note that the result does not include sub-paths of extra-info
because these vary according to each charm or bundle.

Example: `GET /meta`

```json
[
    "archive-size",
    "archive-upload-time",
    "bundle-machine-count",
    "bundle-metadata",
    "bundle-unit-count",
    "bundles-containing",
    "charm-actions",
    "charm-config",
    "charm-metadata",
    "charm-related",
    "extra-info",
    "hash",
    "hash256",
    "id",
    "id-name",
    "id-revision",
    "id-series",
    "id-user",
    "manifest",
    "promulgated",
    "revision-info",
    "stats",
    "supported-series",
    "tags"
]
```

#### GET meta/*endpoint*

This endpoint allows a user to query any number of IDs for metadata.
<pre>
GET meta/<i>endpoint</i>?id=<i>id0</i>[&id=<i>id1</i>...][<i>otherflags</i>]
</pre>

This call is equivalent to calling "*id*/meta" for each id separately. The
result holds an element for each id in the request with the resulting metadata
exactly as returned by "GET *id*/meta/*endpoint*[?*otherflags*]". The map keys
are the ids exactly as specified in the request, although they are resolved to
fill in series and revision as usual when fetching the metadata. Any ids that
are not found, or with non-relevant metadata, will be omitted.

```go
map[string] interface{}
```

Example: `GET meta/archive-size?id=wordpress&id=mysql`

```json
{
    "wordpress": {
        "Size": 1234
    },
    "mysql" : {
        "Size": 4321
    }
}
```

Example: `GET /meta/any?include=archive-size&include=extra-info/featured&id=wordpress&id=mysql`

```json
{
    "wordpress": {
        "Id": "precise/wordpress-3",
        "Meta": {
            "archive-size": {
                "Size": 1234
            },
            "extra-info/featured": true
        }
    },
    "mysql" : {
        "Id": "precise/mysql-23",
        "Meta": {
            "archive-size": {
                "Size": 4321
            },
            "extra-info/featured": true
        }
    }
}
```

#### PUT meta/*endpoint*

A PUT to this endpoint allows the metadata endpoint of several ids to be
updated. The request body is as specified in the result of the above GET
request. The ids in the body specify the ids that will be updated. If there is
a failure, the error code will be "multiple errors", and the Info field will
holds one entry for each id in the request body that failed, holding the error
for that id. If there are no errors, PUT endpoints usually return an empty body
in the response.

Example: `PUT meta/extra-info/featured`

Request body:
```json
{
    "precise/wordpress-23" : true,
    "precise/mysql-53" :     true,
    "precise/wordpress-22" : false,
}
```

Example: `PUT meta/any`

Request body:
```json
{
    "precise/wordpress-23": {
        "Meta": {
            "extra-info/featured": true,
            "extra-info/revision-info": "12dfede4ee23",
            "bad-metaname": 3235
        }
    },
    "trusty/mysql-23": {
        "Meta": {
            "extra-info/featured": false,
        }
    }
}
```

Response body (with HTTP status 500):
```json
{
    "Message": "multiple errors (1) found",
    "Code": "multiple errors",
    "Info": {
        "precise/wordpress-23": {
            "Message": "multiple errors",
            "Code": "multiple errors",
            "Info": {
                "bad-metaname": {
                    "Message": "metadata not found",
                    "Code": "not found"
                }
            }
        }
    }
}
```

If the request succeeds, a 200 OK status code is returned with an empty
response body.

#### GET *id*/meta

This path returns the same information as the meta path. The results are the
same regardless of the actual id.

Example: `GET foo/meta`

```json
[
    "archive-size",
    "archive-upload-time",
    "bundle-machine-count",
    "bundle-metadata",
    "bundle-unit-count",
    "bundles-containing",
    "charm-actions",
    "charm-config",
    "charm-metadata",
    "charm-related",
    "extra-info",
    "id",
    "id-name",
    "id-revision",
    "id-series",
    "id-user",
    "manifest",
    "promulgated",
    "revision-info",
    "stats",
    "tags"
]
```

#### GET *id*/meta/any

<pre>
GET <i>id</i>/meta/any?[include=<i>meta</i>[&include=<i>meta</i>...]]
</pre>

The `meta/any` path returns requested metadata information on the given id. If
the id is non-specific, the latest revision and preferred series for the id
will be assumed.

Other metadata can be requested by specifying one or more `include` flags. The
value of each meta must be the name of one of the path elements defined under
the `/meta` path (for example: `charm-config`, `charm-meta`, `manifest`) and
causes the desired metadata to be included in the Meta field, keyed by meta. If
there is no metadata for the given meta path, the element will be omitted (for
example, if bundle-specific data is requested for a charm id).

The `any` path may not itself be the subject of an include directive. It is
allowed to specify "charm-" or "bundle-"" specific metadata paths -- if the id
refers to a charm then bundle-specific metadata will be omitted and vice versa.

Various other paths use the same `include` mechanism to allow retrieval of
arbitrary metadata.

```go
type Meta struct {
        Id string                      `json:",omitempty"`
        Meta map[string] interface{}   `json:",omitempty"`
}
```

Example: `GET wordpress/meta/any`

```json
{
    "Id": "trusty/wordpress-32"
}
```

Example: `GET ubuntu/meta/any?include=archive-size&include=extra-info/featured`

```json
{
	"Id": "trusty/ubuntu-3",
	"Meta": {
        "archive-size": {
           "Size": 7580
        },
        "extra-info/featured": true
	}
}
```

#### PUT *id*/meta/any

This endpoint allows the updating of several metadata elements at once. These
must support PUT requests. The body of the PUT request is in the same form as
returned by the above GET request, except with the Id field omitted. The
elements inside the Meta field specify which meta endpoints will be updated. If
one or more of the update fails, the resulting error will contain an Info field
that has an entry for each update that fails, keyed by the endpoint name.

Example: `PUT ubuntu/meta/any`

Request body:
```json
{
    "Meta": {
        "extra-info": {
            "revision-info": "a46f45649f0d0e0b"
        },
        "extra-info/featured": true
    }
}
```

Example: `PUT ubuntu/meta/any`

Request body:
```json
{
    "Meta": {
        "extra-info/featured": false,
        "archive-size": 12354,
    }
}
```

Response body:
```json
{
    "Message": "multiple errors",
    "Code": "multiple errors",
    "Info": {
        "archive-size": {
            "Message": "method not allowed",
            "Code": "bad request",
        }
    }
}
```

#### GET *id*/meta/charm-metadata

The `/meta/charm.metadata` path returns the contents of the charm metadata file
for a charm. The id must refer to a charm, not a bundle.

```go
type CharmMetadata struct {
        Summary     string
        Description string
        Subordinate bool                        `json:",omitempty"`
        // Provides and Requires map from the relation name to
        // information about the relation.
        Provides    map[string]Relation         `json:",omitempty"`
        Requires    map[string]Relation         `json:",omitempty"`
        Peers       map[string]Relation         `json:",omitempty"`
        Tags  []string                          `json:",omitempty"`
}

type Relation struct {
        Interface string
        Optional  bool                          `json:",omitempty"`
        Limit     int                           `json:",omitempty"`
        Scope     RelationScope
}

type RelationRole string
type RelationScope string
```

The possible values of a `RelationScope` are

* global
* container

Example: `GET wordpress/meta/charm-metadata`

```json
{
    "Summary": "WordPress is a full featured web blogging tool, this charm deploys it.",
    "Description": "This will install and setup WordPress optimized to run in the cloud. This install, in particular, will \n place Ngnix and php-fpm configured to scale horizontally with Nginx's reverse proxy\n",
    "Provides": {
        "website": {
            "Interface": "http",
            "Scope": "global"
        }
    },
    "Requires": {
        "cache": {
            "Interface": "cache",
            "Scope": "global"
        },
        "db": {
            "Interface": "db",
            "Scope": "global"
        }
    },
    "Peers": {
        "loadbalancer": {
            "Interface": "reversenginx",
            "Scope": "global"
        }
    },
    "Tags": [
        "applications"
    ]
}
```

#### GET *id*/meta/bundle-metadata

The `meta/bundle-metadata` path returns the contents of the bundle metadata
file for a bundle. The id must refer to a bundle, not a charm.

```go
type BundleData struct {
        Services map[string] ServiceSpec
        Machines map[string] MachineSpec `json:",omitempty"`
        Series string                    `json:",omitempty"`
        Relations [][]string             `json:",omitempty"`
}

type MachineSpec struct {
        Constraints string               `json:",omitempty"`
        Annotations map[string]string    `json:",omitempty"`
}

type ServiceSpec struct {
        Charm string
        NumUnits int
        To []string                      `json:",omitempty"`

        // Options holds the configuration values
        // to apply to the new service. They should
        // be compatible with the charm configuration.
        Options map[string]interface{}   `json:",omitempty"`
        Annotations map[string]string    `json:",omitempty"`
        Constraints string               `json:",omitempty"`
}
```

Example: `GET mediawiki/meta/bundle-metadata`

```json
{
    "Services": {
        "mediawiki": {
            "Charm": "cs:precise/mediawiki-10",
            "NumUnits": 1,
            "Options": {
                "debug": false,
                "name": "Please set name of wiki",
                "skin": "vector"
            },
            "Annotations": {
                "gui-x": "619",
                "gui-y": "-128"
            }
        },
        "memcached": {
            "Charm": "cs:precise/memcached-7",
            "NumUnits": 1,
            "Options": {
                "connection_limit": "global",
                "factor": 1.25
            },
            "Annotations": {
                "gui-x": "926",
                "gui-y": "-125"
            }
        }
    },
    "Relations": [
        [
            "mediawiki:cache",
            "memcached:cache"
        ]
    ]
}
```

#### GET *id*/meta/bundle-unit-count

The `meta/bundle-unit-count` path returns a count of all the units that will be
created by a bundle. The id must refer to a bundle, not a charm.

```go
type BundleCount struct {
        Count int
}
```

Example: `GET bundle/mediawiki/meta/bundle-unit-count`

```json
{
    "Count": 1
}
```

#### GET *id*/meta/bundle-machine-count

The `meta/bundle-machine-count` path returns a count of all the machines used
by a bundle. The id must refer to a bundle, not a charm.

```go
type BundleCount struct {
        Count int
}
```

Example: `GET bundle/mediawiki/meta/bundle-machine-count`

```json
{
    "Count": 2
}
```

#### GET *id*/meta/manifest

The `meta/manifest` path returns the list of all files in the bundle or charm's
archive.

```go
[]ManifestFile
type ManifestFile struct {
        Name string
        Size int64
}
```

Example: `GET trusty/juju-gui-3/meta/manifest`

```json
[
    {
        "Name": "config.yaml",
        "Size": 8254
    },
    {
        "Name": "HACKING.md",
        "Size": 11376
    },
    {
        "Name": "Makefile",
        "Size": 3304
    },
    {
        "Name": "metadata.yaml",
        "Size": 1110
    },
    {
        "Name": "README.md",
        "Size": 9243
    },
    {
        "Name": "hooks/config-changed",
        "Size": 1636
    },
    {
        "Name": "hooks/install",
        "Size": 3055
    },
    {
        "Name": "hooks/start",
        "Size": 1101
    },
    {
        "Name": "hooks/stop",
        "Size": 1053
    }
]
```

#### GET *id*/meta/charm-actions


The `meta/charm-actions` path returns the actions available in a charm as
stored in its `actions.yaml` file. Id must refer to a charm, not a bundle.

```go
type Actions struct {
        Actions map[string]ActionSpec `json:",omitempty"`
}

type ActionSpec struct {
        Description string
        Params JSONSchema
}
```

The Params field holds a JSON schema specification of an action's parameters.
See [http://json-schema.org/latest/json-schema-core.html](http://json-schema.org/latest/json-schema-core.html).

Example: `GET wordpress/meta/charm-actions`

```json
{
    "Actions": {
        "backup": {
            "Description": "back up the charm",
            "Params": {
                "properties": {
                    "destination-host": {
                        "type": "string"
                    },
                    "destination-name": {
                        "type": "string"
                    }
                },
                "required": [
                    "destination-host"
                ],
                "type": "object"
            }
        }
    }
}
```

#### GET *id*/meta/charm-config

The `meta/charm-config` path returns the charm's configuration specification as
stored in its `config.yaml` file. Id must refer to a charm, not a bundle.

```go
type Config struct {
        Options map[string] Option
}

// Option represents a single charm config option.
type Option struct {
        Type        string
        Description string
        Default     interface{}
}
```

Example: `GET trusty/juju-gui-3/meta/charm-config`

```json
{
    "Options": {
        "builtin-server": {
            "Type": "boolean",
            "Description": "Enable the built-in server.",
            "Default": true
        },
        "login-help": {
            "Type": "string",
            "Description": "The help text shown to the user.",
            "Default": null
        },
        "read-only": {
            "Type": "boolean",
            "Description": "Enable read-only mode.",
            "Default": false
        }
    }
}
```

#### GET *id*/meta/archive-size

The `meta/archive-size` path returns the archive size, in bytes, of the archive
of the given charm or bundle id.

```go
type ArchiveSize struct {
        Size int64
}
```

Example: `GET wordpress/meta/archive-size`

```json
{
    "Size": 4747
}
```

#### GET *id*/meta/hash

This path returns the SHA384 hash sum of the archive of the given charm or
bundle id.

```go
type HashResponse struct {
    Sum string
}
```

Example: `GET wordpress/meta/hash`

Response body:
```json
{
    "Sum": "0a410321586d244d3981e2b23a27a7e86ebdcab8bd0ca8f818d3f4c34b2ea2791e0dbdc949f70b283a3f5efdf908abf1"
}
```

#### GET *id*/meta/hash256

This path returns the SHA256 hash sum of the archive of the given charm or
bundle id.

```go
type HashResponse struct {
    Sum string
}
```

Example: `GET wordpress/meta/hash256`

Response body:
```json
{
    "Sum": "9ab5036cc18ba61a9d25fad389e46b3d407fc02c3eba917fe5f18fdf51ee6924"
}
```

#### GET *id*/meta/supported-series

This path returns the set of series supported by the given
charm. This endpoint is appropriate for charms only.

```go
type SupportedSeriesResponse struct {
    SupportedSeries []string
}
```

Example: `GET precise/wordpress/meta/supported-series`

Response body:
```json
{
    "SupportedSeries": ["precise"]
}
```

#### GET *id*/meta/bundles-containing

The `meta/bundles-containing` path returns information on the last revision of
any bundles that contain the charm with the given id.

<pre>
GET <i>id</i>/meta/bundles-containing[?include=<i>meta</i>[&include=<i>meta</i>...]]
</pre>

The Meta field is populated with information on the returned bundles according
to the include flags - see the `meta/any` path for more info on how to use the
`include` flag. The only values that are valid for `any-series`, `any-revision`
or `all-results` flags are 0, 1 and empty. If `all-results` is enabled, all the
bundle revisions are returned, not just the last one. The API should validate
that and return bad request if any other value is provided.

```go
[]Bundle
type Bundle struct {
        Id string
        Meta map[string]interface{} `json:",omitempty"`
}
```

Example: `GET mysql/meta/bundles-containing?include=featured` might return:

```json
[
    {
        "Id": "bundle/mysql-scalable",
        "Meta": {
            "featured": {
                "Featured": false
            }
        }
    },
    {
        "Id": "bundle/wordpress-simple",
        "Meta": {
            "featured": {
                "Featured": true
            }
        }
    }
]
```

#### GET *id*/meta/extra-info

The meta/extra-info path reports any additional metadata recorded for the
charm. This contains only information stored by clients - the API server itself
does not populate any fields. The resulting object holds an entry for each
piece of metadata recorded with a PUT to `meta/extra-info`.

```go
type ExtraInfo struct {
        Values map[string] interface{}
}
```

Example: `GET wordpress/meta/extra-info`

```json
{
    "featured": true,
    "vcs-digest": "4b6b3c7d795eb66ca5f82bc52c01eb57ab595ab2"
}
```

#### GET *id*/meta/extra-info/*key*

This path returns the contents of the given `extra-info` key. The result is
exactly the JSON value stored as a result of the PUT request to `extra-info` or
`extra-info/key`.

Example: `GET wordpress/meta/extra-info/featured`

```json
true
```

#### PUT *id*/meta/extra-info

This request updates the value of any metadata values. Any values that are not
mentioned in the request are left untouched. Any fields with null values are
deleted.

Example: `PUT precise/wordpress-32/meta/extra-info`

Request body:
```json
{
    "vcs-digest": "7d6a853c7bb102d90027b6add67b15834d815e08",
}
```

#### PUT *id*/meta/extra-info/*key*

This request creates or updates the value for a specific key.
If the value is null, the key is deleted.

Example: `PUT precise/wordpress-32/meta/extra-info/vcs-digest`

Request body:

```json
"7d6a853c7bb102d90027b6add67b15834d815e08",
```

The above example is equivalent to the `meta/extra-info` example above.

#### GET *id*/meta/charm-related

The `meta/charm-related` path returns all charms that are related to the given
charm id, which must not refer to a bundle. It is possible to include
additional metadata for charms by using the `include` query:

<pre>
GET <i>id</i>/meta/charm-related[?include=<i>meta</i>[&include=<i>meta</i>...]]
</pre>

```go
type Related struct {
        // Requires holds an entry for each interface provided by
        // the charm, containing all charms that require that interface.
        Requires map[string] []Item        `json:",omitempty"`


        // Provides holds an entry for each interface required by the
        // the charm, containing all charms that provide that interface.
        Provides map[string] []Item        `json:",omitempty"`
}


type Item struct {
        Id string
        Meta map[string] interface{}       `json:",omitempty"`
}
```

The Meta field is populated according to the include flags  - see the `meta`
path for more info on how to use this.

Example: `GET wordpress/meta/charm-related`

```json
{
    "Requires": {
        "memcache": [
            {"Id": "precise/memcached-13"}
        ],
        "db": [
            {"Id": "precise/mysql-46"},
            {"Id": "~clint-fewbar/precise/galera-42"}
        ]
    },
    "Provides": {
        "http": [
            {"Id": "precise/apache2-24"},
            {"Id": "precise/haproxy-31"},
            {"Id": "precise/squid-reverseproxy-8"}
        ]
    }
}
```

Example: `GET trusty/juju-gui-3/meta/charm-related?include=charm-config`

```json
{
    "Provides": {
        "http": [
            {
                "Id": "precise/apache2-24",
                "Meta": {
                    "charm-config": {
                        "Options": {
                            "logrotate_count": {
                                "Type": "int",
                                "Description": "The number of days",
                                "Default": 365
                            }
                        }
                    }
                }
            }
        ],
        "nrpe-external-master": [
            {
                "Id": "precise/nova-compute-31",
                "Meta": {
                    "charm-config": {
                        "Options": {
                            "bridge-interface": {
                                "Type": "string",
                                "Description": "Bridge interface",
                                "Default": "br100"
                            },
                            "bridge-ip": {
                                "Type": "string",
                                "Description": "IP to be assigned to bridge",
                                "Default": "11.0.0.1"
                            }
                        }
                    }
                }
            }
        ]
    }
}
```

#### GET *id*/meta/archive-upload-time

The `meta/archive-upload-time` path returns the time the archives for the given
*id* was uploaded. The time is formatted according to RFC3339.

```go
type ArchiveUploadTimeResponse struct {
    UploadTime time.Time
}
```

Example: `GET trusty/wordpress-42/meta/archive-upload-time`

```json
{
    "UploadTime": "2014-07-04T13:53:57.403506102Z"
}
```

#### GET *id*/meta/promulgated

The `promulgated` path reports whether the entity with the given ID is promulgated.
Promulgated charms do not require the user portion of the ID to be specified.

```go
type PromulgatedResponse struct {
	Promulgated bool
}
```

Example: `GET trusty/wordpress-42/meta/promulgated`

```json
{
	"Promulgated": true
}
```

#### GET *id*/meta/stats

<pre>
GET <i>id</i>/meta/stats?[refresh=0|1]
</pre>

Many clients will need to use stats to determine the best result. Details for a
charm/bundle might require the stats as important information to users.
Currently we track deployment stats only. We intend to open this up to
additional data. The response includes downloads count for both the specific
requested entity revision and for all the revisions, and it is structured as
below:

```go
// StatsResponse holds the result of an id/meta/stats GET request.
type StatsResponse struct {
        // ArchiveDownloadCount is superceded by ArchiveDownload but maintained for
        // backward compatibility.
        ArchiveDownloadCount int64
        // ArchiveDownload holds the downloads count for a specific revision of the
        // entity.
        ArchiveDownload StatsCount
        // ArchiveDownloadAllRevisions holds the downloads count for all revisions
        // of the entity.
        ArchiveDownloadAllRevisions StatsCount
}

// StatsCount holds stats counts and is used as part of StatsResponse.
type StatsCount struct {
        Total int64 // Total count over all time.
        Day   int64 // Count over the last day.
        Week  int64 // Count over the last week.
        Month int64 // Count over the last month.
}
```

If the refresh boolean parameter is non-zero, the latest stats will be returned without caching.

#### GET *id*/meta/tags

The `tags` path returns any tags that are associated with the entity.

Example: `GET trusty/wordpress-42/meta/tags`

```json
{
    "Tags": [
        "blog",
        "cms"
    ]
}
```

#### GET *id*/meta/revision-info

The `revision-info` path returns information about other available revisions of
the charm id that the charm store knows about. It will include both older and
newer revisions. The fully qualified ids of those charms will be returned in an
ordered list from newest to oldest revision. Note that the current revision
will be included in the list as it is also an available revision.

```go
type RevisionInfoResponse struct {
        Revisions []*charm.URL
}
```

Example: `GET trusty/wordpress-42/meta/revision-info`

```json
{
    "Revisions": [
        "cs:trusty/wordpress-43",
        "cs:trusty/wordpress-42",
        "cs:trusty/wordpress-41",
        "cs:trusty/wordpress-39"
    ]
}
```

#### GET *id*/meta/id

The `id` path returns information on the charm or bundle id, split apart into
its various components, including the id itself. The information is exactly
that contained within the entity id.

```go
type IdResponse struct {
        Id *charm.URL
        User string
        Series string `json:",omitempty"`
        Name string
        Revision int
}
```

Example: `GET ~bob/trusty/wordpress/meta/id`

```json
{
    "Id": "~bob/trusty/wordpress-42",
    "User": "bob",
    "Series": "trusty",
    "Name": "wordpress",
    "Revision": 42
}
```

Example: `GET precise/wordpress/meta/id`

```json
{
    "Id": "precise/wordpress-42",
    "Series": "precise",
    "Name": "wordpress",
    "Revision": 42
}
```

Example: `GET bundle/openstack/meta/id`

```json
{
    "Id": "bundle/openstack-3",
    "Series": "bundle",
    "Name": "openstack",
    "Revision": 3
}
```

#### GET *id*/meta/id-revision

The `revision` path returns information on the revision of the id. The
information is exactly that contained within the id.

```go
type Revision struct {
        Revision int
}
```

Example: `GET trusty/wordpress-42/meta/id-revision`

```json
{
    "Revision": 42
}
```

#### GET *id*/meta/id-name

The `name` path returns information on the name of the id. The information is
exactly that contained within the id.

```go
type Name struct {
        Name string
}
```

Example: `GET trusty/wordpress-42/meta/id-name`

```json
{
    "Name": "wordpress"
}
```

#### GET *id*/meta/id-user

The `id-user` path returns information on the user name in the id. This
information is exactly that contained within the id.

```go
type User struct {
        User string
}
```

Example: `GET ~bob/trusty/wordpress-42/meta/id-user`

```json
{
    "User": "bob"
}
```

Example: `GET trusty/wordpress-42/meta/id-user`

```json
{
    "User": ""
}
```

#### GET *id*/meta/id-series

The `id-series` path returns information on the series in the id. This
information is exactly that contained within the id. For bundles, this will
return "bundle".

```go
type Series struct {
        Series string
}
```

Example: `GET ~bob/trusty/wordpress-42/meta/id-series`

```json
{
    "Series": "trusty"
}
```

#### GET *id*/meta/common-info

The meta/common-info path reports any common metadata recorded for the base
entity. This contains only information stored by clients - the API server
itself does not populate any fields. The resulting object holds an entry for
each piece of metadata recorded with a PUT to `meta/common-info`.

```go
type CommonInfo struct {
        Values map[string] interface{}
}
```

Example: `GET wordpress/meta/common-info`
         `GET precise/wordpress-32/meta/common-info`

```json
{
    "homepage": "http://wordpress.org",
    "bugs-url": "http://wordpress.org/bugs",
}
```

#### GET *id*/meta/common-info/*key*

This path returns the contents of the given `common-info` key. The result is
exactly the JSON value stored as a result of the PUT request to `common-info` or
`common-info/key`.

Example: `GET wordpress/meta/common-info/homepage`
         `GET precise/wordpress-32/meta/common-info/homepage`

```json
"http://wordpress.org"
```

#### PUT *id*/meta/common-info

This request updates the value of any metadata values. Any values that are not
mentioned in the request are left untouched. Any fields with null values are
deleted.

Example: `PUT precise/wordpress-32/meta/common-info`

Request body:
```json
{
    "bugs-url": "http://wordpress.org/newbugs",
}
```

#### PUT *id*/meta/common-info/*key*

This request creates or updates the value for a specific key.
If the value is null, the key is deleted.

Example: `PUT precise/wordpress-32/meta/common-info/bugs-url`

Request body:

```json
"http://wordpress.org/newbugs",
```

The above example is equivalent to the `meta/common-info` example above.

### Resources

**Not yet implemented**

#### POST *id*/resources/name.stream

Posting to the resources path creates a new version of the given stream
for the charm with the given id. The request returns the new version.

```go
type ResourcesRevision struct {
        Revision int
}
```

#### GET *id*/resources/name.stream[-revision]/arch/filename

Getting from the `/resources` path retrieves a charm resource from the charm
with the given id. If version is not specified, it retrieves the latest version
of the resource. The SHA-256 hash of the data is specified in the HTTP response
headers.

#### PUT *id*/resources/[~user/]series/name.stream-revision/arch?sha256=hash

Putting to the `resources` path uploads a resource (an arbitrary "blob" of
data) associated with the charm with id series/name, which must not be a
bundle. Stream and arch specify which of the charms resource streams and which
architecture the resource will be associated with, respectively. Revision
specifies the revision of the stream that's being uploaded to.

The hash value must specify the hash of the stream. If the same series, name,
stream, revision combination is PUT again, it must specify the same hash.

### Search

#### GET search

The `search` path searches within the latest version of charms and bundles
within the store.

<pre>
GET search[?text=<i>text</i>][&autocomplete=1][&filter=<i>value</i>...][&limit=<i>limit</i>][&skip=<i>skip</i>][&include=<i>meta</i>[&include=<i>meta</i>...]][&sort=<i>field</i>]
</pre>

`text` specifies any text to search for. If `autocomplete` is specified, the
search will return only charms and bundles with a name that has text as a
prefix. `limit` limits the number of returned items to the specified limit
count. `skip` skips over the first skip items in the result. Any number of
filters may be specified, limiting the search to items with attributes that
match the specified filter value. Items matching any of the selected values for
a filter are selected, so `name=1&name=2` would match items whose name was
either 1 or 2. However, if multiple filters are specified, the charm must match
all of them, so `name=1&series=2` will only match charms whose name is 1 and
whose series is 2. Available filters are:

* tags - the set of tags associated with the charm.
* name - the charm's name.
* owner - the charm's owner (the ~user element of the charm id)
* promulgated - the charm has been promulgated.
* provides - interfaces provided by the charm.
* requires - interfaces required by the charm.
* series - the charm's series.
* summary - the charm's summary text.
* description - the charm's description text.
* type - "charm" or "bundle" to search only one doctype or the other.


Notes

1. filtering on a specified, but empty, owner is the same as filtering on promulgated=1.
2. a specified, but empty text field will return all charms and bundles.
3. the promulgated filter is only applied if specified. If the value is "1" then only
   promulgated entities are returned if it is any other value only non-promulgated
   entities are returned.

The response contains a list of information on the charms or bundles that were
matched by the request. If no parameters are specified, all charms and bundles
will match.  By default, only the charm store id is included.

The results are sorted according to the given sort field, which may be one of
`owner`, `name` or `series`, corresponding to the filters of the same names. If
the field is prefixed with a hyphen (-), the sorting order will be reversed. If
the sort field is not specified, the results are returned in
most-relevant-first order if the text filter was specified, or an arbitrary
order otherwise. It is possible to specify more than one sort field to get
multi-level sorting, e.g. sort=name,-series will get charms in order of the
charm name and then in reverse order of series.

The Meta field is populated according to the include flag  - see the `meta`
path for more info on how to use this.

```go
[]SearchResult

type SearchResult struct {
        Id string
        // Meta holds at most one entry for each meta value
        // specified in the include flags, holding the
        // data that would be returned by reading /meta/meta?id=id.
        // Metadata not relevant to a particular result will not
        // be included.
        Meta map[string] interface{} `json:",omitempty"`
}
```

Example: `GET search?text=word&autocomplete=1&limit=2&include=archive-size`

```json
[
    {
        "Id": "precise/wordpress-1",
        "Meta": {
            "archive-size": {
                "Size": 1024
            }
        }
    },
    {
        "Id": "precise/wordpress-2",
        "Meta": {
            "archive-size": {
                "Size": 4242
            }
        }
    }
]
```

#### GET search/interesting

This returns a list of bundles and charms which are interesting from the Juju
GUI perspective. Those are shown on the left sidebar of the GUI when no other
search requests are performed.

`GET search/interesting[?limit=limit][&include=meta]`

The Meta field is populated according to the include flag  - see the `meta`
path for more info on how to use this.
The `limit` flag is the same as for the "search" path.

### Debug info

#### GET /debug

**Not yet implemented**

This returns metadata describing the current version of the software running
the server, and any other information deemed appropriate. The specific form of
 the returned data is deliberately left unspecified for now.

#### GET /debug/status

Used as a health check of the service. The API will also be used for nagios
tests. The items that are checked:

* connection to MongoDB
* connection to ElasticSearch (if needed) (based on charm config) (elasticsearch cluster status, all nodes up/etc see charmworld)
* number of charms and bundles in the blobstore
* number of promulgated items
* time and location of service start
* time of last ingestion process
* did ingestion finish
* did ingestion finished without errors (this should not count charm/bundle ingest errors)

```go
type DebugStatuses map[string] struct {
    Name string
    Value string
    Passed bool
}
```

Example: `GET /debug/status`

```json
{
    "mongo_connected" : {
        "Name": "MongoDB is connected",
        "Value": "Connected",
        "Passed": true
    },
    "mongo_collections" : {
        "Name": "MongoDB collections",
        "Value": "All required collections exist",
        "Passed": true
    },
    "ES_connected": {
        "Name": "ElasticSearch is connected",
        "Value": "Connected",
        "Passed": true
    },
    "entities": {
        "Name": "Entities in charm store",
        "Value": "5701 charms; 2000 bundles; 42 promulgated",
        "Passed": true,
    },
    "server_started": {
        "Name": "Server started",
        "Value": "123.45.67.89 2014-09-16 11:12:29Z",
        "Passed": true
    },
}
```

### Permissions

All entities in the charm store have their own access control lists. Read and
write permissions are supported for specific users and groups. By default, all
charms and bundles are readable by everyone, meaning that anonymous users can
retrieve archives and metadata information without restrictions. The permission
endpoints can be used to retrieve or change entities' permissions.

#### GET *id*/meta/perm

This path reports the read and write ACLs for the charm or bundle.

```go
type PermResponse struct {
    Read  []string
    Write []string
}
```

If the `Read` ACL is empty, the entity and its metadata cannot be retrieved by
anyone.
If the `Write` ACL is empty, the entity cannot be modified by anyone.
The special user `everyone` indicates that the corresponding operation
(read or write) can be performed by everyone, including anonymous users.

Example: `GET ~joe/wordpress/meta/perm`

```json
{
    "Read": ["everyone"],
    "Write": ["joe"]
}
```

#### PUT *id*/meta/perm

This request updates the permissions associated with the charm or bundle.

```go
type PermResponse struct {
    Read  []string
    Write []string
}
```

If the Read or Write ACL is empty or missing from the request body, that
field will be overwritten as empty. See the *id*/meta/perm/*key* request
to PUT only Read or Write.

Example: `PUT precise/wordpress-32/meta/perm`

Request body:
```json
{
    "Read": ["everyone"],
    "Write": ["joe"]
}
```

#### GET *id*/meta/perm/*key*

This path returns the contents of the given permission *key* (that can be
`read` or `write`). The result is exactly the JSON value stored as a result of
the PUT request to `meta/perm/key`.

Example: `GET wordpress/meta/perm/read`

```json
["everyone"]
```

#### PUT *id*/meta/perm/*key*

This request updates the *key* permission associated with the charm or bundle,
where *key* can be `read` or `write`.

Example: `PUT precise/wordpress-32/meta/perm/read`

Request body:

```json
["joe", "frank"]
```

### Authorization

#### GET /macaroon

This endpoint returns a macaroon in JSON format that, when its third
party caveats are discharged, will allow access to the charm store. No
prior authorization is required.

#### GET /delegatable-macaroon

This endpoint returns a macaroon in JSON format that can be passed to
third parties to allow them to access the charm store on the user's
behalf.  A first party "is-entity" caveat may be added to restrict those
parties so that they can only access a given charmstore entity with a
specified id.

A delegatable macaroon will only be returned to an authorized user (not
including admin). It will carry the same privileges as the macaroon used
to authorize the request.

#### GET /whoami

This endpoint returns the user name of the client and the list of groups the user
is a member of. This endpoint requires authorization.

Example: `GET whoami`

```json
{
    "User": "alice",
    "Groups": ["charmers", "admin", "team-awesome"]
}
```

The response is defined as:
```go
type WhoAmIResponse struct {
    User string
    Groups []string
}
```

### Logs

#### GET /log

This endpoint returns the log messages stored on the charm store. It is
possible to save them by sending POST requests to the same endpoint (see
below). For instance, the ingestion of charms/bundles produces logs that are
collected and send to the charm store by the ingestion client.

`GET /log[?limit=count][&skip=count][&id=entity-id][&level=log-level][&type=log-type]`


Each log message is defined as:

```go
type LogResponse struct {
        // Data holds the log message as a JSON-encoded value.
        Data json.RawMessage

        // Level holds the log level as a string.
        Level LogLevel

        // Type holds the log type as a string.
        Type LogType

        // URLs holds a slice of entity URLs associated with the log message.
        URLs []`*`charm.URL `json:",omitempty"`

        // Time holds the time of the log.
        Time time.Time
}
```

The log entries are ordered by last inserted (most recent logs first), and by
default the last 1000 logs are returned. Use the `limit` and skip `query`
parameters to change the default behavior. Logs can further be filtered by log
level (“info”, “warning” or “error”) and by related entity id. The type query
parameter groups entries by type. For instance, to request all the ingestion
errors related to the *utopic/django* charm, use the following URL:

`/log?type=ingestion&level=error&id=utopic/django`

#### POST /log

This endpoint uploads logs to the charm store. The request content type must be
`application/json`. The body must contain the JSON representation of a list of
logs, each one being in this format:

```go
type Log struct {
        // Data holds the log message as a JSON-encoded value.
        Data *json.RawMessage

        // Level holds the log level as a string.
        Level LogLevel

        // Type holds the log type as a string.
        Type LogType

        // URLs holds a slice of entity URLs associated with the log message.
        URLs []*charm.URL `json:",omitempty"`
}
```

Nothing is returned if the request succeeds. Otherwise, an error is returned.

### Changes

Each charm store has a global feed for all new published charms and bundles.

#### GET changes/published

This endpoint returns the ids of published charms or bundles published, most
recently published first.

`GET changes/published[?limit=count][&from=fromdate][&to=todate]`

The `fromdate` and `todate` values constrain the range of publish dates, in
"yyyy-mm-dd" format. If `fromdate` is specified only charms published on or
after that date are returned; if `todate` is specified, only charms published
on or before that date are returned. If the `limit` count is specified, it must
be positive, and only the first count results are returned. The published time
is in RFC3339 format.

```go
[]Published
type Published struct {
        Id string
        PublishTime time.Time
}
```

Example: `GET changes/published`

```json
[
    {
        "Id": "cs:trusty/wordpress-42",
        "PublishTime": "2014-07-31T15:04:05Z"
    },
    {
        "Id": "cs:trusty/mysql-11",
        "PublishTime": "2014-07-30T14:20:00Z"
    },
    {
        "Id": "cs:bundle/mediawiki",
        "PublishTime": "2014-07-29T13:45:10Z"
    }
]
```

Example: `GET changes/published?limit=10&from=31-07-2014`

```json
[
    {
        "Id": "cs:trusty/wordpress-42",
        "PublishTime": "2014-07-31T15:04:05Z"
    }
]
```
