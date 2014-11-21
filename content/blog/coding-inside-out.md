+++
title = "Coding Inside Out in Go"
date = 2014-04-18T13:58:00Z
updated = 2014-04-18T13:58:55Z
draft = true
blogimport = true 
type = "post"
tags = ["go", "golang", "programming", "interfaces"]
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

Interfaces are one of the best features of Go, and the one most confusing to
people who have used interfaces in other languages.  This is not an introduction
to interfaces, I'm going to assume you know how they work, more or less. This is
more of a post about how to effectively use interfaces and avoid the mistake of
assuming they should be used like they are in other languages.

Interfaces in Go are different than those in most other statically typed
languages like C++, Java, or C# - Go's interfaces are *implicitly* fulfilled,
rather than explicitly. What this means is that the type that satisfies the
interface doesn't even have to know the interface exists.  All that is required
is that the type have the correct methods.  This has some far reaching
ramifications that are not immediately obvious.

In other languages, you write an interface for a type. For example, you might
have an interface to abstract away a document store, so that you can use a
database, filesystem, network, or in-memory storage.  Since these languages
require interfaces to be explicitly implemented by types, each of the types that
implement the interface must be explicitly marked as doing so.  Interfaces tend
to end up very wide (having lots of methods), because if you miss exposing a
method in the interface, you can't access that method unless you go back and add
it to the interface, and then add the implementation of the method to each of
the implementations of the concrete types. 

While you can do similar things in Go, this is often not the best way to
leverage Go's interfaces.  In fact, using Go's interfaces in the most efficient
way possible often involves turning your code inside out.

There are two rules to keep in mind when using Go with interfaces:

1. Go's interfaces work best when they are very small.

2. Design interfaces for functions, not for types.

These two rules go hand in hand, and are a direct result of Go's implicit
satisfaction of interfaces.  Let's look at one of the canonical Go interfaces,
io.Writer.  It has only a single method:


```go
type Writer interface {
	Write(p []byte) (int, error).  
}
```

All it does is write some bytes from the given slice.  So, why is it so awesome?
It is a very narrowly focused interface, so almost anything can implement it
without much difficulty - network connections, files, in-memory byte buffers, or
even things like Stdout and Stderr.  The focus is narrow and the signature is
very basic.  Take some bytes to write, return the number of bytes written, plus
an error in case something goes wrong.  

This makes it easy to write functions that use this interface in different ways.
Any function that needs to write out data to a value can (and should) do so by
taking an io.Writer and writing to it.

It also means it's very easy to transform the data being written, just write a
wrapper that wraps another io.Writer, and you now have a writer that can, for
example, gzip the data before it gets written, or strip out newlines, or encrypt
it, or all three.  And this kind of transformation can be easily inserted into a
pipeline of data without any of the rest of the parts of the system even being
aware of it.

And it's all possible because io.Writer is a tiny little interface written in a
very basic way, so a whole bunch of functions and types can implement it and use
it in their own way.




