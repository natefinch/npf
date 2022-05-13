+++
date = "2022-05-13T09:03:00-04:00"
draft = false
title = "Safer Enums"
type = "post"
tags = ["go", "golang", "enums", "bite-sized-go"]
series = ["Bite Sized Go"]
+++

How to "do" enums is a common problem in Go, given that it doesn't have “real”
enums like other languages. There's basically two common ways to do it, the
first is just typed strings:

```
type FlagID string
const (
    FooBar FlagID = “FooBar”
    FizzBuzz FlagID = “FizzBuzz”
)
func IsEnabled(id FlagID) bool {
```

The problem with this is that string literals (really, string constants) in Go
will get converted to the correct type, so you’d still be able to call
`IsEnabled(“foo-bar”)` without the compiler complaining.

A common replacement is to use numeric constants:

```
type FlagID int
const (
    FooBar FlagID = iota
    FizzBuzz
)
func IsEnabled(id FlagID) bool {
```

This is nice, because it would be pretty odd to see code like `IsEnabled(4)`.
But the problem then becomes that you can't easily print out the name of the
enum in logs or errors.

To fix this, someone ([Rob Pike?](https://go.dev/blog/generate)) wrote
[stringer](https://pkg.go.dev/golang.org/x/tools/cmd/stringer), which generates
code to print out the name of the flags... but then you have to remember to run
stringer, and it's a bunch of (really) ugly code.  

The solution to this was something I first heard suggested by Dave Cheney
(because of course it was), and is so simple and effective that I can’t believe
I had never thought of it before. Make FlagName into a very simple struct:

```
type FlagID struct {
    name string
}
func (f FlagID) String() { return f.name } 

var (
    FooBar = FlagID{ “FooBar” }
    FizzBuzz = FlagID{ “FizzBuzz” }
)

func IsEnabled(id FlagID) bool {
```

Now, you can’t call `IsEnabled(“nope”)`, because the constant string can’t be
converted into a struct, so the compiler would complain. You *could* still call
`IsEnabled(FlagID{“nope”})` … but that’s not likely to happen by accident.

There’s no size difference between a `string` and a `struct{ string }` and it's
just as easy to read as a straight string. Because of the String() method, you
can pass these values to `%s` etc in format strings and they'll print out the
name with no extra code or work.

The one tiny drawback is that the globals have to be variables instead of
constants, but that’s one of those problems that really only exists in the
theoretical realm. I’ve never seen a bug from someone overwriting a global
variable like this, that is intended to be immutable.

I’ll definitely be using this pattern in my projects going forward. I hope this
helps some folks who are looking to avoid typos and accidental bugs from
stringly typed code in Go.