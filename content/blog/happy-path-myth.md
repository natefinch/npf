+++
date = "2014-10-25T06:44:32-04:00"
draft = true
title = "The Myth of the Happy Path"
type = "post"
series = ["Go Myths"]
+++

I had intended to make a single post about some myths related to Go, but I think
it would get too long and unwieldy, so instead I'm doing a series of posts.

In programming, the [happy path](http://en.wikipedia.org/wiki/Happy_path) is the
code path where nothing goes wrong.  People try to optimize their code so that
the happy path is most clear, so you can see what the code is *supposed* to do.
The problem with this is, the happy path is a myth.

Your code has a lot of code paths. They are all equally valid to the computer.
They all add up to what your code is *supposed* to do. They should be all
equally valid to you, the programmer, and to someone else, the person who has to
read and understand your code 18 months down the road.

Let's look at some code, and try to figure out what the happy path is:

```
// five truncates a string to a maximum of 5 characters and returns any remainder.
func five(s string) (five, remainder string) {
	if len(s) > 5 {
		return s[:5], s[5:]
	}
	return s, ""
}
```

Ok, so which return illustrates the happy path here? Can't tell? That's because
*both paths are equally valid*. Now lets look at some other code:

```
// five truncates a string to a maximum of 5 characters or returns an error if the string is too short
func five(s string) (string, error) {
	if len(s) > 5 {
		return s[5:], nil
	}
	return "", errors.New("String is too short!")
}
```

Ok, so which is the happy path in this case?  

Languages with exceptions swallow the myth of the happy path hook, line, and
sinker. They let you write *just* a single codepath for your "happy path" and
obfuscate the other paths. The problem being, of course, that the happy path
isn't special. Life isn't ideal, and you *will* deviate from the happy path,
probably quite often.

