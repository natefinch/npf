+++
title = "Vanity Imports with Hugo"
date = 2015-11-22T21:00:00Z
draft = true
type = "post"
tags = ["go", "golang", "hugo"]
+++

Go get has support for vanity imports - i.e. making `import "npf.io/pie"`
actually download the code from https://github.com/natefinch/pie.  The exact way
this works is written [here](https://golang.org/cmd/go/#hdr-
Remote_import_paths), but here's a much shorter defition.  You need a webpage at
the vanity url that includes a meta tag in this format:

`<meta name="go-import" content="import-prefix vcs repo-root">`

Where import-prefix is a string that matches a prefix of the import statement
used in your code, vcs is the type of source control used, and repo-root is the
root of the VCS repo where your code lives.  For example, if I wanted to
implement the vanity path stated in the first paragraph, my meta tag would look
like this:

`<meta name="go-import" content="npf.io/pie git https://github.com/natefinch/pie">`

What's important to note here is that these should be set this way for packages
in subdirectories as well.  So, if I had a package npf.io/pie/pumpkin, the meta
tag should still be as above, since it matches a prefix of the import path, and
the root of the repo is still github.com/natefinch/pie.

Now, since you need a page at this vanity address, you might as well make it
contain documentation for your code, since one of the great thnigs about go-
gettable code is that you can just drop an import statement in your browser and
go right to the code.  Plus, you can mostly just copy and paste the markdown
from the readme in your repo anyway.

Obviously, you need this page to live at the exact same place as the import
statement... that generally will mean it needs to be in the root of your domain
(I know that I, personally don't want to see `import "npf.io/code/pie"` when I
could have `import "npf.io/pie"`).  

The easiest way to do this and keep your code organized is to put all your pages
for code into a new directory under content called "code".  Then you just need
to set the "permalink" for the code type in your config file thusly:

```toml
[Permalinks]
	code = "/:filename/"
```

Then your content's filename (minus extension) will be used as its url relative
to your site's base URL. Following the same example as above, I have
content/code/pie.md which contains the markdown for the readme of my pie
package, which hugo will now render at npf.io/pie - and thus will be where `go
get` is looking for it.

And indeed, if you go to https://npf.io/pie, you can see a readme style page,
and if you view source, you can see the meta tag in the html. Getting the meta
tag in the html is pretty easy, too.  Most of the time, you want the import-prefix value to the be same as the current page's url (that's kind of the point of `go get` right?).

You probably have some sort of a template for the header for all your pages. In
the Hyde theme that I use, it's in themes/hyde/chrome/head.html.  I just added the following code to that
