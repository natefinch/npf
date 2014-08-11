+++
title = "Using the Windows API in Go"
date = 2013-10-15T09:12:00Z
updated = 2013-10-15T09:12:24Z
tags = ["Go", "programming", "golang"]
draft = true
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

Go, as you probably know, is cross platform. &nbsp;The <a href="http://golang.org/pkg/syscall/" target="_blank">syscall</a> package is the main interface with the operating system, and is used by the <a href="http://golang.org/pkg/os/" target="_blank">os</a> package (among others) to provide a way to create files, open sockets, etc. &nbsp;The syscall package wraps a number of Windows API endpoints, but it is far from a complete list.
