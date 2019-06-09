+++
title = "Do or Do Not"
date = 2019-06-07T23:40:50-04:00
type = "post"
+++
## The proposal

There's a new Go proposal in town - [try()](https://github.com/golang/go/issues/32437).  The gist is that it adds a builtin function `try()` that can wrap a function that returns (a, b, c, ..., error), and if the error is non-nil, it will return from the enclosing function, and if the error is nil, it'll return the rest of the return values.

This is how it looks in code:

```
func doIt() (string, int, error){
    return "Daisy", 45, io.EOF
}

func tryIt() error {
    name, age := try(doIt())
    // use name, age
    return nil
}
```

In the above, if doIt returns a non-nil error, tryIt will exit at the point where try is called, and will return that error.

## Complications

So here's my problem with this... it complicates the code. It adds points where your code can exit from inside the right hand side of a statement somewhere. It can make it very easy to miss the fact that there's an early exit statement in the code.

The above is simplistic, it could instead look like this:

```
func tryIt() error {
    fmt.Printf("Hi %s, happy %vth birthday!\n", try(doIt())
    // do other stuff
    return nil
}
```

At first blush, it would be very easy to read that code and think this function
always returns nil, and that would be wrong and it could be catastrophically
wrong. 

## The Old Way

In my opinion, the old way (below) of the original code is a lot more readable. The exit point is clearly called out by the return keyword as well as the indent. The intermediate variables make the print statement a lot more clear.

```
func tryIt() error {
    name, age, err := doIt()
    if err != nil {
        return err
    }
    fmt.Printf("Hi %s, happy %vth birthday!\n", name, age)
    return nil
}
```

Oh, and did you catch the mismatched parens on the Printf statement in the try() version of `tryIt()` above?  Me neither the first time.

## Early Returns

Writing Go code involves a LOT of returning early, more than any other popular language except maybe C or Rust. That's the real meat of all those `if err != nil` statements... it's not the `if`, it's the **return**.

The reason early returns are so good is that once you pass that return block, you can ignore that case forever. The case where the file doesn't exist? Past the line of os.Open's error return, you can ignore it. It no longer exists as something you have to keep in your head. 

However, with try, you now have to worry about both cases in the same line and keep that in your head. Order of operations can come into play, how much work are you actually doing before this try may kick you out of the function?

## One idea per line

One of the things I have learned as a go programmer is to eschew line density.  I don't want a whole ton of logic in one line of code. That makes it harder to understand and harder to debug.  This is why I don't care about missing ternary operator or map and filter generics. All those do is let you jam more logic into a single line, and I don't want that. That makes code hard to understand, and easier to *misunderstand*.

Try does exactly that, though.  It encourages you to put a call getting data into a function that then uses that data.  For simple cases, this is really nice, like field assignment:

```
p := Person{
    Name: try(getUserName()),
    Age: try(getUserAge()),
}
```

But note how even here, we're trying to split up the code into multiple lines, one assignment per line. 

Would you ever write this code this way?

```
p := Person{Name: try(getUserName()), Age: try(getUserAge())}
```

You certainly can, but holy crap, that's a dense line, and it takes me an order of magnitude longer to understand that line than it does the 4 lines above, even though they're just differently formatted version of the exact same code.  But this is exactly what will be written if try becomes part of the language.  Maybe not struct initialization, but what about struct initialization functions?  

```
p := NewPerson(try(getUserName()), try(getUserAge()))
```

Nearly the same code.  Still hard to read.

## Nesting Functions

Nesting functions is bad for readability. I *very* rarely nest functions in my go code, and looking at other people's go code, most other people also avoid it.  Not only does try() force you to nest functions as its basic use case, but it then encourages you to use that nested function nested in some other function.  So we're going from `NewPerson(name, age)` to `NewPerson(try(getUserName()), try(getUserAge()))`. And that's a real tragedy of readability.


