+++
date = "2015-10-10T00:00:19-04:00"
draft = false
title = "Returning Errors"
type = "post"
tags = ["go", "golang", "errors", "error handling"]
+++

There are basically two ways to return errors in Go:

```go
func (c Config) Save() error {
	if err := c.checkDefault(); err != nil {
		return err
	}
	...
}
```

or

```go
func (c Config) Save() error {
	if err := c.checkDefault(); err != nil {
		return fmt.Errorf("can't find default config file: %v", err)
	}
	...
}
```

The former passes the original error up the stack, but adds no context to it.
Thus, your saveConfig function may end up printing "file not found:
default.cfg" without telling the caller why it was trying to open default.cfg.

The latter allows you to add context to an error, so the above error could
become "can't find default config file: file not found: default.cfg".
This gives nice context to the error, but unfortunately, it creates an entirely
new error that only maintains the error string from the original.  This is fine
for human-facing output, but is useless for error handling code.

If you use the former code, calling code can then use `os.IsNotExist()`, figure
out that it was a not found error, and create the file.  Using the latter code,
the type of the error is now a different type than the one from os.Open, and
thus will not return true from os.IsNotExist.  Using fmt.Errorf effectively
masks the original error from calling code (unless you do ugly string parsing -
please don't).

Sometimes it's good to mask the original error, if you don't want your callers
depending on what should be an implementation detail (thus effectively making it
part of your API contract). However, lots of times you may want to give your
callers the ability to introspect your errors and act on them. This then loses
the opportunity to add context to the error, and so people calling your code
have to do some mental gymnastics (and/or look at the implementation) to
understand what an error really means.

A further problem for both these cases is that when debugging, you lose all
knowledge of where an error came from.  There's no stack trace, there's not even
a file and line number of where the error originated.  This can make debugging
errors fairly difficult, unless you're careful to make your error messages easy
to grep for.  I can't tell you how often I've searched for an error formatting
string, and hoped I was guessing the format correctly.

This is just the way it is in Go, so what's a developer to do?  Why, write an
errors library that does smarter things of course!  And there are a ton of these
things out there.  Many add a stack trace at error creation time.  Most wrap an
original error in some way, so you can add some context while keeping the
original error for checks like os.IsNotExist. At Canonical, the Juju team wrote
just such a library (actually we wrote 3 and then had them fight until only one
was standing), and the result is https://github.com/juju/errors.

Thus you might return an error this way:

```go
func (c Config) Save() error {
	if err := c.checkDefault(); err != nil {
		return errors.Annotatef(err, "can't find default config file")
	}
}
```

This returns a new error created by the errors package which adds the given
string to the front of the original error's error message (just like
fmt.Errorf), but you can introspect it using `errors.Cause(err)` to access the
original error return by checkDefault.  Thus you can use
`os.IsNotExist(errors.Cause(err))` and it'll do the right thing.

However, this and every other special error library suffer from the same problem
- your library can only understand its own special errors.  And no one else's
code can understand your errors (because they won't know to use errors.Cause
before checking the error).  Now you're back to square one - your errors are
just as opaque to third party code as if they were created by fmt.Errorf.

I don't really have an answer to this problem. It's inherent in the
functionality (or lack thereof) of the standard Go error type.  

Obviously, if you're writing a standalone package for many other people to use,
don't use a third party error wrapping library.  Your callers are likely not
going to be using the same library, so they won't get use out of it, and it adds
unnecessary dependencies to your code.  To decide between returning the original
error and an annotated error using fmt.Errorf is harder.  It's hard to know when
the information in the original error might be useful to your caller.  On the
other hand, the additional context added by fmt.Errorf can often change an
inscrutable error into an obvious one.

If you're writing an application where you'll be controlling most of the
packages being written, then an errors package may make sense... but you still
run the risk of giving your custom errors to third party code that can't
understand them.  Plus, any errors library adds some complexity to the code (for
example, you always have to rememeber to call `os.IsNotExist(errors.Cause(err))`
rather than just calling `os.InNotExist(err)`).

You have to choose one of the three options every time you return an error.
Choose carefully.  Sometimes you're going to make a choice that makes your life
more difficult down the road.
