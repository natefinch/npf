# juju/charmstore

Store and publish Juju charms.

## Installation

To start using the charm store, run the following:

    go get -u -v -t github.com/juju/charmstore/...

## Go dependencies

The project uses godeps (https://launchpad.net/godeps) to manage Go
dependencies. After installing the application, you can update the dependencies
to the revision specified in the `dependencies.tsv` file with the following:

    make deps

Use `make create-deps` to update the dependencies file.

## Development environment

A couple of system packages are required in order to set up a charm store
development environment. To install them, run the following:

    make sysdeps

To run the elasticsearch tests you must run an elasticsearch server. If the
elasticsearch server is running at an address other than localhost:9200 then
set `JUJU_TEST_ELASTICSEARCH=<host>:<port>` where host and port provide
the address of the elasticsearch server.

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

    charmload -config cmd/charmd/config.yaml

Note: the operation takes a large amount of time and disk space to complete:
at the time of this writing it takes ~2:30h and ~4GB to store ~1050 charms,
but this can vary significantly based on your machine/connection speed.
The process can be stopped by typing ^C.
To check the imported charm count, you can run the following:

    mongo --eval "db.getSiblingDB('juju').charms.count()"

The charmload process logs errors to a charmload.err file in the current
directory of the charmload process.

## Charmstore server

Once the charms database is fully populated, it is possible to interact with
charm data using the charm store server. It can be started with the following
command:

    charmd cmd/charmd/config.yaml

The same result can be achieved more easily by running `make server`.

At this point the server starts listening on port 8080 (as specified in the
config YAML file).

