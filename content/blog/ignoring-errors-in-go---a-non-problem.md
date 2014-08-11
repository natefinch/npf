+++
title = "Ignoring errors in Go - a non-problem"
date = 2014-04-10T15:55:00Z
updated = 2014-04-10T15:55:41Z
draft = true
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

One common complaint about Go is that, because it doesn't use exceptions, and instead uses multiple returns and error values, that it's "easy" to just ignore errors and have your code blindly try to continue to function after it has encountered an error.<br /><br />The idea that it is easy to ignore errors is completely untrue, and I'll show you why that is.<br /><br />There are only two ways a function can return an error, either as a standalone return, or as a multiple return with some other data
