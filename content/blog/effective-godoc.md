+++
title = "Effective Godoc"
date = 2014-04-01T06:43:00Z
updated = 2014-04-01T07:01:56Z
tags = ["Go", "programming", "golang"]
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

I started to write a blog post about how to get the most out of godoc, with examples in a repo, and then realized I could just write the whole post as godoc on the repo, so that's what I did. &nbsp;Feel free to send pull requests if there's anything you see that could be improved.<br /><br />I actually learned quite a lot writing this article, by exploring all the nooks and crannies of Go's documentation generation. &nbsp;Hopefully you'll learn something too.<br /><br />Either view the documentation on godoc.org:<br /><br /><a href="https://godoc.org/github.com/natefinch/godocgo">https://godoc.org/github.com/natefinch/godocgo</a><br /><br />or view it locally using the godoc tool:<br /><br /><pre style="background-color: whitesmoke; border-bottom-left-radius: 4px; border-bottom-right-radius: 4px; border-top-left-radius: 4px; border-top-right-radius: 4px; border: 1px solid rgb(204, 204, 204); box-sizing: border-box; color: #333333; font-family: Monaco, Menlo, Consolas, 'Courier New', monospace; font-size: 13px; line-height: 1.428571429; margin-bottom: 10px; overflow: auto; padding: 9.5px; word-break: normal; word-wrap: normal;">go get code.google.com/p/go.tools/cmd/godoc<br />go get github.com/natefinch/godocgo<br />godoc -http=:8080</pre><br />Then open a browser to <a href="http://localhost:8080/pkg/github.com/natefinch/godocgo">http://localhost:8080/pkg/github.com/natefinch/godocgo</a><br /><br />Enjoy!
