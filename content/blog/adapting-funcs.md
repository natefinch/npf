+++
title = "Adapting Functions"
type = "post"
date = "2017-07-15T15:55:43+01:00"

+++

A question came up at Gophercon about using functions as arguments, and what to
do when you have a function that you want to use that doesn't quite match the
signature.  Here's an example:

```
type Translator func(string) string

func RunTwice(translate Translator, input string) string {
    return translate(translate(input))
}
```

Now, what if you want to use RunTwice with a function that needs more inputs
than just a string?

```
func Append(orig, suffix string) string {
    return orig + suffix
}

func do() {
    orig := "awesome"
    bang := "!"
    s := RunTwice(Append(orig, )) // wait, that won't work
    fmt.Println(s)
}

```

The answer is the magic of closures. Closures are anonymous functions that
"close over" or save copies of all local variables so they can be used later.
You can write a closure that captures the bang, and returns a function that'll
have the Translator signature.

```
func do() string {
    orig := "awesome"
    bang := "!"
    bangit := func(s string) string {
        return Append(s, bang)
    }
    return RunTwice(bangit(orig))
}
```

Yay, that works.  But it's not reusable outside the do function.  That may be
fine, it may not. If you want to do it in a reusable way (like, a lot of people
may want to adapt Append to return a Translator, you can make a dedicated
function for it like this: 

```
func AppendTranslator(suffix string) Translator {
    return func(s string) string {
        return Append(s, suffix)
    }
}
```

In AppendTranslator, we return a closure that captures the suffix, and returns a
function that, when called, will append that suffix to the string passed to the
Translator.  

And now you can use AppendTranslator with RunTwice.