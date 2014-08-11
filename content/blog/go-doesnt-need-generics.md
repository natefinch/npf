+++
title = "Go Doesn't Need Generics"
date = 2014-04-19T09:01:00Z
updated = 2014-04-19T09:01:08Z
draft = true
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

Ok, so the title is a little bit of clickbait.  In some limited instances, generics would be handy in Go.  But they're not nearly as essential in Go as they are in other languages.  Let me show you why.<br /><br />First off, Go already has generics.  You can make arrays, slices, and maps of whatever types you like.  It's not like the early java days where you had a Collection and had to cast everything in and out of it.<br /><br />What Go is missing, then, is user-defined generic data structures and functions.  However, what it has is interfaces and first class functions, which can go a long way toward bridging the gap. Given a tiny bit of boilerplate, you can even make most of a binary tree that is 100% type safe.<br /><br />However, for many applications, you don't need huge binary trees full of specialized data objects. Yes, for some applications you absolutely do need binary trees. You have two options in that case: write a type-specific binary tree type, or don't write that application in Go.  <br /><br />
