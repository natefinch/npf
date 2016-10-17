+++
title = "Vanity Imports with Hugo"
date = 2016-10-26T00:01:00Z
type = "post"
tags = ["go", "golang", "hugo"]
+++

When working on [Gorram](https://github.com/natefinch/gorram), I decided I
wanted to release it via a vanity import path.  After all, that's half the
reason I got npf.io in the first place (an idea blatantly stolen from Russ Cox's
rsc.io).  

What is a vanity import path?  It is explained in the go get
[documentation](https://golang.org/cmd/go/#hdr-Remote_import_paths, though it
isn't given that name (or any name, unfortunately).  If you're not hosted on one
of the well known hosting sites (github, bitbucket, etc), go get has to figure
out how to get your code. How it does this is fairly ingenious - it performs an
http GET of the import path (first https then http) and looks for specific meta
elements in the page's header.  The header elements tells go get what type of
VCS is being used and what address to use to get the code.  

The great thing about this is that it removes the dependency of your code on any
one code hosting site. If you want to move your code from github to bitbucket,
you can do that without breaking anyone.

So, the first thing you need to host your own vanity imports is something that
will respond to those GET requests with the right response.  You could do
something complicated like a special web application running on a VM in the
cloud, but that costs money and needs maintenance.  Since I already had a Hugo
website (running for free on github pages), I wanted to see if I could use that.
It's a slightly more manual process, but the barrier of entry is a lot lower and
it works on any free static hosting (like github pages).

So what I want is to have `go get npf.io/gorram`, actually download the code
from https://github.com/natefinch/gorram.  For that, I need
https://npf.io/gorram to serve up this meta element:

`<meta name="go-import" content="npf.io/gorram git https://github.com/natefinch/gorram">`

or more generally:

`<meta name="go-import" content="import-prefix vcs repo-root">`

Where import-prefix is a string that matches a prefix of the import statement
used in your code, vcs is the type of source control used, and repo-root is the
root of the VCS repo where your code lives.

What's important to note here is that these should be set this way for packages
in subdirectories as well.  So, for npf.io/gorram/run, the meta tag should still
be as above, since it matches a prefix of the import path, and the root of the
repo is still github.com/natefinch/gorram.  (We'll get to how to handle
subdirectories later.)

You need a page serving that meta tag to live at the exact same place as the import
statement... that generally will mean it needs to be in the root of your domain
(I know that I, personally don't want to see `go get npf.io/code/gorram` when I
could have `go get npf.io/gorram`).  

The easiest way to do this and keep your code organized is to put all your pages
for code into a new directory under content called "code".  Then you just need
to set the "permalink" for the code type in your site's config file thusly:

```toml
[Permalinks]
	code = "/:filename/"
```

Then your content's filename (minus extension) will be used as its url relative
to your site's base URL. Following the same example as above, I have
content/code/gorram.md which will make that page now appear at npf.io/gorram.

Now, for the content.  I don't actually want to have to populate this page with
content... I'd rather people just get forwarded on to github, so that's what
we'll do, by using a refresh header.  So here's our template, that'll live under layouts/code/single.html:

```
<!DOCTYPE html>
<head>
  <meta http-equiv="content-type" content="text/html; charset=utf-8">
  <meta name="go-import" content="npf.io{{substr .RelPermalink 0 -1}} git {{.Params.vanity}}">
  <meta name="go-source" content="npf.io{{substr .RelPermalink 0 -1}} {{.Params.vanity}} {{.Params.vanity}}/tree/master{/dir} {{.Params.vanity}}/blob/master{/dir}/{file}#L{line}">
  <meta http-equiv="refresh" content="0; url={{.Params.vanity}}">
</head>
</html>
```

This will generate a page that will auto-forward anyone who hits it on to your
github account.  Now, there's one more (optional but recommended) piece - the
go-source meta header.  This is only relevant to godoc.org, and tells godoc how
to link to the sourcecode for your package (so links on godoc.org will go
straight to github and not back to your vanity url, see more details [here](https://github.com/golang/gddo/wiki/Source-Code-Links)).

Now all you need is to put a value of `vanity = https://github.com/you/yourrepo`
in the frontmatter of the correct page, and the template does the rest. If your
repo has multiple directories, you'll need a page for each directory (such as
npf.io/gorram/run).  This would be kind of a drag, making the whole directory
struture with content docs in each, except there's a trick you can do here to
make that easier.

I recently landed a change in Hugo that lets you customize the rendering of
alias pages.  Alias pages are pages that are mainly used to redirect people from
an old URL to the new URL of the same content.  But in our case, they can serve
up the go-import and go-source meta headers for subdirectories of the main code
document.  To do this, make an alias.html template in the root of your layouts
directory, and make it look like this:

```
<!DOCTYPE html><html>
    <head>
        {{if .Page.Params.vanity -}}
        <meta name="go-import" content="npf.io{{substr .Page.RelPermalink 0 -1}} git {{.Page.Params.vanity}}">
        <meta name="go-source" content="npf.io{{substr .Page.RelPermalink 0 -1}} {{.Page.Params.vanity}} {{.Page.Params.vanity}}/tree/master{/dir} {{.Page.Params.vanity}}/blob/master{/dir}/{file}#L{line}">
        {{- end}}
        <title>{{ .Permalink }}</title>
        <link rel="canonical" href="{{ .Permalink }}"/>
        <meta http-equiv="content-type" content="text/html; charset=utf-8" />
        <meta http-equiv="refresh" content="0; url={{ .Permalink }}" />
    </head>
</html>
```

Other than the stuff in the if statement, the rest is the default alias page
that Hugo creates anyway.  The stuff in the if statement is basically the same
as what's in the code template, just with an extra indirection of specifying
.Page first. 

**Note that this change to Hugo is in master but not in a release yet.  It'll be
in 0.18, but for now you'll have to build master to get it.**

Now, to produce pages for subpackages, you can just specify aliases in the front
matter of the original document with the alias being the import path under the
domain name:

`aliases = [ "gorram/run", "gorram/cli" ]`

So your entire content only needs to look like this:

```
+++
date = 2016-10-02T23:00:00Z
title = "Gorram"
vanity = "https://github.com/natefinch/gorram"
aliases = [
    "/gorram/run",
    "/gorram/cli",
]
+++
```

Any time you add a new subdirectory to the package, you'll need to add a new
alias, and regenerate the site.  This is unfortunately manual, but at least it's
a trivial amount of work.

That's it. Now go get (and godoc.org) will know how to get your code.