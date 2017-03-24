+++
title = "Make.go"
type = "post"
draft = true
date = "2016-12-14T03:55:43+01:00"

+++

A question came up at the Framingham Go meetup tonight about why something like
Gradle hasn't taken hold in the Go community.  I can't say that I know for sure
what the answer is - I don't speak for the community - but, I have some guesses.
I think part of it is that many projects don't need a full-fledged build tool -
for your typical Go networked server, a single binary built with `go build` is
probably fine.  For more complex builds, which may require more steps than just
compile and link, like for bundling static assets for a web server, for example,
many people in the Go community reach for Make.  Personally, I find that
unfortunate.  Make is not Windows friendly, and it has its own language and
conventions that you need to learn.

I think there's a simpler, better way for Go projects - a make.go file.  This is
a file containing go code that knows how to run your build.  

A make.go file is incredibly easy to create.  Add a file called make.go to the
root of your project (or wherever you feel appropriate, but I like the root of
the project because then it's easy to find).  Give the file a build tag along
the lines of

```
//+build dontbuildme
```

The actual build tag isn't important, so long as it's something you'd never
think of using as a real tag.  Its point is to exlcude this file when you run
`go build ./...` on your project directory, so it won't be accidentally included
in your executable.

Now give it a `func main()` like any other command line application, and away
you go.  When you want to build the project, just run `go run make.go` - go run
ignores the build tag, and just builds the files you specify on the command
line. Bam, done.

I first saw this used by Brad Fitzpatrick's Camlistore project, and I think it's
brilliant.  It follows the same thinking as writing unit tests - tests should
just be go code, like anything else. So why not the same for your build code?
There's nothing Gradle or Make can do that you can't do with plain old go code.
It gives you an infinitely extensible language that doesn't impose any
unecessary restrictions, and will never coerce you into making contortions to fit
its model of how it thinks you should assemble a project.

