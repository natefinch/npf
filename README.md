# juju/charmstore

Store and publish Juju charms.

## Installation

To start using the charm store, run the following:

    go get github.com/juju/charmstore

## Go dependencies

The project uses godeps (https://launchpad.net/godeps) to manage Go
dependencies. After installing the application, you can update the dependencies
to the revision specified in the `dependencies.tsv` file with the following:

    make deps

Use `make create-deps` to update the dependencies file.

## Devlopment environment

A couple of system packages are required in order to set up a charm store
development environment. To install them, run the following:

    make sysdeps

At this point, from the root of this branch, run the command::

    make install

The command above builds and installs the charm store binaries, and places them
in `$GOPATH/bin`. This is the list of the installed commands:

- charmload: populate the database with charms from Launchpad;
- charmd: start the charm store server;
- charm-admin: manage published charms.

A description of each command can be found below.

## Testing

Run `make check` to test the application.
Run `make help` to display help about all the available make targets.

## Populate the charms database

The charm store creates a MongoDB database named "juju" and stores info about
charms in the MongoDB "juju.charms" collection. Also charm files are stored in
a GridFS named "juju.charmfs".

To populate the database with the charms published in Launchpad, run the
following command:

    charmload cmd/charmd/config.yaml

Note: the operation takes a large amount of time and disk space to complete:
at the time of this writing it takes ~2:30h and ~4GB to store ~1050 charms,
but this can vary significantly based on your machine/connection speed.
The process can be stopped by typing ^C.
To check the imported charm count, you can run the following:

    mongo --eval "db.getSiblingDB('juju').charms.count()"

## Charmstore server

Once the charms database is fully populated, it is possible to interact with
charm data using the charm store server. It can be started with the following
command:

    charmd cmd/charmd/config.yaml

The same result can be achieved more easily by running `make server`.

At this point the server starts listening on port 8080 (as specified in the
config YAML file).
The server exposes the following API:

#### /charm-info

A GET call to `/charm-info` returns info about one or more charms, including
its canonical URL, revision, SHA256 checksum and VCS revision digest.
The returned info is in JSON format.
For instance a request to `/charm-info?charms=cs:trusty/juju-gui` returns the
following response:

    {"cs:trusty/juju-gui": {
        "canonical-url": "cs:trusty/juju-gui",
        "revision": 3,
        "sha256": "a15c77f3f92a0fb7b61e9...",
        "digest": jeff.pihach@canonical.com-20140612210347-6cc9su1jqjkhbi84"
    }}

#### /charm-event:

A GET call to `/charm-event` returns info about an event occurred in the life
of the specified charm(s). Currently two types of events are logged:
"published" (a charm has been published and it's available in the store) and
"publish-error" (an error occurred while importing the charm).
E.g. a call to `/charm-event?charms=cs:trusty/juju-gui` generates the following
JSON response:

    {"cs:trusty/juju-gui": {
        "kind": "published",
        "revision": 3,
        "digest": "jeff.pihach@canonicalcom-20140612210347-6cc9su1jqjkhbi84",
        "time": "2014-06-16T14:41:19Z"
    }}

#### /charm/

The `charm` API provides the ability to download a charm as a Zip archive,
given the charm identifier. For instance, it is possible to download the Juju
GUI charm by performing a GET call to `/charm/trusty/juju-gui-42`. Both the
revision and OS series can be omitted, e.g. `/charm/juju-gui` will download the
last revision of the Juju GUI charm with support to the more recent Ubuntu LTS
series.

#### /stats/counter/

Stats can be retrieved by calling `/stats/counter/{key}` where key is a query
that specifies the counter stats to calculate and return.

For instance, a call to `/stats/counter/charm-bundle:*` returns the number of
times a charm has been downloaded from the store. To get the same value for
a specific charm, it is possible to filter the results by passing the charm
series and name, e.g. `/stats/counter/charm-bundle:trusty:juju-gui`.

The results can be grouped by specifying the `by` query (possible values are
`day` and `week`), and time delimited using the `start` and `stop` queries.

It is also possible to list the results by passing `list=1`. For example, a GET
call to `/stats/counter/charm-bundle:trusty:*?by=day&list=1` returns an
aggregated count of trusty charms downloads, grouped by charm and day, similar
to the following:

    charm-bundle:trusty:juju-gui  2014-06-17  5
    charm-bundle:trusty:mysql     2014-06-17  1

## Manage published charms

The `charm-admin` command is used to manage the store contents. Currently the
only implemented sub-command is `delete-charm`, which removes a charm from
the store, e.g.:

    charm-admin delete-charm --config cmd/charmd/config.yaml --url trusty/mysql

Run `charm-admin help` for the complete command's help.
