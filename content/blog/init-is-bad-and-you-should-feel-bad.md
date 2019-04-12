+++
title = "Init Is Bad and You Should Feel Bad"
date = 2019-04-12T11:04:21-04:00
type = "post"
+++

`func init()` in Go is a weird beast. It's the only function you can have
multiples of in the same package (yup, that's right... give it a try). It
gets run when the package is *imported*. And you should never use it.

Why not? Well, there's a few reasons. The main one is that init is only useful
for setting global state. I think it's pretty well accepted that global state is
bad (because it's hard to test and it makes concurrency dangerous). So, by
association init is bad, because that's all it can do.

But wait, there's more that makes it even worse. Init is run when a package is
imported, but when does a package get imported? If a imports b and b imports c
and b and c both have init functions, which one runs first? What if c has two
init functions in different files? You can find out, but it's non-obvious and it
can change if you import code differently. Not knowing the order in which code
executes is bad. Normal go code executes top to bottom in a very clear and
obvious order. There's good reason for that.

How do you test init functions? Trick question, you can't. It's not possible to
test the state of a package before init and then make sure the state after init
is correct. As soon as your test code runs, it imports the package and runs init
right away. Ok, maybe that's not 100% true, you can probably do some hackery in
init to check if you're running under `go test` and then not run the init logic...
but then your package isn't set up the way it expects, and you'd have to write a
test specifically named to run first, to test init... and that's just horrible
(and nobody does that, so it's basically always untested code).

Ok, so there's the reasons not to use it... now what do you do instead? If you
want state, use a struct. Instead of global variables on the package, use fields
on a struct. The package-level functions become methods, and the init function
becomes a constructor.

This fixes all the aforementioned problems. You get rid of global variables, so
if you have two different parts of your code using the same package, they don't
stomp on each other's settings etc. You can run tests without worrying that a
previous test modifies global state for a later test. It's clear and obvious how
to test before and after a constructor gets called. And finally, there's a clear
and normal order to the initialization of things. You don't have to wonder what
gets called when, because it's just normal go functions.

As a corollary... this means you shouldn't use underscore imports either (since
they're generally only useful for triggering init functions). These imports
(`import _ "github.com/foo/db"`) are used for their side effects, like
registering sql/db drivers. The problem is that these are, by definition,
setting global variables, and those are bad, as we've said. So don't use those
either.

Once you start writing code with structs instead of globals and init, you'll
find your code is much easier to test, easier to use concurrently, and more
portable between applications. So, don't use init.