+++
title = "Using Cobra"
date = 2017-09-14T11:05:44-04:00
draft = true
+++

I started writing [Gnorm](https://github.com/gnormal/gnorm), and of course
wanted it to have a nice, usable CLI.  The state of the art is
[Cobra](https://github.com/spf13/cobra), used by such projects as Hugo (no
surprise) and Kubernetes.

Over all, Cobra is really great.  You can easily set up multi-level commands
with flags (posix compliant) in just a few easy-to-understand structures.  

However, there are a few API choices that I wish were different, to make it
easier to use Cobra in a way that I consider to be best practices.  That being
said, they're not dealbreakers, and I'll talk about how I work around them here.

Cobra is written to optimize the use case of having its commands created as
global variables, and/or set up in an init() function.  That's not great.... as
we all know, global variables are evil.  I think testing your CLI commands is
important, and making them global variables makes them harder to test.

The other weak point of Cobra is that the commands run functions

