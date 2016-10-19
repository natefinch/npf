+++
type="post"
title="Commands part II"
date = "2016-10-18T23:08:09-04:00"
draft = true
series=["Writing Go Commands"]
+++


So, we abstracted away one of the outputs of the application... what about the
other inputs and outputs?  There's actually only a few standard ones: for input
we have the arguments and stdin, for outputs we have the exit code (already
handled) and stderr/stdout.  You don't *have* to abstract these away for
testing, but it does make things a lot easier (stderr and stdout being an
*os.File really makes mocking them out a pain).... plus, as we all know, globals
are evil, and what are os.Stdout and friends but globals?  Let's fix that right
now:

```go
func main{
    os.Exit(app{
        Args: os.Args,
        Stderr: os.Stderr,
        Stdout: os.Stdout,
        Stdin: os.Stdin,
    }.Run())
}

type app struct {
    Args []string
    Stderr io.Writer
    Stdout io.Writer
    Stdin io.Reader
}

func (a app) Run() int {
     // all your code   
}
```

There, now you have all the main inputs and outputs abstracted away from your
code, you no longer need to rely on global variables, and that'll make testing
loads easier.  You can test the main entrypoint of your code and verify all
behavior without having to resort to really gnarly test code that actually runs
your application (yes, I've seen tests that do that).

## Project Layout

If your project is just a single executable, please put the main package in the
root of the repo.  This makes it easily go-gettable.  

If your application consists of multiple executables and just one is the
"client" application (i.e. the one that most people will use from their
desktops), please put that one in the root of the repo, and put the others in
subdirectories.  Again, this makes go-get just work for the common case.

## Building

Don't use makefiles.  They're exceedingly unfriendly for Windows users, and
generally indicate your build process is too complicated.  If the makefile just
wraps the usual go tool commands (go install etc), then it just makes it look
like the maintainers don't know Go well enough to type out the very simple build
& test commands.

With the recent addition of officially supported vendoring solutions, there's 









If you really must have a build process that is more complicated than go install
(and I realize sometimes this is the case for bigger projects, or projects with
resources external to go code), then I recommend having a go script in the root
of your repo which can be run using "go run".

The most straight forward thing to do with your application is to put all the
code in package main.  Please resist doing this.  The code in package main
cannot be imported by other packages, and thus cannot be reused by others.  If
someone sees your application and wants to use part of its functionality in
their application, they won't be able to... they'll have to resort to major
hacking of your code to reuse the main logic.

Instead, put the main logic as a reusable library.  In fact, you may want to
write the code as if it were a library first, and then write a CLI adapter for
it later.  You'll likely get a cleaner API and make the code easier to test.  
This approach also cleanly separates the concerns between the domain logic, and
application=specific logic.  Parsing CLI flags? Application logic.  The meat of
the algorithm? Domain logic.

The real question is - do you put the CLI code in the same repo as the library code?  

## Flags

Most CLI commands use flags to configure the application. 

## Logging

The main goal for your application's startup sequence should be to run *as
little code as possible* before getting logging set up.  Remember, anything that
goes wrong before logging is set up is going to be a giant pain in the butt to
debug.  For applications that simply write to stderr for logging, this is simple
- there is no configuration (at least as far as where logging should be
written). 

For applications that require more configuration, this is essential to get
right.  This is a place where I think it is prudent to throw abstractions and
reusable code out the window.  If you have to do a huge initialization process
before being able to get a viable logging configuration set up, then you're
going to have a hellish time trying to debug that initialization process with
zero logging available from it.  Hack a special direct path to just slurp up the
logging config from wherever it comes from... and consider with much skepticism
any proposal that requires too much complexity in order to retrieve that
configuration.
