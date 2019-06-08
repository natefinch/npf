+++
title = "Go Generate"
date = 2019-05-08T14:42:32-04:00
draft = true
type = "post"
+++

I love generating code. Many of the projects I've put the most effort into involve generating code. But I think `go generate` is the wrong tool for the job.

The biggest problem with go generate is that the commands live hidden in random files all over the codebase. It adds hidden dependencies to your code on 