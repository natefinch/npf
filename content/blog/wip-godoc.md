+++
date = "2015-06-11T07:37:00-04:00"
title = "Sharing Godoc of a WIP Branch"
type = "post"
tags = ["Go", "golang", "godoc"]
+++

I had a problem yesterday - I wanted to use the excellent godoc.org to show
coworkers the godoc for the feature I was working on.  However, the feature was
on a branch of the main code in Github, and `go get` Does Not Work That Way™.
So, what to do?  Well, I figured out a hack to make it work.

https://gopkg.in is a super handy service that lets you point `go get` at
branches of your repo named vN (e.g. v0, v1, etc).  It also happens to work on
tags.  So, we can leverage this to get godoc.org to render the godoc for our WIP
branch.

From your WIP branch, simply do 

```
git tag v0
git push myremote v0
```

This creates a lightweight tag that only affects your repo (not upstream from
whence you forked).

You now can point godoc at your branch by way of gopkg.in:
https://godoc.org/gopkg.in/GithubUser/repo.v0

This will tell godoc to 'go get' your code from gopkg.in, and gopkg.in will
redirect the command to your v0 tag, which is currently on your branch.  Bam,
now you have godoc for your WIP branch on godoc.org.

Later, the tag can easily be removed (and reused if needed) thusly:

```
git tag -d v0
git push myremote :refs/tags/v0
```

So, there you go, go forth and share your godoc.  I find it's a great way to get
feedback on architecture before I dive into the reeds of the implementation.
