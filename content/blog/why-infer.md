+++
date = "2022-05-26T09:03:00-04:00"
draft = false
title = "Why Infer Types?"
type = "post"
tags = ["go", "golang", "types", "type-inference", "bite-sized-go"]
series = ["Bite Sized Go"]
+++

Someone at work asked the following question:

Why write code like this: 

```
foo := getFoo()
bar, err := getBar()
```

instead of this:

```
var foo Type = getFoo()

var err error
var bar Type

bar, err = getBar(foo)
```

Isn't the latter more explicit? Isn't explicit better? It'll be easier to review
because you'll be able to see all the types.

Well, yes and no.

For one thing, between the name of the function you’re calling and the name of
the variable you’re assigning to, the type is obvious most of the time, at
least at the high level.

```
userID, err := store.FindUser(username)
```

Maybe you don’t know if userID is a UUID or some custom ID type, or even just a
numeric id... but you know what it represents, and the compiler will ensure you
don’t send it to some function that doesn’t take that type or call a method on
it that doesn't exist.

In an editor, you’ll be able to hover over it to see the type. In a code
review, you may be able to as well, and even if not, you can rely on the
compiler to make sure it’s not being used inappropriately.

One big reason to prefer the inferred type version is that it makes refactoring
a lot easier.  

If you write this code everywhere:

```
var foo FooType = getFoo()
doSomethingWithFoo(foo)
```

Now if you want to change getFoo to return a Foo2, and doSomethingWithFoo to
take a Foo2, you have to go change every place where these two functions are
called and update the explicitly declared variable type.

But if you used inference:

```
foo := getFoo()
doSomethingWithFoo(foo)
```

Now when you change both functions to use Foo2, no other code has to change. And
because it’s statically typed, we can know this is safe, because the compiler
will make sure we can’t use Foo2 inappropriately.

Does this code *really* care what type getFoo returns, or what type
doSomethingWithFoo takes? No, it just wants to pipe the output of one into the
other. If this shouldn't work, the type system will stop it.

So, yes, please use the short variable declaration form. Heck, if you look at it
sideways, it even looks kinda like a gopher :=