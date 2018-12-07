+++
title = "Designing Libraries for Users"
date = 2018-09-24T22:38:36-04:00
draft = true
type = "post"
+++

UX stands for user experience.  Every piece of software has users, and they all
have an experience when using your software.  Optimize your software to make it
a good experience, and your users will love your software.  It doesn't matter if
it's a backend library, a CLI tool, or a service in the cloud.  This article is
mostly focused on libraries, but it applies to the other products as well.

I often start writing software by designing the user experience I want first,
and then figuring out how to implement that later.  In this way, I'm not
restricting myself by what I think is possible or most performant, but I instead
focus on how it feels to *use* the end product.

## Actually Write Some Code

Pretend the library/tool/service already exists, and think about the common ways
people will want to interact with it.

Take a few of the common actions you expect people to perform with your code, and
actually write some code using your fictitious library.  DON'T SKIP THIS PART.
Actually write actual code in your actual editor.  It doesn't necessarily have
to run or compile (if applicable), but it needs to be as close to real
usage as possible.  Don't handwave conversion functions or setup functions...
that's actual code real users will need to write, so you need to write it, too,
to understand what it's like to interact with your code.  This is the only way
to really understand the ergonomics of your API.  

This is traditionally one of the stated benefits of test driven development,
which I think is fine, but in my experience, good tests rarely mimic real
systems at any significant depth.  Real tests carefully mock out all outside
resources, uses very controlled input, and sets up complicated metrics for
testing usage, outputs, etc ... real code doesn't do any of that.  Real code
interacts with real systems and generally trusts the library to do what it says
it does (and report errors as appropriate).

## Thing to Watch Out For

There are a bunch of standard tripping hazards that you can watch out for when
designing your software.

### Types

Do people have to bend over backwards to get their types to align with what your
library takes?  Try to think about what other systems a user will be using as
inputs to your library or what systems they'll feed outputs of your library
into.  Are you using types that make those conversions easy or hard?  

Are you using types that make the restrictions of your API clear?  One of the
key points to UX is making it easy to do the right thing, and hard or impossible
to do the wrong thing.  one of the benefit of statically typed languages is that
you *can't* pass a string into a function expecting an int.  But even in these
languages, it's easy to be misleading about your API.  If a function in your API
takes a string, but that string can only be one of three different values...
you're using the wrong type.  It shouldn't be a string, it should be an enum
(whatever that means in your language).

Choose the most restrictive type you can, without destroying ease of use...
and then ruthlessly check your inputs to make sure you return clear errors when
the type system can't properly define all the restrictions of your library.



## Usage Patterns

Are their footguns they have to avoid like not calling A before B?  