+++
date = "2021-04-07T23:03:00-04:00"
draft = false
title = "Error Flags"
type = "post"
tags = ["go", "golang", "errors", "error handling"]
+++

Error wrapping in go 1.13 solved a major problem gophers have struggled with since v1: how to add context to errors without obscuring the original error, so that code above could programmatically inspect the original error. However, this did not – by itself – solve the other common problems with errors: implementation leakage and (more generally) error handling.

## Fragile Error Handling

In 2016, Dave Cheney wrote [a blog post](https://dave.cheney.net/2016/04/27/dont-just-check-errors-handle-them-gracefully) that includes a section titled “Assert errors for behaviour, not type”. The gist of the section is that you don’t want code to depend on implementation-specific error types that are returned from a package’s API, because then, if the implementation ever changes, the error handling code will break. Even four and a half years later, and with 1.13’s new wrapping, this can still happen very easily.

For example, say you’re in an HTTP handler, far down the stack in your data layer. You’re trying to open a file and you get an os.ErrNotExist from os.Open. As of 1.13, you can add more context to that error without obscuring the fact that it’s an os.ErrNotExist. Cool, now the consumers of that code get a nicer error message, and if they want, they can check `os.IsNotExist(err)` and maybe return a 404 to the caller.

Right there, your web handler is now tied to the implementation details of how your backend, maybe 4 levels deep in the stack, stores data. If you decide to change your backend to store data in S3, and it starts returning `s3.ObjectNotFound` errors, your web handler won’t recognize that error, and won’t know to return 404. This is barely better than matching on the error string.

## Dave’s Solution - Interfaces

Dave proposes creating errors that fulfill interfaces the code can check for, like this:
```
type notFound interface {
        NotFound() bool
}
 
// IsNotFound returns true if err indicates the resource doesn’t exist.
func IsNotFound(err error) bool {
        m, ok := err.(notFound)
        return ok && m.NotFound()
}
```
Cool, so now you can ensure a consistent API without relying on the implementation-specific type of the error. Callers just need to check for IsNotFound, which could be fulfilled by any type. The problem is, it’s missing a piece. How do you take that os.NotExistErr and give it a IsNotFound() method? Well, it’s not super hard, but kind of annoying. You need to write this code:
```
// IsNotFound returns true if err indicates the resource doesn’t exist.
func IsNotFound(err error) bool {
        n, ok := err.(notFound)
        return ok && n.NotFound()
}
// MakeNotFound wraps err in an error that reports true from IsNotFound.
func MakeNotFound(err error) error {
    if err == nil {
        return nil
    }
    return notFoundErr{error: err}
}        

type notFound interface {
        NotFound() bool
}

type notFoundErr struct {
    error
}

func (notFoundErr) NotFound() bool {
    return true
}

func (n notFoundErr) Unwrap() error {
    return n.error
}
```

So now we’re at 28 lines of code and two exported functions. Now what if you want the same for NotAuthorized or ? 28 more lines and two more exported functions. Each just to add one boolean of information onto an error. And that’s the thing… this is purely used for flow control - all it needs to be is booleans.

## A Better Way - Flags

At Mattel, we had been following Dave’s method for quite some time, and our errors.go file was growing large and unwieldy. I wanted to make a generic version that didn’t require so much boilerplate, but was still strongly typed, to avoid typos and differences of convention.

After thinking it over for a while, I realized it only took a slight modification of the above code to allow for the functions to take the flag they were looking for, instead of baking it into the name of the function and method. It’s of similar size and complexity to IsNotFound above, and can support expansion of the flags to check, with almost no additional work. 
Here’s the code:
```
// ErrorFlag defines a list of flags you can set on errors.
type ErrorFlag int

const (
NotFound = iota + 1
NotAuthorized
	// etc
)

// Flag wraps err with an error that will return true from HasFlag(err, flag).
func Flag(err error, flag ErrorFlag) error {
	if err == nil {
		return nil
	}
	return flagged{error: err, flag: flag}
}

// HasFlag reports if err has been flagged with the given flag.
func HasFlag(err error, flag ErrorFlag) bool {
	for {
		if f, ok := err.(flagged); ok && f.flag == flag {
			return true
		}
		if err = errors.Unwrap(err); err == nil {
			return false
		}
	}
}

type flagged struct {
	error
	flag ErrorFlag
}

func (f flagged) Unwrap() error {
	return f.error
}
```

To add a new flag, you add a single line to the list of ErrorFlags and you move on. There’s only two exported functions, so the API surface is super easy to understand. It plays well with go 1.13 error wrapping, so you can still get at the underlying error if you really need to (but you probably won’t and shouldn’t!).

Back to our example: the storage code can now keep its implementation private and flag errors from the backend with return errors.Flag(err, errors.NotFound). Calling code can check for that with this:
```
if errors.HasFlag(err, errors.NotFound) {
    // handle not found
}
```
If the storage code changes what it’s doing and returns a different underlying error, it can still flag it with that with the NotFound flag, and the consuming code can go on its way without knowing or caring about the difference.

## Indirect Coupling

Isn’t this just sentinel errors again? Well, yes, but that’s ok. In 2016, we didn’t have error wrapping, so anyone who wanted to add info to the error would obscure the original error, and then your check for err == os.ErrNotExist would fail. I believe that was the major impetus for Dave’s post. Error wrapping in Go 1.13 fixes that problem. The main problem left is tying error checks to a specific implementation, which this solves.  

This solution does require both the producer and the consumer of the error to import the error flags package and use these same flags, however in most projects this is probably more of a benefit than a problem. The edges of the application code can easily check for low level errors and flag them appropriately, and then the rest of the stack can just check for flags. Mattel does this when returning errors from calling the database, for example. Keeping the flags in one spot ensures the producers and consumers agree on what flag names exist. 

In theory, Dave’s proposal doesn’t require this coordination of importing the same package. However, in practice, you’d want to agree on the definition of IsNotFound, and the only way to do that with compile-time safety is to define it in a common package. This way you know no one’s going to go off and make their own IsMissing() interface that gets overlooked by your check for IsNotFound().

## Choosing Flags

In my experience, there are a limited number of possible bits of data your code could care about coming back about an error. Remember, flags are only useful if you want to change the application’s behavior when you detect them. In practice, it’s not a big deal to just make a list of a handful of flags you need, and add more if you find something is missing. Chances are, you’ll think of more flags than you actually end up using in real code. 

## Conclusion

This solution has worked wonders for us, and really cleaned up our code of messy, leaky error handling code. Now our code that calls the database can parse those inscrutable postgres error codes right next to where they’re generated, flag the returned errors, and the http handlers way up the stack can happily just check for the NotFound flag, and return a 404 appropriately, without having to know anything about the database.

Do you do something similar? Do you have a totally different solution? I’d love to hear about it in the comments.
