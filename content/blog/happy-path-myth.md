+++
date = "2014-11-04T06:44:32-04:00"
draft = true
title = "The Myth of the Happy Path"
type = "post"
series = ["Go Myths"]
tags = ["errors", "myths", "Go", "golang", "exceptions"]
+++

In programming, the [happy path](http://en.wikipedia.org/wiki/Happy_path) is the
code path where nothing goes wrong.  People try to optimize their code so that
the happy path is most clear, so you can see what the code is *supposed* to do.
The problem with this is, the happy path is a myth.

<img src="/nospoon.jpg">

There is no happy path.

Your code has a lot of code paths. They are all equally valid to the computer.
They all add up to what your code is *supposed* to do. They should be all
equally valid to you, the programmer, and to someone else, the person who has to
read and understand your code 18 months down the road.

Let's look at some code, and try to figure out what the happy path is:

```
// five truncates a slice of bytes to a maximum of 5 bytes.
// It returns an error if the slice shorter than 5 bytes.
func five(b []byte) ([]byte, error) {
	if len(b) > 5 {
		return b[:5], nil
	}
	return nil, fmt.Errorf("Slice is too short! (len:%d)", len(b))
}
```
Ok, so which is the happy path in this case?  Well, obviously the second path
that returns an error is the error path, right?  So the first one must be the
happy path.  Now let's look at another snippet:

```
// five truncates a slice of bytes to a length of 5 and returns any remainder.
func five(b []byte]) (five, remainder []byte) {
	if len(b) > 5 {
		return b[:5], b[5:]
	}
	return b, nil
}
```

Ok, so which return illustrates the happy path here? Can't tell? That's because
*both paths are equally valid*. But what is the difference between this code and
the code above it?  Why does one have a single happy path and one have two equally
valid paths?  **Because the happy path is a myth.**

Error handling paths are at just as valid code paths as the non-error handling
paths.  In fact, to even call them error handling paths is a misnomer.  A
missing file is not an error, it's just a valid state of your computer.  A
network interruption is not an error, it's just a fact of life.  Assuming that
any of these things won't happen, or not paying enough attention to how you
program for them is a recipe for disaster in production.

## Exceptions

Languages with exceptions swallow the myth of the happy path hook, line, and
sinker. They let you write *just* a single codepath for your "happy path" and
obfuscate the other paths. The problem being, of course, that the happy path
isn't special. Life isn't ideal, and you *will* deviate from the happy path,
probably quite often.

Except that now, your error handling code is far from the code that generates
the error.  This is the very worst thing you can do in a codebase - tie two
pieces of code together that are very closely related, but spacially far apart.
