+++
title = "Go2 Contracts Go Too Far"
date = 2018-09-05T14:00:18-04:00
type = "post"
+++

So, I don't really like the contracts defined
[here](https://go.googlesource.com/proposal/+/master/design/go2draft-contracts.md).
They seem complicated to understand, and duplicate a lot of what interfaces
already do, but in a much clunkier fashion.

I think we can do 90% of what the design given can do, with 20% of the added
complexity.

Most of my objection comes from two things: 

First the syntax, which adds "type parameters" as yet another overloaded meaning
for stuff in parentheses (we already have: argument lists, return values,
function calls, type conversion, type assertion, and grouping for order of
operations). 

Second, the implicit nature of how contracts are defined by a random block of
code that is sorta like go code, but not actually go code.

## Syntax

This is a generic function as declared in the contracts code:
```
func Print(type T)(s []T) {
	for _, v := range s {
		fmt.Println(v)
	}
}
```

The (type T) here defines a type parameter.  In this case it doesn't tell us
anything about the type, so it's effectively like interface{}, except that it
magically works with slices the way we all thought interfaces should work with
slices back in the day – i.e. you can pass any slice into this, not just
[]interface{}.

Are we now going to have `func(type T)(input T)(output T){}`?  That's crazy.

Also, I don't like that the type parameters precede the arguments... isn't the
whole reason that we have Go's unusual <name type> ordering that we acknowledge
that the name is more important than the type?  Also, to call functions in this
way, you have to tell it what type you're passing, so like you'd call Print
above by doing `Print(int)([]int{1,2,3})`. Why can't we have type inference
here?

Here's my fix... since contracts are basically like interfaces, let's actually
use interfaces.  And let's make the contracty part last, since it's least
important:

```
func Print(s []interface{}:T) {
	for _, v := range s {
		fmt.Println(v)
	}
}
```

So here's the change in a nutshell. You use a real interface to define the type
of the argument.  In this case it's interface{}.  This cuts out the need to
define a contract separately when we already have a way of defining an abstract
type with capabilities.  The : tells the compiler that this is a parameterized
type, and T is the name given that type (though it's not used anywhere).


## Contract Definitions as Code Are Hard

> Specifying contracts via example code is going to age about as well as
> specifying time formats via example output.  -me on Twitter

The next example in the design is 

```
contract stringer(x T) {
	var s string = x.String()
}

func Stringify(type T stringer)(s []T) (ret []string) {
	for _, v := range s {
		ret = append(ret, v.String())
	}
	return ret
}
```

Wait, so we have to redefine the Stringer interface?  Why?  WHy not just *use* a
Stringer interface?  Also, what happens if I screw up the code, like this?

```
contract stringer(x T) {
	s := x.String()
}
```

You think the error message from that is going to be good?  I don't.

Also, this allows an arbitrarily large amount of code in contract definitions.
Much of this code could easily imply restrictions that you don't intend, or be
more general than you expect.

```
contract slicer(x T) {
	s := x[0]
}
```

Is that a map of int to something?  Or is it a slice? Is that just invalid? What
would the error message say, if so?  Would it change if I put a 1 in the index?
Or -1?  Or "1"?

Notably... a lot of really smart gophers who have been programming in Go for
years have difficulty defining contracts that are conceptually simple, because
there is so much implied functionality in even simple types.

Take a contract that says you can accept a string or a []byte... what do you
think it would look like?

If you guessed this with even your second or third try...

```
contract stringOrBytes(s S) {
    string(s)
    s[0]
    s[:]
    S([]byte{})
}
```

...then I applaud you for being better at Go than I am. And there's still
questions about whether or not this would fail for len(s) == 0 (answer: it
won't, because it's just type checked, not actually run... but, see what I mean
about implications?) Also, I'm not even 100% sure this is sufficient to define
everything you need.  It doesn't seem to say that you can range over the type.
It doesn't say that indexing the value will produce a single byte. 

## Lack of Names and Documentations

The biggest problem with contracts defined as random blocks of code is their
lack of documentation.  As above, what exactly a bit of code means in a contract
is actually quite hard to distill when you're talking about generic types.  And
then how do you talk about it?  If you have your function that takes your
locally defined stringOrByte, and someone else has theirs defined as robytes,
but the contents are the same (but maybe in a different order with different
type names)... how can you figure out if they're compatible?

Is this the same contract as above?

```
contract robytes(t T) {
    T([]byte{})
    t[5:10]
    string(t)
    t[100]
}
```

Yes, but it's non-trivial to see that it is (and if it wasn't, you'd probably
have to rely on the compiler to tell you).

Imagine for a moment if there were no io.Reader or io.Writer interfaces.  How
would you talk about functions that write to a slice of bytes?  Would we all
write exactly the same interface?  Probably not.  Look at the lack of a Logging
interface, and how that affected logging across the ecosystem.  io.Reader and
io.Writer make writing and reading streams of bytes so nice *because* they're
standardized, *because* they are discoverable. The standardization means that
everyone who writes streams of bytes uses the exact same signature, so we can
compose readers and writers trivially, and discover new ways to compose them
just by looking for the terms io.Reader and io.Writer.


## Just Use Interfaces, and Make Some New Built-in Ones

My solution is to mainly just use interfaces and tag them with :T to denote
they're a parameterized type.  For contracts that don't distill to "has a
method", make built-in contract/interfaces that can be well-documented and
well-known.  Most of the examples I've seen of "But how would you do X?" boil
down to "You can't, and moreover, you probably shouldn't".

A lot of this boils down to "I trust the stdlib authors to define a good set of
contracts and I don't want every random coder to throw a bunch of code in a
contract block and expect me to be able to understand it".

I think most of the useful contracts can be defined in a small finite list that
can live in a new stdlib package, maybe called ct to keep it brief.
ct.Comparable could mean x == x.  ct.Stringish could mean "string or []byte or a
named version of either"... etc.  

Most of the things that fall outside of this are things that I don't think you
should be doing.  Like, "How do you make a function that can compare two
different types with ==?"  Uh... don't, that's a bad idea.  

One of the uses in the contract design is a way to say that you can convert one
thing to another.  This can be useful for generic functions on strings vs []byte
or int vs int64.  This could be yet another specialized interface:

```
package ct

// Convertible defines a type that can be converted into T.
type Convertible:T contract

// elsewhere

func ParseUint64(v ct.Convertible:uint64) {
    i, err := strconv.ParseUint(uint64(v))
}
```

## Conclusion

The contracts design, as written, IMO, will make the language significantly
worse.  Wrapping my head around what a random contract actually means for my
code is just too hard if we're using example code as the means of definition.
Sure, it's a clever way to ensure that only types that can be used in that way
are viable... but clever isn't good.

One of my favorite posts about Go is Rob Napier's [Go is a Shop-Based
Jig](http://robnapier.net/go-is-a-shop-built-jig).  In it, he argues that there
are many ineleagant parts to the Go language, but that they exist to make the
whole work better for actual users. This is stuff like the built-in functions
append and copy, the fact that slices and maps are generic, but nothing else is.
Little pieces are filed off here, stapled on there, because making usage easy
matters more than looking slick.

This design of contracts as written does not feel like a shop-built jig.  It
feels like a combination all-in-one machine that can do anything but is so
complicated that you don't even know how to even approach it or when you should
use it vs the other tools in your shop. 

I think we can make a smaller, more incremental addition to the language that
will fix a lot of the problems that many people have with Go - lack of reusable
container types, copy and paste for simple map and filter functions, etc.  This
will only add a small amount of complexity to the language, while solving real
problems that people experience. 

Notably, I think a lot of the problems generics solve are actually quite minor
in the scheme of major projects.  Yes, I have to rewrite a filter function for
every type. But that's a function I could have written in college and I usually
only need one or two per 20,000 lines of code (and then almost always just
strings).

So... I really don't want to add a bunch of complexity to solve these problems.
Let's take the most straightforward fix we can get, with the least impact on the
language. Go has been an amazing success in the last decade.  Let's move slowly
so we don't screw that up in the next decade.