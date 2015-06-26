+++
date = "2015-05-25T22:44:32-04:00"
title = "Go Plugins are as Easy as Pie"
type = "post"
tags = ["Go", "golang", "plugins"]
+++

When people hear that Go only supports static linking, one of the things they
eventually realize is that they can't have traditional plugins via dlls/libs (in
compiled languages) or scripts (in interpreted languages).  However, that
doesn't mean that you can't have plugins.  Some people suggest doing "compiled-
in" plugins - but to me, that's not a plugin, that's just code.  Some people
suggest just running sub processes and sending messages via their CLI, but that
runs into CLI parsing issues and requires runnnig a new process for every
request.  The last option people think of is using RPC to an external process,
which may also seem cumbersome, but it doesn't have to be.

### Serving up some pie

I'd like to introduce you to https://github.com/natefinch/pie - this is a Go
package which contains a toolkit for writing plugins in Go.  It uses processes
external to the main program as the plugins, and communicates with them via RPC
over the plugin's stdin and stout.  Having the plugin as an external process can
actually has several benefits:

- If the plugin crashes, it won't crash your process.
- The plugin is not in your process' memory space, so it can't do anything nasty.
- The plugin can be written in any language, not just Go.

I think this last point is actually the most valuable.  One of the nicest things
about Go applications is that they're just copy-and-run.  No one even needs to
know they were written in Go.  With plugins as external processes, this remains
true.  People wanting to extend your application can do so in the language of
their choice, so long as it supports the codec your application has chosen for
RPC.

The fact that the communication occurs over stdin and stdout means that there is
no need to worry about negotiating ports, it's easily cross platform compatible,
and it's very secure.

### Orthogonality

Pie is written to be a very simple set of functions that help you set up
communication between your process and a plugin process.  Once you make a couple
calls to pie, you then need to work out your own way to use the RPC connection
created.  Pie does not attempt to be an all-in-one plugin framework, though you
could certainly use it as the basis for one.

### Why is it called pie?

Because if you pronounce API like "a pie", then all this consuming and serving
of APIs becomes a lot more palatable.  Also, pies are the ultimate pluggable
interface - depending on what's inside, you can get dinner, dessert, a snack, or
even breakfast.  Plus, then I get to say that plugins in Go are as easy as...
well, you know.

### Conclusion

I plan to be using pie in one of my own side projects.  Take it out for a spin
in one of your projects and let me know what you think.  Happy eating!
