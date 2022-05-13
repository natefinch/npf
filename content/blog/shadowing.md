+++
date = "2022-04-30T22:03:00-04:00"
draft = true
title = "Know When to Shadow"
type = "post"
tags = ["go", "golang", "shadowing", "bite-sized-go"]
series = ["Bite Sized Go"]
+++

Today, let's talk about error variables - their names and how they get assigned. In a recent PR I reviewed, I saw some code that looked something like this:

```
func Foo() error {
	err, user := getUser()
	// stuff

	if shouldUpdate() {		
		err = user.Update()
		// other stuff
	}

	err = LoadData()
	// more stuff
}
```

Inside the if statement, the author assigned the error to a variable from
outside the the scope of the if statement. When I see that, I normally assume
that this is intentional - the author is passing the value out of the block to
be used by the rest of the function. But in this case, the error was immediately
overwritten after the if statement, so it clearly was not being used in the rest
of the code.

