+++
title = "Go is Boring"
date = 2014-04-17T15:04:00Z
updated = 2014-04-17T15:04:42Z
draft = true
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

I've been writing Go code for a while now, and I've come to the unfortunate conclusion that Go is boring. It really is. There's so many clever code constructs that it lacks, which just makes all my code incredibly boring.<br /><br />Take, for example, the humble ternary operator (a ? b : c). Can't do it in Go. This means that for a nice, concise statement like this (checking authorization):<br /><br /><br />
