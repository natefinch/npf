+++
date = "2014-10-14T10:46:28-04:00"
title = "Why Everyone Hates Go"
type = "post"
tags = ["golang", "go", "programming"]
+++

Obviously, not *everyone* hates Go.  But there was a [quora
question](https://www.quora.com/Why-does-Go-seem-to-be-the- most-heavily-
criticised-among-the-newer-programming-languages?srid=uCiY&share=1) recently
about why everyone critizes Go so much. (sorry, I don't normally post links to
Quora, but it was the motivator for this post) Even before I saw the answers to
the question, I knew what they'd consist of:

* Go is a language stuck in the 70's.
* Go ignores 40 years of programming language research.
* Go is a language for blue collar (mediocre) developers.
* Gophers are ok with working in Java 1.0.

Unfortunately, the answers to the questions were more concerned with explaining
why Go is "bad", rather than why this gets under so many people's skin.

When reading the answers I had a eureka moment, and I realized why it is. So
here's my answer to the same question. This is why Go is so heavily criticized,
not why Go is "bad".

There's two awesome posts that inform my answer: Paul Graham's
[post](http://www.paulgraham.com/identity.html) about keeping your identity
small, and Kathy Sierra's [post](http://seriouspony.com/trouble-at-the-koolaid-
point) about the Koolaid point. I encourage you to read those two posts, as
they're both very informative.  I hesitate to compare the horrific things that
happen to women online with the pedantry of flamewars about programming
languages, but the Koolaid Point is such a valid metaphor that I wanted to link
to the article.

Paul says

>people can never have a fruitful argument about
something that's part of their identity 

*i.e.* the subject hits too close to home,
and their response becomes emotional rather than logical.

Kathy says 

>the hate wasn’t so much about the product/brand but that *other people were falling for it*. 

*i.e.* they'd drunk the kool-aid.

Go is the only recent language that takes the aforementioned 40 years of
programming language research and tosses it out the window. Other new languages
at least try to keep up with the Jones - Clojure, Scala, Rust - all try to
incorporate "modern programming theory" into their design. Go actively tries
not to. There is no pattern matching, there's no borrowing, there's no pure
functional programming, there's no immutable variables, there's no option types,
there's no exceptions, there's no classes, there's no generics.... there's a lot
Go doesn't have. And in the beginning this was enough to merely earn it scorn.
Even I am guilty of this. When I first heard about Go, I thought "What? No
exceptions? Pass."

But then something happened - people started *using* it. And liking it. And
building big projects with it. This is the Koolaid-point - where people have
started to drink the Koolaid and get fooled into thinking Go is a good
language. And this is where the scorn turns into derision and attacks on the
character of the people using it.

The most vocal Go detractors are those developers who write in ML-derived
languages (Haskel, Rust, Scala, *et al*) who have tied their preferred
programming language into their identity. The mere existence of Go says
"your views on what makes a good programming language are wrong". And the more
people that use and like Go, the more strongly they feel that they're being told
their choice of programming language - and therefore their identity - is wrong.

Note that basically no one in the Go community actually says this. But the Go
philosophy of simplicity and pragmatism above all else is the polar opposite of
what those languages espouse (in which complexity in the language is ok because
it enforces correctness in the code). This is insulting to the people who tie
their identity to that language. Whenever a post on Go makes it to the front
page of Hacker News, it is an affront to everything they hold dear, and so you
get comments like Go developers are stuck in the 70's, or is only for blue-collar devs.

So, this is why I think people are so much more vocal about their dislike of Go:
because it challenges their identity, and other people are falling for it. This
is also why these posts so often mention Google and how the language would have
died without them. Google is now the koolaid dispenser. The fact that they
are otherwise generally thought of as a very talented pool of developers means
that it is simultaneously more outrageous that they are fooling people and more
insulting that their language flies in the face of ML-derived languages.

**Update:  I removed the "panties in a bunch" comment, since I was (correctly)
scolded for being sexist, not to mention unprofessional.  My apologies to
anyone I offended.
