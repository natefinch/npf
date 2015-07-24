+++
date = "2015-07-14T07:00:19-04:00"
draft = true
title = "Go Pointers"
type = "post"
tags = ["go", "golang", "pointers"]
+++

One thing makes Go different from many other modern languages - it has pointers.
This can be confusing if you're coming from a language that doesn't have
pointers (such as java, javascript, C#, python, ruby, etc).  And it can be
confusing if you're coming from a language that *does* have pointers, since Go's
pointers don't work precisely like other common languages with pointers (C,
C++).

## In Go, Everything is Pass by Value

In some languages, like C# and Java, many or most types are actually pointers in disguise, for example instances of a class in Java or C#.  If you pass an instance of a class to a method and it modifies the passed-in value, the caller will see the modifications in its own copy.  Sometimes this is called Pass by Reference, but that's a misleading term.  Really, it's Pass by Pointer, it's just hidden from you.  Under the hood, when you pass an instance of a class to a function, what you're really passing is a pointer to that value.

There are some types in Go that behave this way as well - maps, slices, channels, and interface values.  Behind the scenes, these types are all implemented as lightweight structs that contain a pointer to the actual value.

...


## When Should I Use a Pointer Receiver?

One of the most common questions from devs coming from a pointer-free language is, when should I use a pointer receiver for my functions?  The answer, like everything in computer science, comes down to "it depends".  If you're writing a method on a type that is not one of the built-ins that is already a pointer (maps, slices, channels), and you want the method to change the value, you *must* use a pointer receiver.  For example, if you have a struct with a SetName() method, and you want that method to set the name field value in the struct, you must use a pointer receiver for that method.

For everything other than this one clear-cut case, the answer is like anything else - there are tradeoffs.  The common reason to use a pointer receiver for methods 

....