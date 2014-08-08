+++
date = 2014-08-08T10:07:46Z
title = "Hugo: Beyond the Defaults"
type = "post"
series = ["Hugo 101"]
+++

In my last post, I had deployed what is almost the most basic Hugo site
possible.  The only reason it took more then 10 minutes is because I wanted to
tweak the theme.  However, there were a few things that immediately annoyed me.  

I didn't like having to type `hugo -t hyde` all the time.  Well, turns out
that's not necessary.  You can just put  `theme = "hyde"` in your site
config, and never need to type it again.  Sweet.  Now to run the local server, I
can just run `hugo server -w`, and for final generation, I can just run `hugo`.

Next is that my posts were under npf.io/post/postname ... which is not the end
of the world, but I really like seeing the date in post URLs, so that it's easy
to tell if I'm looking at something really, really old.  So, I went about
looking at how to do that.  Turns out, it's trivial.  Hugo has a feature called
[permalinks](http://hugo.spf13.com/extras/permalinks), where you can define the
format of the url for a section (a section is a top level division of your site,
denoted by a top level folder under content/).  So, all you have to do is, in
your site's config file, put some config that looks like this:

	[permalinks]
		post = "/:year/:month/:filename/"
		code = "/:filename/"

While we're at it, I had been putting my code in the top level content
directory, because I wanted it available at npf.io/projectname  .... however
there's no need to do that, I can put the code under the code directory and just
give it a permalink to show at the top level of the site.  Bam, awesome, done.

One note: Don't forget the slash at the end of the permalink.

But wait, this will move my "Hugo is Friggin' Awesome" post to a different URL,
and Steve Francia already tweeted about it with the old URL.  I don't want that
url to send people to a 404 page!
[Aliases](http://hugo.spf13.com/extras/aliases) to the rescue.  Aliases are just
a way to make redirects from old URLs to new ones.  So I just put `aliases =
["/post/hugo-is-awesome/"]` in the metadata at the top of that post, and now
links to there will redirect to the new location.  Awesome.

Ok, so cool... except that I don't really want the content for my blog posts
under content/post/ ... I'd prefer them under content/blog, but still be of type
"post".  So let's change that too.  This is pretty easy, just rename the folder
from post to blog, and then set up an
[archetype](http://hugo.spf13.com/content/archetypes) to default the metadata
under /blog/ to type = "post".  Archetypes are default metadata for a section,
so in this case, I make a file archetypes/blog.md and add type= "post" to the
archetype's metadata, and now all my content created with `hugo new
blog/foo.md` will be prepopulated as type "post".  (does it matter if the type
is post vs. blog?  no.  But it matters to me ;)

[@mlafeldt](https://twitter.com/mlafeldt) on Twitter pointed out my RSS feed was
wonky.... wait, I have an RSS feed?  Yes, Hugo [has that
too](http://hugo.spf13.com/templates/rss).  There are feed XML files
automatically output for most listing directories... and the base feed for the
site is a list of recent content.  So, I looked at what Hugo had made for me
(index.xml in the root output directory)... this is not too bad, but I don't
really like the title, and it's including my code content in the feed as well as
posts, which I don't really want.  Luckily, this is trivial to fix.  The RSS xml
file is output using a Go template just like everything else in the output.
It's trivial to adjust the template so that it only lists content of type
"post", and tweak the feed name, etc.

I was going to write about how I got the series stuff at the bottom of this
page, but this post is long enough already, so I'll just make that into its own
post, as the next post in the series! :)