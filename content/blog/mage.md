+++
title = "Mage"
date = 2018-09-19T18:50:51-04:00
type = "post"
+++

A question came up at the Framingham Go meetup a while back about why something
like Gradle hasn't taken hold in the Go community.  I can't say that I know for
sure what the answer is - I don't speak for the community - but, I have some
guesses. I think part of it is that many projects don't need a full-fledged
build tool - for your typical Go networked server or CLI tool, a single binary
built with `go build` is probably fine. 

For more complex builds, which may require more steps than just compile and
link, like for bundling static assets in a web server or generating code from
protobufs, for example, many people in the Go community reach for Make.
Personally, I find that unfortunate.  Make is not Windows friendly, and it has
its own language and conventions that you need to learn on top of the oddity
that is Bash scripting.  Finally, it doesn't let you leverage the Go community's
two greatest resources - go programmers and go code.

---

The above is a blog post I've had half written for two years.  I started to go
on to recommend using `go run make.go` with a go file that does the build for
you.  But in practice, this is problematic.  If you want your script to be
useful for doing more than one thing, you need to implement a CLI and
subcommands.  This ends up being a significant amount of work that then obscures
what the actual code is doing... and no one wants to maintain yet another CLI
just for development tasks.  In addition, there's a lot of chaff you have to
handle, like printing out errors, setting up logging etc.

Last summer there were a couple questions on
[r/golang](https://reddit.com/r/golang) about best practices for using Makefiles
with Go... and I finally decided I'd had enough.  Makefiles are clearly pretty
cool for a number of reasons (built-in CLI, dependencies, file targets). 

I looked around at what existed for alternatives -
[rake](https://github.com/ruby/rake) was the obvious pattern to follow, being
very popular in the Ruby community. [pyinvoke](http://www.pyinvoke.org/) was the
closest equivalent I saw in python.  Was there something similar in Go?  Well,
sort of, but not exactly.  [go-task](https://github.com/go-task/task) is
*written* in Go, but tasks are actually defined in YAML.  Not my
cup of tea.  Mark Bates wrote [grift](https://github.com/markbates/grift) which
has tasks written in Go, but I didn't really like the ergonomics... I wanted
just a little more magic.

I decided that I could write a tool that behaved pretty similarly to Make, but
allowed you to write Go instead of Bash, and didn't need any special syntax, if
I did a little code parsing and generation on the fly.  Thus, Mage was born.

## What is Mage?

Mage is conceptually just like Make, except you write Go instead of Bash.  Of
course, there's a little more to it than that. In Mage, like in Make, you write
targets that can be accessed via a simple CLI.  In Mage, exported functions
become targets.  Any of these exported functions are then runnable by running
`mage <func_name>` in the directory where the magefile lives, just like you'd run
`make <target_name>` for a make target.

## What is a Magefile?

A magefile is simply a .go file with the mage build tag in it.  All you need for
a magefile is this:

```go
//+build mage

package main
```

Mage looks for all go files in the current directory with the `mage` build tag,
and compiles them all together with a generated CLI.

There are a few nice properties that result from using a build tag to mark
magefiles - one is that you can use as many files as you like named whatever you
like.  Just like in normal go code, the files all work together to create a
package.

Another really nice feature is that your magefiles can live side by side with
your regular go code.  Mage only builds the files with the mage tag, and your
normal go build only builds the files *without* the mage tag.

## Targets

A function in a magefile is a target if it is exported and has a signature of
`func()`, `func()error`, `func(context.Context)`, or
`func(context.Context)error`.  If the target has an error return and you return
an error, Mage will automatically print out the error to its own stderr, and
exit with a non-zero error code.

Doc comments on each target become CLI docs for the magefile, doc comments on
the package become top-level help docs.

```go
//+build mage

// Mostly this is used for building the website and some dev tasks.
package main

// Builds the website.  If needed, it will compact the js as well.
func Build() error {
   // do your stuff here
   return nil
}
```

Running mage with no arguments (or `mage -l` if you have a default target
declared) will print out help text for the magefiles in the current directory.

```plain
$ mage
Mostly this is used for building the website and some dev tasks.

Targets:
 build    Builds the website.
```

The first sentence is used as short help text, the rest is available via `mage
-h <target>`

```plain
$ mage -h build
mage build:

Builds the website.  If needed, it will compact the js as well.
```

This makes it very easy to add a new target to your magefile with proper
documentation so others know what it's supposed to do.

You can declare a default target to run when you run mage without a target very
easily:

```go
var Default = Build
```

And just like Make, you can run multiple targets from a single command... `mage
build deploy clean` will do the right thing.

## Dependencies

One of the great things about Make is that it lets you set up a tree of
dependencies/prerequisites that must execute and succeed before the current
target runs.  This is easily done in Mage as well.  The
`github.com/magefile/mage/mg` library has a `Deps` function that takes a list of
dependencies, and runs them in parallel (and any dependencies they have), and
ensures that each dependency is run exactly once and succeeds before continuing.

In practice, it looks like this:

```go
func Build() error {
   mg.Deps(Generate, Protos)
   // do build stuff
}

func Generate() error {
   mg.Deps(Protos)
   // generate stuff
}

func Protos() error {
   // build protos
}
```

In this example, build depends on generate and protos, and generate depends on
protos as well.  Running build will ensure that protos runs exactly once, before
generate, and generate will run before build continues.  The functions sent to
Deps don't have to be exported targets, but do have to match the same signature
as targets have (i.e. optional context arg, and optional error return).

## Shell Helpers

Running commands via os/exec.Command is cumbersome if you want to capture
outputs and return nice errors.  `github.com/magefile/mage/sh` has helper
methods that do all that for you.  Instead of errors you get from exec.Command
(e.g. "command exited with code 1"), `sh` uses the stderr from the command as
the error text. 

Combine this with the automatic error reporting of targets, and you easily get
helpful error messages from your CLI with minimal work:

```go
func Build() error {
   return sh.Run("go", "build", "-o", "foo.out")
}
```

## Verbose Mode

Another nice thing about the `sh` package is that if you run mage with `-v` to
turn on verbose mode, the `sh` package will print out the args of what commands
it runs.  In addition, mage sets up the stdlib `log` package to default to
discard log messages, but if you run mage with -v, the default logger will
output to stderr. This makes it trivial to turn on and off verbose logging in
your magefiles.

## How it Works

Mage parses your magefiles, generates a main function in a new file (which
contains code for a generated CLI), and then shoves a compiled binary off in a
corner of your hard drive.  The first time it does this for a set of magefiles,
it takes about 600ms.  Using the go tool's ability to check if a binary needs to
be rebuilt or not, further runs of the magefile avoid the compilation overhead
and only take about 300ms to execute.  Any changes to the magefiles or their
dependencies cause the cached binary to be rebuilt automatically, so you're
always running the newest correct code.

Mage is built 100% with the standard library, so you don't need to install a
package manager or anything other than go to build it (and there are binary
releases if you just want to curl it into CI).

## Conclusion

I've been using Mage for all my personal projects for almost a year and for
several projects at Mattel for 6 months, and I've been extremely happy with it.
It's easy to understand, the code is plain old Go code, and it has just enough
helpers for the kinds of things I generally need to get done, taking all the
peripheral annoyances out of my way and letting me focus on the logic that needs
to be right.

Give it a try, file some issues if you run into anything.  Pull requests more
than welcome.