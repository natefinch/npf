+++
date = "2014-10-02T17:28:22-04:00"
title = "Everything That's Wrong With Go"
type = "post"
draft = true
+++

Wherein I use profanity to dismiss most complaints about Go.

> There's no package manager!

So, there is a package manager, it's called your friendly neighborhood VCS.
This is actually fucking brilliant.  There's no single point of failure.  Anyone
with a server that runs git/mercurial/bazaar/svn is instantly their own package
distributor.  There's no more dichotomy between where the code is actively
worked on and where it's distributed from.  They are one and the same.  It also
means that the URL to get the code, the string for the import path, and the path
to find the code on your disk are __all the same__.  Fucking brilliant.

> Versioning is a nightmare!

No, it's not.  This is actually three topics, so I'll break it up for you:

> 1.) `go get` always pulls from master, so you can't version your code.

This is just bullshit.  There are a 100 ways to version your code, just by
encoding the version in your package's import path.  It's trivial to move your
foo package to v2 by creating a new repo called github.com/yourname/foo.v2.  Bam
done.  If you want to get fancy, you can use [gopkg.in](http://gopkg.in) to
redirect to different branches of the same repo (you can self-host this if you
want to use your own URL and/or not depend on an external service).

> 2.) You can't make reproducible builds!

There are a bunch of tools to help you with this.  The two I'd most recommend
are, unfortunately, named almost the exact same thing:
[godeps](https://launchpad.net/godeps) and [godep](https://github.com/).  The
first is purely revision-pinning.  i.e. it is used to set all the repos your
code depends on to a specific commit number, so that every build uses the exact
same code.  The second can be used as revision pinning or to "vendor" all your
dependencies.  "Vendoring" in this context means to copy all the code for your
dependencies into your own repo, so that even if the other repos go away, your
project will still always build.

I think vendoring does not generally hit the ROI benchmark.  If any of your
dependencies *do* go away, you will almost certainly still have N copies of that
code, where N is the number of developers who have recently checked out your
project.  Thus you can recover from disaster by hosting the code in a repo you
control, and changing your imports to point to that.

> 3.) You can't automatically handle version conflicts!

Newsflash: neither can &lt;insert package manager&gt;.  Even if you have
complicated version matching instructions like "package foo >= 1.2", you can
still have two dependencies that clash, if another one requires "package foo <=
1.1".  In addition, the idea that any piece of code is trivially compatible with
vast numbers of revisions of other pieces of code is quite laughable.  There's
only one way to know if two pieces of code really work together - test them.

At least in Go, two different versions of the same package do not produce types
that seem compatible.  The Foo type from package foo is not compatible with
functions that expect the Foo type from package foo.v2, *even if they have the
exact same signature*.  They're from different packages, so they're *different
types*.  This means that if you do import two versions of the same package,
you'll get compile errors if they attempt to get treated as the same type.  If
there's no compile errors, then the most you have to worry about is conflicting
init functions (such as two packages that try to use the same system resource -
like a port or file).

> There's no generics, so you can't write reusable code!

So, first off, this is partially simply untrue - there are generics: generic
hashmaps, arrays, lists, and thread-safe queues.  There are no user-created
generic functions or types.  This is true.

Read this first: [Esmerelda's
Imagination](http://commandcenter.blogspot.com.au/2011/12/esmereldas-
imagination.html).  What, you're not going to read a 2011 blog post from [some
random guy](http://en.wikipedia.org/wiki/Rob_Pike)?  Ok, let me summarize:
Actress says "I can't imagine being anything other than an actress" wiseass
replies: "Then you can't be much of an actress, can you?"  Applied to
programming it means, if you can't think of ways to get around not having
generics, you're not really trying.

Anyone who says that you can't write reusable code without generics is just
blatantly wrong.  Want to see some reusable code?  How about the [standard
fucking library](http://golang.org/pkg/)?  Giant fucking piece of code reused by
every Go program ever written.

So what you really mean is:

> I can't write a (high performance type-safe) generic container in Go (without duplicate code).

This is true.  If you absolutely need a high performance, type-safe generic tree
implementation, and code generation is anathema to you, you should probably look
somewhere else.  However, you can write a perfectly good generic tree
implementation and then just use code generation to make it specific to the type
you need.  There was even a [very clever post](http://bouk.co/blog/idiomatic-
generics-in-go/) about how to do this through the magic of go-get recently. Yes,
this means you'll have SetInt instead of Set&lt;int&gt;.  Boo hoo. It's the same
information, just different symbols.  Hell, this is what C++ templates do, and
no one* complains about that.

Or you can make a generic implementation using interface{} and then write a
type-safe wrapper around it for each instance you need, casting on the way out.
Java does it this way using type erasure, and no one* complains about that
either.

> Go is for people stuck in the 70's and who were happy with Java 1.0!

Ok fuckwad, now you're just trolling.  But I'll feed the troll, this one time.
First off, the programmers of the 70's actually knew quite a lot.  They designed
things like C, and Unix.  Later they designed things like UTF-8 and Go.

So, yes, Go may seem like it's a throwback to the 70's, since it does not include many of complex programming ideas that 
