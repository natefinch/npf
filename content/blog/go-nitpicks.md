+++
date = "2014-10-28T06:17:21-04:00"
title = "Go Nitpicks"
type = "post"

+++

I saw this tweet last night:

<blockquote class="twitter-tweet" lang="en"><p>A code interview I like to ask:&#10;&#10;&quot;What would you change about &lt;your favourite language&gt;?&quot;&#10;&#10;Having nothing to say to that is a big strike.</p>&mdash; karlseguin (@karlseguin) <a href="https://twitter.com/karlseguin/status/526860386704695296">October 27, 2014</a></blockquote>
<script async src="//platform.twitter.com/widgets.js" charset="utf-8"></script>

I figured I'd answer it here about Go.  Luckily, Go is a very small language, so there's not a lot of surface area to dislike. However, there's definitely some things I wish were different. Most of these are nitpicks, thus the title.

#### #1 Bare Returns

	func foo() (i int, err error) {
		i, err = strconv.ParseInt("5") 
		return // wha??
	}

For all that Go promotes readable and immediately understandable code, this seems like a ridiculous outlier. The way it works is that if you don't declare what the function is returning, it'll return the values stored in the named return variables.  Which seems logical and handy, until you see a 100 line function with multiple branches and a single bare return at the bottom, with no idea what is actually getting returned.

To all gophers out there: don't use bare returns.  Ever.

#### #2 New

	a := new(MyStruct)

New means "Create a zero value of the given type and return a pointer to it".  It's sorta like the C `new`, which is probably why it exists.  The problem is that it's nearly useless.  It's mostly redundant with simply returning the address of a value thusly:

	a := &MyStruct{}

The above is a lot easier to read, it also gives you the ability to populate the value you're constructing (if you wish).  The only time new is "useful" is if you want to initialize a pointer to a builtin (like a string or an int), because you can't do this:

	a := &int

but you can do this:

	a := new(int)

Of course, you could always just do it in (*gasp*) two lines:

	a := 0
	b := &a

To all the gophers out there: don't use new. Always use &Foo{} with structs, maps, and slices. Use the two line version for numbers and strings. 

#### #3 Close

The close built-in function closes a channel. If the channel is already closed, close will panic.  This pisses me off, because most of the time when I call close, I don't actually care if it's already closed.  I just want to ensure that it's closed.  I'd much prefer if close returned a boolean that said whether or not it did anything, and then if **I** choose to panic, I can.  Or, you know, not.

#### #4 There is no 4

That's basically it.  There's some things I think are necessary evils, like `goto` and `panic`.  There's some things that are necessary ugliness, like the built-in functions `append`, `make`, `delete`, etc.  I sorta wish `x := range foo` returned the value in x and not the index, but I get that it's to be consistent between maps and slices, and returning the value in maps would be odd, I think. 

All these are even below the level of nitpicks, though.  They don't bug me, really.  I understand that everything in programming is a tradeoff, and I think the decisions made for Go were the right ones in these cases.  Sometimes you need goto.  Sometimes you need to panic.  Making those functions built-ins rather than methods on the types means you don't need any methods on the types, which keeps them simpler, and means they're "just data".  It also means you don't lose any functionality if you make new named types based on them.

So that's my list for Go.  

#### Postscript

Someone on the twitter discussion mentioned he couldn't think of anything he disliked about C#, which just about made me spit my coffee across the room.  I programmed in C# for ~9 years, starting out porting some 1.1 code to 2.0, and leaving as 5.0 came out.  The list of features in C# as of 5.0 is gigantic.  Even being a developer writing in it 40+ hours a week for 9 years, there was still stuff I had to look up to remember how it worked.  

I feel like my mastery of Go after a year of side projects was about equivalent to my mastery of C# after 9 years of full time development.  If we assume 1:1 correlation between time to master and size of the language, an order of magnitude sounds about right.

