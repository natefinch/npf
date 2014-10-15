+++
title = "gocog"
date = 2013-01-25T08:33:00Z
updated = 2013-01-28T14:04:11Z
tags = ["programming", "golang"]
blogimport = true 
type = "post"
[author]
	name = "Nate Finch"
	uri = "https://plus.google.com/115818189328363361527"
+++

I recently got very enamored with <a href="http://golang.org/" target="_blank">Go</a>, and decided that I needed to write a real program with it to properly get up to speed. One thing came to mind after reading a lot on the <a href="https://groups.google.com/forum/#!forum/golang-nuts" target="_blank">Go mailing list</a>: a code generator.<br /><br />I had worked with <a href="http://nedbatchelder.com/" target="_blank">Ned Batchelder</a>&nbsp;at a now-defunct startup, where he developed <a href="http://nedbatchelder.com/code/cog/">cog.py</a>. I figured I could do something pretty similar with Go, except, I could do one better - Go generates native executables, which means you can run it without needing any specific programming framework installed, and you can run it on any major operating system. Also, I could construct it so that gocog supports any programming language embedded in the file, so long as it can be run via command line.<br /><br />Thus was born gocog -&nbsp;<a href="https://github.com/natefinch/gocog">https://github.com/natefinch/gocog</a><br /><br />Gocog runs very similarly to cog.py - you give it files to look at, and it reads the files looking for specially tagged embedded code (generally in comments of the actual text). Gocog extracts the code, runs it, and rewrites the file with the output of the code embedded.<br /><br />Thus you can do something like this in a file called test.html:<br /><pre class="brush: xml; gutter: false;">&lt;html&gt;<br />&lt;body&gt;<br />&lt;!-- [[[gocog<br />print "&lt;b&gt;Hello World!&lt;/b&gt;"<br />gocog]]] --&gt;<br />&lt;!-- [[[end]]] --&gt;<br />&lt;/body&gt;<br />&lt;/html&gt;<br /></pre><br />if you run gocog over the file, specifying python as the command to run:<br /><br />gocog test.html -cmd python -args %s -ext .py<br /><br />This tells gocog to extract the code from test.html into a &nbsp;file with the .py extension, and then run python &lt;filename&gt; and pipe the output back into the file.<br /><br />This is what test.html looks like after running gocog:<br /><br /><pre class="brush: xml; gutter: false;">&lt;html&gt;<br />&lt;body&gt;<br />&lt;!-- [[[gocog<br />print "&lt;b&gt;Hello World!&lt;/b&gt;"<br />gocog]]] --&gt;<br />&lt;b&gt;Hello World!&lt;/b&gt;<br />&lt;!-- [[[end]]] --&gt;<br />&lt;/body&gt;<br />&lt;/html&gt;<br /></pre><div><br /></div><div>Note that the generator code still exists in the file, so you can always rerun gocog to update the generated text. &nbsp;</div><div><br /></div><div>By default gocog assumes you're running embedded Go in the file (hey, I wrote it, I'm allowed to be biased), but you can specify any command line tool to run the code - python, ruby, perl, even compiled languages if you have a command line tool to compile and run them in a single step (I know of one for C# at least).</div><div><br /></div><div>"Ok", you're saying to yourself, "but what would I really do with it?" &nbsp;Well, it can be really useful for reducing copy and paste or recreating boilerplate. Ned and I used it to keep a schema of properties in sync over several different projects. Someone on Golang-nuts emailed me and is using it to generate boilerplate for CGo enum properties in Go.</div><div><br /></div><div>Gocog's sourcecode actually uses gocog - I embed the usage text into three different spots for documentation purposes - two in regular Go comments and one in a markdown file. &nbsp;I also use gocog to generate a timestamp in the code that gets displayed with the version information.</div><div><br /></div><div>You don't need to know Go to run Gocog, it's just an executable that anyone can run, without any prerequisites. &nbsp;You can download the binaries of the latest build from the gocog wiki here:&nbsp;<a href="https://github.com/natefinch/gocog/wiki">https://github.com/natefinch/gocog/wiki</a></div><div><br /></div><div>Feel &nbsp;free to submit an issue if you find a bug or would like to request a feature.</div>