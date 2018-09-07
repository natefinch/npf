+++
title = "Handle and Check - Let's Not"
date = 2018-09-06T13:49:33-04:00
type = "post"
+++

There's a new error handling design [proposed here](https://go.googlesource.com/proposal/+/master/design/go2draft-error-handling.md).  It's.... not great.

Handle is a new keyword that basically defines a translation that can be applied
to errors returned from the current function:

```
func printSum(a, b string) error {
	handle err { return fmt.Errorf("error summing %v and %v: %v", a, b, err ) }
	x := check strconv.Atoi(a)
	y := check strconv.Atoi(b)
	fmt.Println("result:", x + y)
	return nil
}
```

Check applies the handler and returns if the error passed into it is not nil,
otherwise it returns the non-error value.

Handle, in my opinion is kind of useless. We can already do this today with functions thusly:

```
func printSum(a, b string) (err error) {
	check := func(err error) error { 
        return fmt.Errorf("error summing %v and %v: %v", a, b, err )
    }
	x, err := strconv.Atoi(a)
    if err != nil {
        return check(err)
    }
	y, err := strconv.Atoi(b)
    if err != nil {
        return check(err)
    }
	fmt.Println("result:", x + y)
	return nil
}
```

That does literally the same thing as check and handle above. 

The stated reason for adding check and handle is that too many people just write
"return err" and don't customize the error at all, which means somewhere at the
top of your program, you get this inscrutable error from deep in the bowels of
your code, and you have no idea what it actually means.

It's trivial to write code that does most of what check and handle do... and no
one's doing it today (or at least, not often).  So why add this complexity?

Check and handle actually make error handling worse.  With the check and handle
code, there's no required "error handling scope" after the calls to add context
to the error, log it, clean up, etc.  With the current code, I *always* have an
if statement that I can easily slot more lines into, in order to make the error
more useful and do other things on the error path.  With `check`, that space in
the code doesn't exist.  There's a barrier to making that code handle errors
better - now you have to remove `check` and swap in an if statement. Yes,
you can add a new `handle` section, but that applies globally to any further
errors returns in the function, not just for this one specific error.  Most of
the time I want to add information about one specific error case.

So, for example, in the code above, I would want a different error message for A
failing Atoi vs. B failing Atoi.... because in real code, which one is the
problem may not be obvious if the error message just says "either A or B is a
problem".

Yes, `if err != nil {` constitutes a lot of Go code.  That's ok.  That's actually
good.  Error handling is extremely important.  Check and handle don't make error
handling better.  I suspect they'll actually make it worse.

A refrain I often state about changes requested for Go is that most of them
just involve avoiding an if statement or a loop.  This is one of them.  That's
not a good enough reason to change the language, in my opinion.