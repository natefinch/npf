+++
date = 2014-08-13T14:09:51Z
draft = true
title = "Intro to TOML"
type = "post"

+++

TOML stands for Tom's Own Minimal Language.  It is a configuration language
vaguely similar to YAML or property lists, but far, far better.  But before we
get into it in detail, let's look back at what came before.

### Long Ago, In A Galaxy Far, Far Away

Since the beginning of computing, people have needed a way to configure
their software.  On Linux, this generally is done in text files.  For simple
configurations, good old foo = bar works pretty well.  One setting per line,
name on the left, value on the right, separated by an equals.  Great.  But when
your configuration gets more complicated, this quickly breaks down.  What if you
need a value that is more than one line?  How do you indicate a value should be
parsed as a number instead of a string?  How do you namespace related
configuration values so you don't need ridiculously long names to prevent
collisions?

### The Dark Ages

In the 90's, we used XML.  And it sucked.  XML is verbose, it's hard for humans
to read and write, and it still doesn't solve a lot of the problems above (like
how to specify the type of a value).  In addition, the XML spec is huge,
processing is very complicated, and all the extra features invite abuse and
overcomplication.

### Enlightenment

In the mid 2000's, JSON came to popularity as a data exchange format, and it was
so much better than XML.  It had real types, it was easy for programs to
process, and you didn't have to write a spec on what values should get processed
in what way (well, mostly).  It was sigificantly less verbose than XML.  But it
is a format intended for computers to read and write, not humans.  It is a pain
to write by hand, and even pretty-printed, it can be hard to read and the
compact data format turns into a nested mess of curly braces.  Also, JSON is not
without its problems... for example, there's no date type, there's no support
for comments, and all numbers are floats.

### A False Start

YAML came to popularity some time after JSON as a more human-readable format,
and its `key: value` syntax and pretty indentation is definitely a lot easier on
the eyes than JSON's nested curly-braces.  However, YAML trades ease of reading
for difficulty in writing.  Indentation as delimiters is fraught with error...
figuring out how to get multiple lines of data into any random value is an
exercise in googling and trial & error.

The YAML spec is also ridiculously long.  100% compatible parsers are very
difficult to write.  Writing YAML by hand is a ridden with landmines of corner
cases where your choice of names or values happens to hit a reserved word or
special marker.  It does support comments, though.

### The Savior

On February 23, 2013, Tom Preston-Werner (former CEO of GitHub) made his first
commit to https://github.com/toml-lang/toml.  TOML stands for Tom's Obvious,
Minimal Language.  It is a language designed for configuring software.  Finally.

TOML takes inspiration from all of the above (well, except XML) and even gets
some of its syntax from Microsoft's INI files.  It is easy to write by hand and
easy to read.  The spec is short and understandable by mere humans, and it's
fairly easy for computers to parse.  It supports comments, has first class
dates, and supports both integers and floats.  It is generally insensitive to
whitespace, without requiring a ton of delimiters.

Let's dive in.

### The Basics

The basic form is key = value

```
# Comments start with hash
foo = "strings are in quotes and are always UTF8 with escape codes: \n \u00E9"

bar = """multi-line strings
use three quotes"""

baz = 'literal\strings\use\single\quotes'

bat = '''multiline\literals\use
three\quotes'''

int = 5 # integers are just numbers
float = 5.0 # floats have a decimal point with numbers on both sides

date = 2006-05-27T07:32:00Z # dates are ISO 8601 full zulu form

bool = true # good old true and falss
```

One cool point:  If the first line of a multiline string (either literal or not)
is a line return, it will be trimmed.  So you can make your big blocks of text
start on the line after the name of the value and not need to worry about the
extraneous newline at the beginning of your text:

```
preabmle = """
We the people of the United States, in order to form a more perfect union,
establish justice, insure domestic tranquility, provide for the common defense,
promote the general welfare, and secure the blessings of liberty to ourselves
and our posterity, do ordain and establish this Constitution for the United
States of America."""
```

### Lists

Lists (arrays) are signified with brackets and delimited with commas.  Only
primitives are allowed in this form, though you may have nested lists.  The
format is forgiving, ignoring whitespace and newlines, and yes, the last comma
is optional (thank you!):

```
foo = [ "bar", "baz"
        "bat"
]

nums = [ 1, 2, ]

nested = [[ "a", "b"], [1, 2]]
```

I love that the format is forgiving of whitespace and that last comma.  I like
that the arrays are all of a single type, but allowing mixed types of sub-arrays
bugs the heck out of me.

### Now we get crazy

What's left?  In JSON there are objects, in YAML there are associative arrays...
in common parlance they are maps or dictionaries or hash tables.  Named
collections of key/value pairs.

In TOML they are called tables and look like this:

```
# some config above
[table_name]
foo = 1
bar = 2
```

Foo and bar are keys in the table called table_name.  Tables have to be at the
end of the config file. Why?  because there's no end delimiter.  All keys under
a table declaration are associated with that table, until a new table is
declared or the end of the file.  So declaring two tables looks like this:

```
# some config above
[table1]
foo = 1
bar = 2

[table2]
	foo = 1
	baz = 2
```

The declaration of table2 defines where table1 ends.  Note that you can indent
the values if you want, or not.  TOML doesn't care.

If you want nested tables, you can do that, too.  It looks like this:

```
[table1]
	foo = "bar"

[table1.nested_table]
	baz = "bat"
```

`nested_table` is defined as a value in `table1` because its name starts with
`table1.`.  Again, the table goes until the next table definition, so `baz="bat"`
is a value in `table1.nested_table`.  You can indent the nested table to make it
more obvious, but again, all whitespace is optional:

```
[table1]
	foo = "bar"

	[table1.nested_table]
		baz = "bat"
```

This is equivalent to the JSON:

```
{ 
	"table1" : {
		"foo" : "bar",
		"nested_table" : {
			"baz" : "bat"
		}
	}
}
```

Having to retype the parent table name for each sub-table is kind of annoying,
but I do like that it is very explicit.  It also means that ordering and
indenting and delimiters don't matter.  You don't have to declare parent tables
if they're empty, so you can do something like this:

```
[foo.bar.baz]
bat = "hi"
```

Which is the equivalent to this JSON:

```
{
	"foo" : {
		"bar" : {
			"baz" : {
				"bat" : "hi"
			}
		}
	}
}
```

### Last but not least

The last thing is arrays of tables, which are declared with double brackets
thusly:

```
[[comments]]
author = "Nate"
text = "Great Article!"

[[comments]]
author = "Anonymous"
text = "Love it!"
```

This is equivalent to the JSON:

```
{
	"comments" : [
		{
			"author" : "Nate",
			"text" : Great Article!"
		},
		{
			"author" : "Anonymous",
			"text" : Love It!"
		}
	]
}
```

Arrays of tables inside another table get combined in the way you'd expect, like
[[table1.array]].

TOML is very permissive here. Because all tables have very explicitly defined
parentage, the order they're defined in doesn't matter. You can have tables (and
entries in an array of tables) in whatever order you want.  This is totally
acceptable:

```
[[comments]]
author = "Anonymous"
text = "Love it!"

[foo.bar.baz]
bat = "hi"

[foo.bar]
howdy = "neighbor"

[[comments]]
author = "Anonymous"
text = "Love it!"
```

Of course, it generally makes sense to actually order things in a more organized
fashion, but it's nice that you can't shoot yourself in the foot if you reorder
things "incorrectly".

### Conclusion

That's TOML.  It's pretty awesome.  

There's a [list of parsers](https://github.com/toml-lang/toml#implementations)
on the TOML page on github for pretty much whatever language you want.  I
recommend [BurntSushi](http://github.com/BurntSushi/toml)'s for Go, since it
works just like the built-in parsers.

It is now my default configuration language for all the applications I write.

The next time you write an application that needs some configuration, take a
look at TOML.  I think your users will thank you.