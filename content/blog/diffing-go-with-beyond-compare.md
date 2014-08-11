+++
title = "Diffing Go with Beyond Compare"
date = 2014-05-14T13:09:00Z
updated = 2014-05-14T13:09:37Z
tags = ["Beyond Compare", "Go", "programming", "golang", "diff"]
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

I love Beyond Compare, it's an awesome visual diff/merge tool. &nbsp;It's not free, but I don't care, because it's awesome. &nbsp;However, there's no built-in configuration for Go code, so I made one. &nbsp;Not sure what the venn diagram of Beyond Compare users and Go users looks like, it might be that I'm the one point of crossover, but just in case I'm not, here's the configuration file for Beyond Compare 3 for the Go programming language:&nbsp;<a href="http://play.golang.org/p/G6NWE0z1GC">http://play.golang.org/p/G6NWE0z1GC</a> &nbsp;(please forgive the abuse of the Go playground)<br /><br />Just copy the text into a file and in Beyond Compare, go to Tools-&gt;Import Settings... and choose the file. &nbsp;Please let me know if you have any troubles or suggested improvements.
