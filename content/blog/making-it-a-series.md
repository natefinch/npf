+++
date = 2014-08-08T11:12:44Z
title = "Making It a Series"
type = "post"
series = ["Hugo 101"]
+++


I obviously have a lot to talk about with Hugo, so I decided I wanted to make
this into a series of posts, and have links at the bottom of each post
automatically populated with the other posts in the series.  This turned out to
be somewhat of a challenge, but doable with some effort... hopefully someone
else can learn from my work.

This now brings us to [Taxonomies](http://hugo.spf13.com/taxonomies/overview).
Taxonomies are basically just like tags, except that you can have any number of different types of tags.  So you might have "Tags" as a taxonomy, and thus you can give a content tags with values of "go" and "programming".  Buy you can have a taxonomy of "series" and give content a series of "Hugo 101".

Taxonomy is sort of like relatable metadata to gather multiple pieces of content
together in a structured way... it's almost like a minimal relational database.
Taxonomies are listed in your site's metadata, and consist of a list of keys.
Each piece of content can specify one or more values for those keys (the Hugo
documentation calls the values "Terms").  The values are completely ad-hoc, and
don't need to be pre-defined anywhere.  Hugo automatically creates pages where
you can view all content based on Taxonomies and see how the various values are
cross-referenced against other content.  This is a way to implement tags on
posts, or series of posts.

So, for my example, we add a Taxonomy to my site config called "series".  Then
in this post, the "Hugo: Beyond the Defaults" post, and the "Hugo is Friggin' Awesome" post, I just add `series =
["Hugo 101"]`  (note the brackets - the values for the taxonomy are actually a
list, even if you only have one value).  Now all these posts are magically related together under a taxonomy called "series".  And Hugo automatically generates a listing for this taxonomy value at [/series/hugo-101](http://npf.io/series/hugo-101) (the taxonomy value gets url-ized).  Any other series I make will be under a similar directory.

This is fine and dandy and pretty aweomse out of the box... but I really want to
automatically generate a list of posts in the series at the bottom of each post
in the series.  This is where things get tricky, but that's also where things
get interesting.

The examples for [displaying
Taxonomies](http://hugo.spf13.com/taxonomies/displaying) all "hard code" the
taxonomy value in the template... this works great if you know ahead of time
what value you want to display, like "all posts with tag = 'featured'".
However, it doesn't work if you don't know ahead of time what the taxonomy value
will be (like the series on the current post).

This is doable, but it's a little more complicated.

I'll give you a dump of the relevant portion of my post template and then talk about how I got there:

```
{{ if .Params.series }}
    {{ $name := index .Params.series 0 }}
    <hr/>
	<p><a href="" id="series"></a>This is a post in the 
	<b>{{$name}}</b> series.<br/>
	Other posts in this series:</p>

    {{ $name := $name | urlize }}
    {{ $series := index .Site.Taxonomies.series $name }}
    <ul class="series">
    {{ range $series.Pages }}
    	<li>{{.Date.Format "Jan 02, 2006"}} -
    	<a href="{{.Permalink}}">{{.LinkTitle}}</a></li>
    {{end}}
    </ul>
{{end}} 
```

So we start off defining this part of the template to only be used if the post
has a series.  Right, sure, move on.

Now, the tricky part... the taxonomy values for the current page resides in the
.Params values, just like any other custom metadata you assign to the page.

Taxonomy values are always a list (so you can give things multiple tags etc),
but I know that I'll never give something more than one series, so I can just
grab the first item from the list.  To do that, I use the index function, which
is just like calling series[0] and assign it to the $name variable.

Now another tricky part... the series in the metadata is in the pretty form you
put into the metadata, but the list of Taxonomies in .Site.Taxonomies is in the
urlized form...  How did I figure that out?  Printf
debugging.  Hugo's auto-reloading makes it really easy to use the template
itself to figure out what's going on with the template and the data.  

When I started writing this template, I just put `{{$name}}` in my post template
after the line where I got the name, and I could see it rendered on webpage of
my post that the name was "Hugo 101".  Then I put `{{.Site.Taxonomies.series}}`
and I saw something like `map[hugo-101:[{0 0xc20823e000} {0 0xc208048580} {0
0xc208372000}]]`  which is ugly, but it showed me that the value in the map is
"hugo-101"... and I realized it was using the urlized version, so I used the
pre-defined hugo function `urlize` to convert the pretty series.

And from there it's just a matter of using `index` again, this time to use
`$name` as a key in the map of series....  .Site.Taxonomies is a map
(dictionary) of Taxonomy names (like "series") to maps of Taxonomy values (like
"hugo-101") to lists of pages.  So, .Site.Taxonomies.series reutrns a map of
series names to lists of pages... index that by the current series names, and
bam, list of pages.

And then it's just a matter of iterating over the pages and displaying them
nicely. And what's great is that this is now all automatic... all old posts get
updated with links to the new posts in the series, and any new series I make,
regardless of the name, will get the nice list of posts at the bottom for that series.
