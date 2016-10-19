+++
date = "2016-10-18T22:08:09-04:00"
title = "Writing Go Applications with Reusable Logic"
type = "post"
tags = ["Go", "programming", "golang"]
series = ["Writing Go Applications"]
+++

Writing libraries in Go is a relatively well-covered topic, I think... but I see
a lot fewer posts about writing commands.  When it comes down to it, all Go code
ends up in a command.  So let's talk about it!  This will be the first in a
series, since I ended up having a lot more to say than I realized.

Today I'm going to focus on basic project layout, with the aims of optimizing
for reusability and testability.

There are three unique bits about commands that influence how I structure my
code when writing a command rather than a library:

## Package main

This is the only package a go program must have.  However, aside from telling
the go tool to produce a binary, there's one other unique thing about package
main - no one can import code from it.  That means that any code you put in
package main can not be used directly by another project, and that makes the OSS
gods sad.  Since one of the main reasons I write open source code is so that
other developers may use it, this goes directly against my desires.

There have been many times when I've thought "I'd love to use the logic behind X
Go binary as a part of my code".  If that logic is in package main, you can't.

## os.Exit

If you care about producing a binary that does what users expect, then you
should care about what exit code your binary exits with.  The only way to do
that is to call os.Exit (or call something that calls os.Exit, like log.Fatal).

However, you can't test a function that calls os.Exit.  Why?  Because calling
os.Exit during a test *exits the test executable*.  This is quite hard to figure
out if you end up doing it by accident (which I know from personal experience).
When running tests, no tests actually fail, the tests just exit sooner than they
should, and you're left scratching your head.

The easiest thing to do is *don't call os.Exit*.  Most of your code shouldn't be
calling os.Exit anyway... someone's going to get real mad if they import your
library and it randomly causes their application to terminate under some
conditions.

So, only call os.Exit in exactly one place, as near to the "exterior" of your
application as you can get, with minimal entry points.  Speaking of which...

## func main()

It's is the one function all go commands must have.  You'd think that
everyone's func main would be different, after all, everyone's application is
different, right?  Well, it turns out, if you really want to make your code
testable and reusable, there's really only approximately one right answer to
"what's in your main function?" 

In fact, I'll go one step further, I think there's only approximately one right
answer to "what's in your package main?" and that's this:

```go
// command main documentation here.
package main

import (
    "os"

    "github.com/you/proj/cli"
)
func main{
    os.Exit(cli.Run())
}
```

That's it.  This is approximately the most minimal code you can have in a useful
package main, thereby wasting no effort on code that others can't reuse.  We
isolated os.Exit to a single line function that is the very exterior of our
project, and effectively needs no testing.

## Project Layout

Let's get a look at the total package layout:

```
/home/you/src/github.com/you/proj $ tree
.
├── cli
│   ├── parse.go
│   ├── parse_test.go
│   └── run.go
├── LICENSE
├── main.go
├── README.md
└── run
    ├── command.go
    └── command_test.go
```

We know what's in main.go... and in fact, main.go is the only go file in the
main package. LICENSE and README.md should be self-explanatory. (Always
use a license!  Otherwise many people won't be able to use your code.)

Now we come to the two subdirectories, run and cli.

### CLI

The cli package contains the command line parsing logic.  This is where you
define the UI for your binary.  It contains flag parsing, arg parsing, help
text, etc.

It also contains the code that returns the exit code to func main (which gets
sent to os.Exit).  Thus, you can test exit codes returned from those functions,
instead of trying to test exit codes your binary as a whole produces.

### Run

The run package contains the meat of the logic of your binary.  You should write
this package as if it were a standalone library.  It should be far removed from
any thoughts of CLI, flags, etc.  It should take in structured data and return
errors.  Pretend it might get called by some other library, or a web service, or
someone else's binary.  Make as few assumptions as possible about how it'll be
used, just as you would a generic library.

Now, obviously, larger projects will require more than one directory.  In fact,
you may want to split out your logic into a separate repo.  This kind of depends
on how likely you think it'll be that people want to reuse your logic.  If you
think it's highly likely, I recommend making the logic a separate directory. In
my mind, a separate directory for the logic shows a stronger committment to
quaity and stability than some random directory nestled deep in a repo
somewhere.

## Putting it together

The cli package forms a command line frontend for the logic in the run package.
If someone else comes along, sees your binary, and wants to use the logic behind
it for a web API, they can just import the run package and use that logic
directly.  Likewise, if they don't like your CLI options, they can easily write
their own CLI parser and use it as a frontend to the run package.  

This is what I mean about reusable code.  I never want someone to have to hack
apart my code to get more use out of it.  And the best way to do that is to
separate the UI from the logic.  This is the key part.  **Don't let your UI
(CLI) concepts leak into your logic.**  This is the best way to keep your logic
generic, and your UI manageable.

### Larger Projects

This layout is good for small to medium projects.  There's a single binary that
is in the root of the repo, so it's easier to go-get than if it's under multiple
subdirectories.  Larger projects pretty much throw everything out the window. 
They may have multiple binaries, in which case they can't all be in the root of
the repo.  However, such projects usually also have custom build steps and
require more than just go-get (which I'll talk about later).

More to come soon.