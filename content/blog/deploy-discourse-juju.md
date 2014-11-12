+++
date = "2014-10-01T06:31:49-04:00"
title = "Deploy Discourse with Juju in 8 minutes"
type = "post"
tags = ["juju", "discourse"]
+++

[Steve Francia](http://stevefrancia.com/) asked me to help him get
[Discourse](https://discourse.org) deployed as a place for people to discuss
[Hugo](http://gohugo.io), his static site generator (which is what I use to
build this blog).  If you don't know Discourse, it's pretty amazing forum
software with community-driven moderation, all the modern features you expect
(@mentions, SSO integration, deep email integration, realtime async updates, and
a whole lot more).  What I ended up deploying is now at
[discuss.gohugo.io](http://discuss.gohugo.io).

I'd already played around with deploying Discourse about six months ago, so I
already had an idea of what was involved.  Given that I work on
[Juju](http://juju.ubuntu.com) as my day job, of course I decided to use Juju to
deploy Discourse for Steve.  This involved writing a Juju *charm* which is sort
of like an install script, but with hooks for updating configuration and hooks
for interacting with other services. I'll talk about the process of writing the
charm in a later post, but for now, all you need to know is that it follows the
official [install guide](https://github.com/discourse/discourse/blob/master/docs/INSTALL-digital-ocean.md) for installing Discourse.

The install guide says that you can install Discourse in 30 minutes.  Following
it took me a **lot** longer than that, due to some confusion about what the
install guide really wanted you to do, and what the install really required.
But you don't need to know any of that to use Juju to install Discourse, and you
can get it done in 8 minutes, not 30.  Here's how:

First, install Juju:

    sudo add-apt-repository -y ppa:juju/stable
    sudo apt-get update && sudo apt-get install -y juju-core

Now, Juju does not yet have a provider for Digital Ocean, so we have to use a
plugin to get the machine created.  We're in the process of writing a provider
for Digital Ocean, so soon the plugin won't be necessary.  If you use another
cloud provider, such as AWS, Azure, HP Cloud, Joyent, or run your own Openstack
or MAAS, you can easily [configure Juju](https://juju.ubuntu.com/docs/getting-a
started.html#configuring) to use that service, and a couple of these steps will
not be necessary.  I'll post separate steps for that later.  But for now, let's
assume you're using Digital Ocean.

Install the juju [Digital Ocean plugin](https://github.com/kapilt/juju-digitalocean):

    sudo apt-get install -y python-pip
    pip install -U juju-docean

Get your Digital Ocean [access info](https://cloud.digitalocean.com/api_access)
and set the client id in an environment variable called DO_CLIENT_ID and the API
key in an environment variable called DO_API_KEY.

Juju requires access with an SSH key to the machines, so make sure you have one
set up in your Digital Ocean account.

Now, let's create a simple configuration so juju knows where you want to deploy
your new environment.

    juju init

Running juju init will create a boilerplate configuration file at
~/.juju/environments.yaml.  We'll append our digital ocean config at the bottom:

	echo "    digitalocean:
	        type: manual
	        bootstrap-host: null
	        bootstrap-user: root
	" >> ~/.juju/environments.yaml

Note that this is yaml, so the spaces at the beginning of each line are
important.  Copy and paste should do the right thing, though.

Now we can start the real fun, let's switch to the digitalocean environment we
just configured, and create the first Juju machine in Digital Ocean:

	juju switch digitalocean
	juju docean bootstrap --constraints="mem=2g, region=nyc2"

(obviously replace the region with whatever one you want)

Now, it'll take about a minute for the machine to come up.

Discourse *requires* email to function, so you need an account at
[mandrill](http://mandrill.com), [mailgun](http://mailgun.com), etc.  They're free, so
don't worry.  From that account you need to get some information to properly set
up Discourse.  You can do this after installing discourse, but it's faster if
you do it before and give the configuration at deploy time. (changing settings
later will take a couple minutes while discourse reconfigures itself)

When you deploy discourse, you're going to give it a configuration file, which
will look something like this:

    discourse:
      DISCOURSE_HOSTNAME: discuss.example.com
      DISCOURSE_DEVELOPER_EMAILS: foo@example.com,bar@example.com
      DISCOURSE_SMTP_ADDRESS: smtp.mailservice.com
      DISCOURSE_SMTP_PORT: 587
      DISCOURSE_SMTP_USER_NAME: postmaster@example.com
      DISCOURSE_SMTP_PASSWORD: supersecretpassword
      UNICORN_WORKERS: 3

The first line must be the same as the name of the service you're deploying.  By
default it's "discourse", so you don't need to change it unless you're deploying
multiple copies of discourse to the same Juju environment.  And remember, this
is yaml, so those spaces at the beginning of the rest of the lines are
important.

The rest should be pretty obvious.  Hostname is the domain name where your site
will be hosted.  This is important, because discourse will send account
activation emails, and the links will use that hostname.  Developer emails are
the email addresses of accounts that should get automatically promoted to admin
when created.  The rest is email-related stuff from your mail service account.
Finally, unicorn workers should just stay 3 unless you're deploying to a machine
with less than 2GB of RAM, in which case set it to 2.

Ok, so now that you have this file somewhere on disk, we can deploy discourse.
Don't worry, it's really easy.  Just do this:

	juju deploy cs:~natefinch/trusty/discourse --config path/to/configfile --to 0
	juju expose discourse

That's it. If you're deploying to a 2GB Digital Ocean droplet, it'll take about
7 minutes.

To check on the status of the charm deployment, you can do `juju status`, which
will show, among other things "agent-state: pending" while the charm is being
deployed.  Or, if you want to watch the logs roll by, you can do `juju debug-
log`.

Eventually juju status will show `agent-state: started`.  Now grab the ip
address listed at `public address:` in the same output and drop that into your
browser.  Bam!  Welcome to Discourse.

If you ever need to change the configuration you set in the config file above,
you can do that by editing the file and doing

	juju set discourse --config=/path/to/config

Or, if you just want to tweak a few values, you can do 

	juju set discourse foo=bar baz=bat ...

Note that every time you call juju set, it'll take a couple minutes for
Discourse to reconfigure itself, so you don't want to be doing this over and
over if you can hep it.

Now you're on your own, and will have to consult the gurus at
[discourse.org](discourse.org) if you have any problems.  But don't worry, since
you deployed using Juju, which uses their official install instructions, your
discourse install is just like the ones people deploy manually (albeit with a
lot less time and trouble).

Good Luck!

Please let me know if you find any errors in this page, and I will fix them
immediately.