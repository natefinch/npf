+++
date = 2014-08-01T06:30:00Z
title = "Hugo is Friggin' Awesome"
+++

This is the first post of my new blog.  You may (eventually) see old posts
showing up behind here, those have been pulled in from my personal blog at
[blog.natefinch.com](http://blog.natefinch.com). I've decided to split off my
programming posts so that people who only want to see the coding stuff don't
have to see my personal posts, and people that only want to see my personal
stuff don't have to get inundated with programming posts.

This blog is powered by [Hugo](http://hugo.spf13.com), a static site generator
written by Steve Francia (aka spf13).  It is, of course, written in Go.  It is
pretty similar to [Jekyll](jekyllrb.com), in that you write markdown, run a
little program (hugo) and html pages come out the other end in the form of a
full static site.  What's different is that Jekyll is written in ruby and is
relatively slow, and Hugo is written in Go and is super fast... only taking a
few milliseconds to render each page.

Hugo includes a webserver to serve the content, which will regenerate the site
automatically when you change your content.  Your browser will update with the
changes immediately, making your development cycle for a site a very tight
loop.

The basic premise of Hugo is that your content is organized in a specific way on
purpose.  Folders of content and the name of the files combine to turn into the
url at which they are hosted. For example, content/foo/bar/baz.md will be hosted
at <site>/foo/bar/baz.

Every content file has a section of metadata at the top that allows you to
specify information about the content, like the title, date, even arbitrary data
for your specific site (for example, I have lists of badges that are shown on
pages for code projects).

All the data in a content file is just that - data.  Other than markdown
specifying a rough view of your page, the actual way the content is viewed is
completely separated from the data.  Views are written in Go's templating
language, which is quick to pick up and easy to use if you've used other
templating languages (or even if, like me, you haven't).  This lets you do
things like iterate over all the entries in a menu and print them out in a ul/li
block, or iterate over all the posts in your blog and display them on the main
page.

You can learn more about Hugo by going to [its site](http://hugo.spf13.com),
which, of course, is built using Hugo.

The static content for this site is hosted on github pages at
https://github.com/natefinch/natefinch.github.io. But the static content is
relatively boring... that's what you're looking at in your browser right now.
What's interesting is the code behind it.  That lives in a separate repo on
github at https://github.com/natefinch/npf.  This is where the markdown content
and templates live.

Here's how I have things set up locally... all open source code on my machine
lives in my GOPATH (which is set to my HOME).  So, it's easy to find anything I
have ever downloaded. Thus, the static site lives at
$GOPATH/src/github.com/natefinch/natefinch.github.io and the markdown +
templates lives in $GOPATH/src/github.com/natefinch/npf.  I created a symbolic
link under npf called public that points to the natefinch.github.io directory.
This is the directory that hugo outputs the static site to by default... that
way Hugo dumps the static content right into the correct directory for me to
commit and push to github.  I just had to add public to my .gitignore so
everyone wouldn't get confused.

Then, all I do is got to the npf directory, and run 

	hugo new post/urlofpost.md
	hugo server --buildDrafts --watch -t hyde

That generates a new content item that'll show up on my site under
/post/urlofpost.  Then it runs the local webserver so I can watch the content by
pointing a browser at localhost:1313 on a second monitor as I edit the post in a
text editor. hyde is the name of the theme I'm using, though I have modified
it.  Note that hugo will mark the content as a draft by default, so you need
--buildDrafts for it to get rendered locally, and remember to delete the draft =
true line in the page's metadata when you're ready to publish, or it won't show
up on your site.  

When I'm satisfied, kill the server, and run

	hugo -t hyde

to generate the final site output, switch into the public directory, and 

	git commit -am "some new post"

That's it.  Super easy, super fast, and no muss.  Coming from Blogger, this is
an amazingly better workflow with no wrestling with the WYSIWYG editor to make
it display stuff in a reasonable fashion.  Plus I can write posts 100% offline
and publish them when I get back to civilization.

There's a lot more to Hugo, and a lot more I want to do with the site, but that
will come in time and with more posts :)