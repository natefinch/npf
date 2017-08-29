+++
type = "post"
date = "2017-08-29T12:40:19-04:00"
title = "Code Must Never Lie"
tags = ["programming"]
+++


> If you tell the truth, you don’t have to remember anything.
>
> —Mark Twain

In a code review recently, I asked the author to change some of their asserts to
requires. Functions in testify's assert package allow the test to continue,
whereas those in the require package end the test immediately. Thus, you use
require to avoid trying to continue running a test when we know it'll be in a
bad state.  (side note: don't use an assert package, but that's another post)
Since testify's assert and require packages have the same interface, the
author's solution was to simply change the import thusly:

```
import (
    assert "github.com/stretchr/testify/require"
)
```

Bam, now all the assert.Foo calls would stop the test immediately, and we didn't
need a big changelist changing every use of assert to require.  All good,
right?  

No.  

**Hell No.**

Why? Because it makes the code lie.  Anyone familiar with the testify package
understands the difference between assert and require.  But we've now made code
that *looks like* an assert, but is actually a require.  People who are 200
lines down in a test file may well not realize that those asserts are actually
requires. They'll assume the test function will continue processing after an
assert fails.  They'll be wrong, and they could accidentally write incorrect
tests because of it - tests that fail with confusing error messages.

This is true in general - **code must never lie**.  This is a cardinal sin
amongst programmers.  This is an extension of the mantra that code should be
written to be read.  If code looks like it's doing one thing when it's actually
doing something else, someone down the road will read that code and
misunderstand it, and use it or alter it in a way that causes bugs. If they're
lucky, the bugs will be immediate and obvious. If they're unlucky, they'll be
subtle and only be figured out after a long debugging session and much head
banging on keyboard. That someone might be you, even if it you was your code in
the first place.

If, for some reason, you have to make code that lies (to fulfill an interface or
some such), document the hell out of it.  Giant yelling comments that can't be
missed during a 2am debugging session.  Because chances are, that's when you're
going to look at this code next, and you might forget that saveToMemory()
function actually saves to a database in AWS's Antarctica region.

So, don't lie.  Furthermore, try not to even mislead.  Humans make assumptions
all the time, it's built into how we perceive the world.  As a coder, it's your
job to anticipate what assumptions a reader may have, and ensure that they are
not incorrect, or if they are, do your best to disabuse them of their incorrect
assumptions.

If possible, don't resort to comments to inform the reader, but instead,
structure the code itself in such a way as to indicate it's not going to behave
the way one might expect.  For example, if your type has a `Write(b []byte)
(int, error)` method that is not compatible with io.Writer, consider calling it
something other than Write... because everyone seeing `foo.Write` is going to
assume that function will work like an io.Write.  Instead maybe call it WriteOut
or PrintOut or anything but Write.

Misleading code can be even more subtle than this.  In a recent code review, the
author wrapped a single DB update in a transaction.  This set off
alarm bells for me as a reviewer.  As a reader, I assumed that the code must be
saving related data in multiple tables, and that's why a transaction was needed.
Turned out, the code didn't actually need the transaction, it was just written
that way to be consistent with some other code we had.  Unfortunately, in this
case, being consistent was actually confusing... because it caused the reader to
make assumptions that were ultimately incorrect.

Do the poor sap that has to maintain your code 6 months or two years down the
road a favor - don't lie. Try not to mislead.  Because even if that poor sap
isn't you, they still don't deserve the 2am headache you'll likely be
inflicting.