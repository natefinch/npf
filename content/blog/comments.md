+++
title = "Comment Your Code"
date = 2017-11-17T14:48:09-05:00
draft = false
type="post"
+++

There's a disturbing thread that pops up every once in a while where People On
The Internet say that comments are bad and the only reason you need them is
because you and/or your code aren't good enough.  I'm here to say that's bullshit.  

## Code Sucks

They're not entirely wrong... your code isn't good enough.  Neither is mine or
anyone else's.  Code sucks.  You know when it sucks the most?  When you haven't
touched it in 6 months.  And you look back at the code and wonder "what in the
hell was the author thinking?" (and then you git blame and it's you... because
it's always you).

The premise of the anti-commenters is that the only reason you need comments is
because your code isn't "clean" enough.  If it were refactored better, named
better, written better, it wouldn't need that comment.  

But of course, what is clean and obvious and well-written to you, today, while
the entire project and problem space are fully loaded in your brain... might not
be obvious to you, six months from now, or to the poor schmuck that has to debug
your code with their manager breathing down their neck beacuse the CTO just ran
into a critical bug in prod.  

Learning to look at a piece of code that you understand, and trying to figure out
how someone else might fail to understand it is a difficult skill to master. But
it is incredibly valuable... one that is nearly as important as the
ability to write good code in the first place.  In industry, almost no one codes
alone.  And even if you *do* code alone, you're gonna forget why you wrote some
of your code, or what exactly this gnarly piece of late night "engineering" is
doing.  And someday you're going to leave, and the person they hire to replace
you is going to have to figure out every little quirk that was in your head at
the time.

So, throwing in comments that may seem overly obvious in the moment is not a bad
thing. Sometimes it can be a huge help.

## Avoiding Comments Often Makes Your Code Worse

Some people claim that if you remove comments, it makes your code better,
because you have to make your code clearer to compensate.  I call BS on this as
well, because I don't think anyone is realistically writing sub-par code and
then excusing it by slapping a comment on it (aside from `// TODO: this is a
temporary hack, I'll fix it later`).  We all write the best code we know howm,
given the various external constraints (usually time).

The problem with refactoring your code to avoid needing comments is that
it often leads to *worse* code, not better.  The canonical example is factoring
out a complicated line of code into a function with a descriptive name.  Which
sounds good, except now you've introduced a context switch for the person reading
the code.. instead of the actual line of code, they have a function call... they
have to scroll to where the function call is, remember and map the arguments
from the call site to the function declaration, and then map the return value
back to the call site's return.

In addition, the clarity of a function's name is only applicable to very trivial
comments.  Any comment that is more than a couple words cannot (or should not)
be made into a function name.  Thus, you end up with... a function with a
comment above it.

Indeed, even the existence of a very short function may cause confusion and more
complicated code.  If I see such a function, I may search to see where else that
function is used. If it's only used in one place, I then have to wonder if this
is actually a general piece of code that represents global logic... (e.g.
`NameToUserID`) or if this function is bespoke code that relies heavily on the
specific state and implementation of its call site and may well not do the right
thing elsewhere. By breaking it out into a function, you're in essence exposing
this implementation detail to the rest of the codebase, and this is not a
decision that should be taken lightly. Even if you know that this is not
actually a function anyone else should call, someone else *will* call it at some
point, even where not appropriate.

The problems with small functions are better detailed in Cindy Sridharan's [medium post](https://medium.com/@copyconstruct/small-functions-considered-harmful-91035d316c29).

We could dive into long variable names vs. short, but I'll stop and just
say that you can't save yourself by making variable names longer.  Unless your
variable name is the entire comment that you're avoiding writing, then you're
still losing information that could have been added to the comment.  And I think
we can all agee that `usernameStrippedOfSpacesWithDotCSVExtension` is a terrible
variable name. 

I'm not trying to say that you shouldn't strive to make your code clear and
obvious.  You definitely should.  It's the hallmark of a good developer.  But
code clarity is orthogonal to the existence of comments.  And good comments are
*also* the hallmark of a good developer.

## There are no bad comments

The examples of bad comments often given in these discussions are trivially
bad, and almost never encountered in code written outside of a programming 101
class.

```
// instantiate an error
var err error
```

Yes, clearly, this is not a useful comment.  But at the same time, it's not
really *harmful*.  It's some noise that is easily ignored when browsing the
code.  I would rather see a hundred of the above comments if it means the dev
leaves in one useful comment that saves me hours of head banging on keyboard.

I'm pretty sure I've never read any code and said "man, this code would be so
much easier to understand if it weren't for all these comments."  It's nearly
100% the opposite.


In fact, I'll even call out some code that I think is egregious in its lack of
comments - the Go standard library.  While the code may be very correct and well
structured.. in many cases, if you don't have a deep understanding of what the
code is doing *before* you look at the it, it can be a challenge to understand
why it's doing what it's doing.  A sprinkling of comments about what the logic
is doing and why would make a lot of the go standard library a lot easier to
read.  In this I am specifically talking about comments inside the
implementation, not doc comments on exported functions in general (those are
generally pretty good).

## Any comment is better than no comment

Another chestnut the anti-commenters like to bring out is the wisdom can be
illustrated with a pithy image:

{{< figure src="/comments.jpg" width="200" >}}

Ah, hilarious, someone updated the contents and didn't update the comment.

But, that was a problem 20 years ago, when code reviews were not (generally) a
thing.  But they are a thing now.  And if checking that comments match the
implementation isn't part of your code review process, then you should probably
review your code review process.  

Which is not to say that mistakes can't be made... in fact I filed a "comment
doesn't match implementation" bug just yesterday.  The saying goes something
like "no comment is better than an incorrect comment" which sounds obviously
true, except when you realize that if there is no comment, then devs will just
*guess* what the code does, and probably be wrong more often than a comment would
be wrong.

Even if this *does* happen, and the code has changed, you still have valuable
information about what the code used to do.  Chances are, the code still does
basically the same thing, just slightly differently.  In this world of
versioning and backwards compatbility, how often does the same function get
drastically changed in functionality while maintaining the same name and
signature?  Probably not often.

Take the bug I filed yesterday... the place where we were using the function was
calling `client.SetKeepAlive(60)`.  The comment on SetKeepAlive was
"SetKeepAlive will set the amount of time (in seconds) that the client should
wait before sending a PING request". Cool, right? Except I noticed that
SetKeepAlive takes a time.Duration.  Without any other units specified for the
value of 60, Go's duration type defaults to.... nanoseconds.  Oops.  Someone had
updated the function to take a Duration rather than an Int.  Interestingly, it
*did* still round the duration down to the nearest second, so the comment was
not incorrect per se, it was just misleading.

## Conclusion

I feel like the line between what's a useful comment and what's not is difficult
to find (outside of trivial examples), so I'd rather people err on the
side of writing too many comments.  You never know who may be reading your code
next, so do them the favor you wish was done for you... write a bunch of
comments.  Keep writing comments until it feels like too many, then write a few
more.  That's probably about the right amount.
