+++
title = "Unused Variables in Go"
date = 2014-03-28T16:13:00Z
updated = 2014-04-16T15:36:33Z
tags = ["Go", "programming", "golang"]
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

The Go compiler treats unused variables as a compilation error. This causes much
annoyance to some newbie Gophers, especially those used to writing languages
that aren't compiled, and want to be able to be fast and loose with their code
while doing exploratory hacking.

The thing is, an unused variable is often a bug in your code, so pointing it out
early can save you a lot of heartache.

Here's an example:
```go
50 func Connect(name, port string) error {
51     hostport := ""
52    if port == "" {
53        hostport := makeHost(name)
54        logger.Infof("No port specified, connecting on port 8080.")
55    } else {
56        hostport := makeHostPort(name, port)
57        logger.Infof("Connecting on port %s.", port)
58    }
59    // ... use hostport down here
60 }
```

Where's the bug in the above?  Without the compiler error, you'd run the code
and have to figure out why hostport was always an empty string.  Did we pass in
empty strings by accident?  Is there a bug in makeHost and makeHostPort?

With the compiler error, it will say "53, hostport declared and not used" and
"56, hostport declared and not used"

This makes it a lot more obvious what the problem is... inside the scope of the
if statement, := declares new variables called hostport.  These hide the
variable from the outer scope, thus, the outer hostport never gets modified,
which is what gets used further on in the function.

```go 
50 func Connect(name, port string) error {
51    hostport := ""
52    if port == "" {
53        hostport = makeHost(name)
54        logger.Infof("No port specified, connecting on port 8080.")
55    } else {
56        hostport = makeHostPort(name, port)
57        logger.Infof("Connecting on port %s.", port)
58    }
59    // ... use hostport down here
60 }
```

The above is the corrected code. It took only a few seconds to fix, thanks to
the unused variable error from the compiler.  If you'd been testing this by
running it or even with unit tests... you'd probably end up spending a non-
trivial amount of time trying to figure it out.  And this is just a very simple
example.  This kind of problem can be a lot more elaborate and hard to find.

And that's why the unused variable declaration error is actually a good thing.
If a value is important enough to be assigned to a variable, it's probably a bug
if you're not actually using that variable.

**Bonus tip:**

Note that if you don't care about the variable, you can just assign it to the
empty identifier directly:

```go
_, err := computeMyVar()
```

This is the normal way to avoid the compiler error in cases where a function
returns more than you need.

If you *really* want to silence the unused variable error and not remove the
variable for some reason, this is the way to do it: 

```go 
v, err := computeMyVar() 
_ = v  // this counts as using the variable 
```

Just don't forget to clean it up before committing.

All of the above also goes for unused packages.  And a similar tip for silencing
that error:

```go
_ = fmt.Printf // this counts as using the package
```
