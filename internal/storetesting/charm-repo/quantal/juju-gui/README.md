<!--
README.md
Copyright 2013-2015 Canonical Ltd.
This work is licensed under the Creative Commons Attribution-Share Alike 3.0
Unported License. To view a copy of this license, visit
http://creativecommons.org/licenses/by-sa/3.0/ or send a letter to Creative
Commons, 171 Second Street, Suite 300, San Francisco, California, 94105, USA.
-->

# Juju GUI Charm #

This charm makes it easy to deploy a Juju GUI into an existing environment.

## Supported Browsers ##

The Juju GUI supports recent releases of the Chrome, Chromium, Firefox, Safari
and Internet Explorer web browsers.

## Demo and Staging Servers ##

The Juju GUI runs a Demo environment on
[demo.jujucharms.com](http://demo.jujucharms.com).  From there,  you can browse
charms, try the GUI, and build an example environment to export for use
elsewhere.

A [staging server](http://comingsoon.jujucharms.com/) is also available,
running the latest and greatest version.

## Deploying the Juju GUI using Juju Quickstart ##

[Juju Quickstart](https://pypi.python.org/pypi/juju-quickstart) is an
opinionated command-line tool that quickly starts Juju and the GUI, whether
you've never installed Juju or you have an existing Juju environment running.

For installation on precise and utopic, you'll need to enable the Juju PPA by
first executing:

    sudo add-apt-repository ppa:juju/stable
    sudo apt-get update
    sudo apt-get install juju-quickstart

For trusty the PPA is not required and you simply need to install it with:

    sudo apt-get install juju-quickstart

At this point, just running `juju-quickstart` will deploy the Juju GUI. When
possible, Quickstart conserves resources by installing the GUI on the bootstrap
node. This colocation is not possible when using a local (LXC) environment.

Quickstart ends by opening the browser and automatically logging the user into
the GUI, to observe and manage the environment visually.
By default, the deployment uses self-signed certificates. The browser will ask
you to accept a security exception once.

## Deploying the Juju GUI the traditional way ##

Deploying the Juju GUI can be accomplished using Juju itself.

You need a configured and bootstrapped Juju environment: see the Juju docs
about [getting started](https://juju.ubuntu.com/docs/getting-started.html),
and then run the usual bootstrap command.

    juju bootstrap

Next, you simply need to deploy the charm and expose it.

    juju deploy juju-gui
    juju expose juju-gui

The instructions above cause you to use a separate machine to work with the
GUI.  If you'd like to reduce your machine footprint (and perhaps your costs),
you can colocate the GUI with the Juju bootstrap node, e.g.:

    juju deploy juju-gui --to 0

Finally, you need to identify the GUI's URL. It can take a few minutes for the
GUI to be built and to start; this command will let you see when it is ready
to go by giving you regular status updates:

    watch juju status

Eventually, at the end of the status you will see something that looks like
this:

    services:
      juju-gui:
        charm: cs:trusty/juju-gui-42
        exposed: true
        relations: {}
        units:
          juju-gui/0:
            agent-state: started
            machine: 1
            open-ports:
            - 80/tcp
            - 443/tcp
            public-address: ec2-www-xxx-yyy-zzz.compute-1.amazonaws.com

That means you can go to the public-address in my browser via HTTPS
(https://ec2-www-xxx-yyy-zzz.compute-1.amazonaws.com/ in this example), and
start configuring the rest of Juju with the GUI.  You should see a similar
web address.  Accessing the GUI via HTTP will redirect to using HTTPS.

By default, the deployment uses self-signed certificates. The browser will ask
you to accept a security exception once.

You will see a login form with the username field prefilled to "admin". The
password is the same as your Juju environment's `admin-secret`. The login
screen includes hints about where to find the environment's password.

### Deploying behind a firewall ###

When using the default options the charm uses the network connection only for
installing Deb packages from the default Ubuntu repositories. For this reason
the charm can be deployed behind a firewall in the usual way:

    juju deploy juju-gui

Network access (other than default Ubuntu repositories) is required in the case
the `juju-gui-source` option is set to a configuration that requires accessing
an external source (for instance in order to fetch a release tarball or a Git
checkout).

In these cases, it is still possible to deploy behind a firewall configuring
the charm to pull the GUI release from a location you specify.

The config variable `juju-gui-source` allows a `url:` prefix which understands
both `http://` and `file://` protocols.  We will use this to load a local copy
of the GUI source.

1. Download the latest release of the Juju GUI Source from [the Launchpad
downloads page](https://launchpad.net/juju-gui/+download) and save it to a
location that will be accessible to the *unit* either via filesystem or HTTP.
2. Set the config variable to that location using a command such as

    `juju set juju-gui juju-gui-source=url:...`

    where the ellipsis after the `url:` is your `http://` or `file://` URI.
    This may also be done during the deploy step using `--config`.

3. If you had already tried to deploy the GUI and received an install error due
to not being able to retrieve the source, you may also need to retry the unit
with the following command (using the unit the GUI is deployed on):

    `juju resolved --retry juju-gui/0`

### Upgrading the charm behind a firewall ###

When a new version of Juju GUI is released, the charm is updated to include the
new release in the local releases repository. Assuming the new version is
1.0.1, after upgrading the charm, it is possible to also upgrade to the newer
Juju GUI release by running the following:

    juju set juju-gui-source=1.0.1

In this case the new version will be found in the local repository and
therefore the charm will not attempt to connect to Launchpad.

## The Juju GUI server ##

While the Juju GUI itself is a client-side JavaScript application, the charm
installation also involves configuring and starting a GUI server, which is
required to serve the application files and to enable some advanced features,
so that using the GUI results in a seamless and powerful experience.
This server is called *GUI server* or *builtin server*.

The builtin server is already included in the charm. For this reason, it does
not require any external dependencies.
The builtin server provides the following functionalities:

1. It serves the Juju GUI static files, including support for ETags and basic
   server side URL routing.
2. It supports running the GUI over TLS (HTTPS) or in insecure mode (HTTP).
3. It redirects secure WebSocket connections established by the browser to
   the real Juju API endpoint. This way the GUI can connect the WebSocket to
   the host and port where it is currently running, so that the already
   accepted self signed certificate is reused and the connection succeeds.
4. It supports running the Juju GUI browser tests if the charm is configured
   accordingly.
5. It exposes an API for bundles deployment. This way bundles can be deployed
   very easily using the GUI, by selecting a bundle from the GUI browser or
   just dragging and dropping a bundle YAML file to the GUI canvas.
6. It allows for logging in into the GUI via a timed token. This is used, for
   instance, by Juju Quickstart to allow automatic user's authentication.
7. It supports deploying local charms by proxying browser HTTPS connections to
   the Juju HTTPS API backend. This also includes retrieving and listing local
   charms' files.
8. By default, it listens on port 443 for HTTPS secure connections, and
   redirects port 80 requests to port 443. The port where the server is
   listening can be changed using the charm configuration "port" option.

## Contacting the Developers ##

If you run into problems with the charm, please feel free to contact us on the
[Juju mailing list](https://lists.ubuntu.com/mailman/listinfo/juju), or on
Freenode's IRC network on #juju.  We're not always around (working hours in
Europe and North America are your best bets), but if you send us a mail or
ping "jujugui" we will eventually get back to you.

If you want to help develop the charm, please see the charm's `HACKING.md`.
