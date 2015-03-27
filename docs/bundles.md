# Bundles in The Charmstore

The charmstore allows two versions of bundle specifications, as described by
github.com/juju/charm.  The versions are numbered 3 and 4, relating to the API
version under which they can be hosted: charmworld (API v3) supports only
version 3 bundles, charmstore (API v4) supports version 3 and version 4.

## Version 3 bundles

Version 3 bundles are currently existing bundles that specify a deployment as a
list of services and, optionally, relations.  The charmstore will not support
the idea of a "basket" or multiple bundles within one file.  However, existing
baskets will still be imported, and split up into their component bundles.

## Version 4 bundles

Version 4 bundles are identical to version 3 bundles except for a few key
differences: the `branch` attribute of the service spec is no longer supported,
they may contain a machine specification, and their deployment directives are
different from version 3 bundles.

### Deploying version 4 bundles

Because version 4 bundles are not yet idempotent (i.e.: if a machine fails to
come up, running the bundle again will recreate all machines in the machine
spec), the juju deployer pessimistically assumes that a bundle is a version 4
bundle *only* if it has a machine spec.  This means that a bundle without a
machine spec must use the version 3 style of placement directives listed below
until further notice, when the deployer is updated.  This does not affect
version 4 bundle support within the charmstore (that is, the machine spec is
still optional).

The Juju GUI does not yet support version 4 bundles as of version 1.3.4, as the
GUI charm contains an older version of the deployer.

### Machine Specifications

A machine specification identifies a machine that will be created in the Juju
environment.  These machines are named with an integer, and can have any of
three optional attributes:

* *constraints* - Constraints are specified as a string as described by the Juju
  constraints flag (see `juju help constraints` for more information).
* *annotations* - Annotations, provided as key-value pairs, are additional
  information that is tacked onto the machine within the Juju state server.
  These can be used for marking machines for your own use, or for use by Juju
  clients.
* *series* - You may optionally specify the series of the machine to be created
  (e.g.: "precise" or "trusty").  If you do not specify a series, the bundle
  series will be used.

Machines are specified under the `machines` top-level attribute.

### Deployment directives

Version 4 deployment directives (the `to` attribute on the service spec) is a
YAML list of items following the format:

    (<containertype>:)?(<unit>|<machine>|new)

If containertype is specified, the unit is deployed into a new container of that
type, otherwise it will be "hulk-smashed" into the specified location, by
co-locating it with any other units that happen to be there, which may result in
unintended behavior.

The second part (after the colon) specifies where the new unit should be placed;
it may refer to a unit of another service specified in the bundle, a machine
id specified in the machines section, or the special name "new" which specifies
a newly created machine.

A unit placement may be specified with a service name only, in which case its
unit number is assumed to be one more than the unit number of the previous unit
in the list with the same service, or zero if there were none.

If there are less elements in To than NumUnits, the last element is replicated
to fill it. If there are no elements (or To is omitted), "new" is replicated.

For example:

    wordpress/0 wordpress/1 lxc:0 kvm:new

specifies that the first two units get hulk-smashed onto the first two units of
the wordpress service, the third unit gets allocated onto an lxc container on
machine 0, and subsequent units get allocated on kvm containers on new machines.

The above example is the same as this:

    wordpress wordpress lxc:0 kvm:new

Version 3 placement directives take the format:

    ((<containertype>:)?<service>(=<unitnumber>)?|0)

meaning that a machine cannot be specified beyond colocating (either through a
container or hulk-smash) along with a specified unit of another service.
Version 3 placement directives may be either a string of a single directive or a
YAML list of directives in the above format.  The only machine that may be
specified is machine 0, allowing colocation on the bootstrap node.

## Example Bundles

### Version 3

```yaml
series: precise
services:
  nova-compute:
    charm: cs:precise/nova-compute
    units: 3
  ceph:
    units: 3
    to: [nova-compute, nova-compute]
  mysql:
    to: 0
  quantum:
    units: 4
    to: ["lxc:nova-compute", "lxc:nova-compute", "lxc:nova-compute", "lxc:nova-compute"]
  verity:
    to: lxc:nova-compute=2
  semper:
    to: nova-compute=2
  lxc-service:
    num_units: 5
    to: [ "lxc:nova-compute=1", "lxc:nova-compute=2", "lxc:nova-compute=0", "lxc:nova-compute=0", "lxc:nova-compute=2" ]
```

### Version 4

```yaml
series: precise
services:
  # Automatically place
  nova-compute:
    charm: cs:precise/nova-compute
    units: 3
  # Specify containers
  ceph:
    units: 3
    to:
      # Specify a unit
      - lxc:nova-compute/0
      # Specify a machine
      - lxc:1
      # Create a new machine, deploy to container on that machine.
      - lxc:new
  # Specify a machine
  mysql:
    to:
      - 0
  # Specify colocation
  quantum:
    units: 4
    to:
      - ceph/1
      # Assume first unit
      - nova-compute
      # Repeats previous directive to fill out placements
machines:
  1:
    constraints: "mem=16G arch=amd64"
    annotations:
      foo: bar
    series: precise
```
