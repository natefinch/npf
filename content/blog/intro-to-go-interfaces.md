+++
title = "Intro++ to Go Interfaces"
date = 2014-05-13T07:08:00Z
updated = 2014-06-04T06:21:52Z
tags = ["Go", "programming", "golang"]
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

### Standard Interface Intro

Go’s interfaces are one of it’s best features, but they’re also one of the most confusing for newbies.  This post will try to give you the understanding you need to use Go’s interfaces and not get frustrated when things don’t work the way you expect.  It’s a little long, but a bunch of that is just code examples.

Go’s interfaces are different than interfaces in other languages, they are implicitly fulfilled.  This means that you never need to mark your type as explicitly implementing the interface (like class CFoo implements IFoo).  Instead, your type just needs to have the methods defined in the interface, and the compiler does the rest.

For example:
```
type Walker interface {
    Walk(miles int)
}

type Camel struct {
    Name string
}

func (c Camel) Walk(miles int) {
     fmt.Printf(“%s is walking %v miles\n”, c.Name, miles)
}

func LongWalk(w Walker) {
     w.Walk(500)
     w.Walk(500)
}

func main() {
    c := Camel{“Bill”}
    LongWalk©
}

// prints
// Bill is walking 500 miles.
// Bill is walking 500 miles.
```
http://play.golang.org/p/erodX-JplO

Camel implements the Walker interface, because it has a method named Walk that
takes an int and doesn’t return anything.  This means you can pass it into the
LongWalk function, even though you never specified that your Camel is a Walker.
In fact, Camel and Walker can live in totally different packages and never know
about one another, and this will still work if a third package decides to make a
Camel and pass it into LongWalk.

### Non-Standard Continuation

This is where most tutorials stop, and where most questions and problems begin.
The problem is that you still don’t know how the interfaces actually work, and
since it’s not actually that complicated, let’s talk about that.

What actually happens when you pass Camel into LongWalk?


So, first off, you’re not passing Camel into LongWalk.  You’re actually
assigning c, a value of type Camel to a value w of type Walker, and w is what
you operate on in LongWalk.

Under the covers, the Walker interface (like all interfaces), would look more or
less like this if it were in Go (the actual code is in C, so this is just a
really rough approximation that is easier to read).

```
type Walker struct {
    type InterfaceType
    data *void
}

type InterfaceType struct {
    valtype *gotype
    func0 *func
    func1 *func
    ...
}
```

All interfaces values are just two pointers - one pointer to information about
the interface type, and one pointer to the data from the value you passed into
the interface (a void in C-like languages… this should probably be Go’s
unsafe.Pointer, but I liked the explicitness of two actual *’s in the struct to
show it’s just two pointers).

The InterfaceType contains a pointer to information about the type of the value
that you passed into the interface (valtype).  It also contains pointers to the
methods that are available on the interface.

When you assign c to w, the compiler generates instructions that looks more or
less like this (it’s not actually generating Go, this is just an easier-to-read
approximation):

```
data := c
w := Walker{ 
    type: &InterfaceType{ 
              valtype: &typeof\(c), 
              func0: &Camel.Walk 
          }
    data: &data
}
```

When you assign your Camel value c to the Walker value w, the Camel type is
copied into the interface value’s Type.valtype field.  The actual data in the
value of c is copied into a new place in memory, and w’s Data field points at
that memory location.

### Implications of the Implementation

Now, let’s look at the implications of this code.  First, interface values are
very small - just two pointers.  When you assign a value to an interface, that
value gets copied once, into the interface, but after that, it’s held in a
pointer, so it doesn’t get copied again if you pass the interface around.

So now you know why you don’t need to pass around pointers to interfaces -
they’re small anyway, so you don’t have to worry about copying the memory, plus
they hold your data in a pointer, so changes to the data will travel with the
interface.

### Interfaces Are Types

Let’s look at Walker again, this is important:

type Walker interface

Note that first word there: type.  Interfaces are types, just like string is a
type or Camel is a type.  They aren’t aliases, they’re not magic hand-waving,
they’re real types and real values which are distinct from the type and value
that gets assigned to them.

Now, let’s assume you have this function:

func LongWalkAll(walkers []Walker) {
    for _, w := range walkers {
        LongWalk(w)
    }
}

And let’s say you have a caravan of Camels that you want to send on a long walk:

```
caravan := []Camel{ Camel{“Bill”}, Camel{“Bob”}, Camel{“Steve”}}
```

You want to pass caravan into LongWalkAll, will the compiler let you?  Nope.
Why is that?  Well, []Walker is a specific type, it’s a slice of values of type
Walker.  It’s not shorthand for “a slice of anything that matches the Walker
interface”.  It’s an actual distinct type, the way []string is different from
[]int.  The Go compiler will output code to assign a single value of Camel to a
single value of Walker.  That’s the only place it’ll help you out.  So, with
slices, you have to do it yourself:

```
walkers := make([]Walker, len(caravan))
for n, c := range caravan {
    walkers[n] = c
}
LongWalkAll(walkers)
```

However, there’s a better way if you know you’ll just need the caravan for
passing into LongWalkAll:

```
caravan := []Walker{ Camel{“Bill”}, Camel{“Bob”}, Camel{“Steve”}}
LongWalkAll(caravan)
```

Note that this goes for any type which includes an interface as part of its
definition: there’s no automatic conversion of your func(Camel) into
func(Walker) or map[string]Camel into map[string]Walker.  Again, they’re totally
different types, they’re not shorthand, and they’re not aliases, and they’re not
just a pattern for the compiler to match.

Interfaces and the Pointers That Satisfy Them

What if Camel’s Walk method had this signature instead?

```
func (c *Camel) Walk(miles int)
```

This line says that the type *Camel has a function called Walk.  This is
important: *Camel is a type.  It’s the “pointer to a Camel” type.  It’s a
distinct type from (non-pointer) Camel.  The part about it being a pointer is
part of its type.  The Walk method is on the type *Camel.  The Walk method (in
this new incarnation) is not on the type Camel. This becomes important when you
try to assign it to an interface.

```
c := Camel{“Bill”}
LongWalk(c)

// compiler output:
cannot use c (type Camel) as type Walker in function argument:
 Camel does not implement Walker (Walk method has pointer receiver)
```

To pass a Camel into LongWalk now, you need to pass in a pointer to a Camel:
```
c := &Camel{“Bill”}
LongWalk\(c)

or

c := Camel{“Bill”}
LongWalk(&c)
```

Note that this true even though you can still call Walk directly on Camel:

```
c := Camel{“Bill”}
c.Walk(500) // this works
```

The reason you can do that is that the Go compiler automatically converts this
line to (&c).Walk(500) for you.  However, that doesn’t work for passing the
value into an interface.  The reason is that the value in an interface is in a
hidden memory location, and so the compiler can’t automatically get a pointer to
that memory for you (in Go parlance, this is known as being “not addressable”).

### Nil Pointers and Nil Interfaces

The interaction between nil interfaces and nil pointers is where nearly everyone
gets tripped up when they first start with Go.

Let’s say we have our Camel type with the Walk method defined on *Camel as
above, and we want to make a function that returns a Walker that is actually a
Camel (note that you don’t need a function to do this, you can just assign a
*Camel to a Walker, but the function is a good illustrative example):

```
func MakeWalker() Walker {
    return &Camel{“Bill”}
}

w := MakeWalker()
if w != nil {
    w.Walk(500)  // we will hit this
}
```

This works fine.  But now, what if we do something a little different:

```
func MakeWalker(c *Camel) Walker {
    return c
}

var c *Camel
w := MakeWalker©
if w != nil {
    // we’ll get in here, but why?
    w.Walk(500)
}
```

This code will also get inside the if statement (and then panic, which we’ll
talk about in a bit) because the returned Walker value is not nil.  How is that
possible, if we returned a nil pointer?  Well, let’s go look back to the
instructions that get generated when we assign a value to an interface.

```
data := c
w := Walker{ 
    type: &InterfaceType{ 
              valtype: &typeof\(c), 
              func0: &Camel.Walk 
          }
    data: &data
}
```

In this case, c is a nil pointer. However, that’s a perfectly valid value to
assign to the Walker’s Data value, so it works just fine.  What you return is a
non-nil Walker value, that has a pointer to a nil *Camel as its data.  So, of
course, if you check w == nil, the answer is false, w is not nil… but then
inside the if statement, we try to call Camel’s walk:

```
func (c *Camel) Walk(miles int) {
     fmt.Printf(“%s is walking %v miles\n”, c.Name, miles)
}
```

And when we try to do c.Name, Go automatically turns that into (*c).Name, and
the code panics with a nil pointer dereference error.

Hopefully this makes sense, given our new understanding of how interfaces wrap
values, but then how do you account for nil pointers?  Assume you want
MakeWalker to return a nil interface if it gets passed a nil Camel.  You have to
explicitly assign nil to the interface:

```
func MakeWalker(c *Camel) Walker {
    if c == nil {
        return nil
    }
    return c
}

var c *Camel
w := MakeWalker©
if w != nil {
    // Yay, we don’t get here!
    w.Walk(500)
}
```

And now, finally, the code is doing what we expect.  When you pass in a nil
*Camel, we return a nil interface.  Here’s an alternate way to write the
function:

```
func MakeWalker(c *Camel) Walker {
    var w Walker
    if c != nil {
        w = c
    }
    return w
}
```

This is slightly less optimal, but it shows the other way to get a nil
interface, which is to use the zero value for the interface, which is nil.

Note that you can have a nil pointer value that satisfies an interface.  You
just need to be careful not to dereference the pointer in your methods.  For
example, if *Camel’s Walk method looked like this:

```
func (c *Camel) Walk(miles int) {
    fmt.Printf(“I’m walking %d miles!”, miles)
}
```

Note that this method does not dereference c, and therefore you can call it even
if c is nil:

```
var c *Camel
c.Walk(500)
// prints “I’m walking 500 miles!”
```

http://play.golang.org/p/4EfyV21at9

### Outro

I hope this article helps you better understand how interfaces works, and helps
you avoid some of the common pitfalls and misconceptions newbies have about how
interfaces work.  If you want more information about the internals of interfaces
and some of the optimizations that I didn’t cover here, read Russ Cox’s article
on Go interfaces, I highly recommend it.
