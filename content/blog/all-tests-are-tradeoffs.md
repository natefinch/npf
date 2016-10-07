+++
date = "2016-05-30T23:08:09-04:00"
draft = true
title = "All Tests Are Tradeoffs"
type = "post"

+++

There have been some discussions at work and (unrelated) on Hacker News about 
the questionable benefit of unit tests.  I don't agree that there's any question
about the usefulness of unit tests... but you can certainly write bad unit
tests.   Just as  you can write bad code in any language, so too, you can write
bad tests no matter what you call them, or what pattern you're following.  In
/ fact, what comprises a unit test is fairly vague.  What exactly is a unit of
code?  Is it a function?  A type/class?  A package/module/whatever?

In my opinion, all styles of testing have merit, in different ways.  As with
everything in computer science, there are tradeoffs.

Small, focused tests (such as a unit test that tests just one function) are easy
to write, easy to understand, and easy to grind deep into that one function's
many possible permutations.  There's usually very little setup needed,   However, if that function 