+++
date = "2015-10-22T00:00:19-04:00"
draft = true
title = "Testing Tips"
type = "post"
tags = ["testing", "tests"]
+++

Tests exert force on your code.  Thus, bad habits in your tests can actually
produce worse code.  Of course, the opposite is true as well - good habits in
your tests can produce better code.  The test driven development folks know this
well, which is why TDD works as well as it does (aside from just forcing you to
write tests).

Today I'm going to look at some bad habits in testing, and how they can hinder
your development processes.

## Don't test what you don't care about

How often have you changed one piece of code and had dozens of tests fail - many
of them seemingly testing things completely unrelated to your change?  This can
happen when your tests are testing more than they really should.  Far too often
I see tests that test the exact wording of an error message.  Does your test
really care if the error message is `item "foo" not found` vs `item foo not
found`?  I hope not.  So don't test that.  Instead, have a way to check for the
item not found error without looking at its error message. This is an example of
your tests exerting positive pressure on your code - it's making you think about
the right way to check your errors.
