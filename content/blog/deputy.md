+++
date = "2015-06-30T12:44:29-04:00"
title = "Deputy"
type = "post"
tags = ["go", "golang", "executables", "package"]
+++

![deputy-sm](https://cloud.githubusercontent.com/assets/3185864/8237448/6bc30102-15bd-11e5-9e87-6423197a73d6.jpg)

<sup><sub>image: creative commons, &copy; [MatsuRD](http://matsurd.deviantart.com/art/Paper53-Deputy-Stubbs-342123485)</sub></sup>

<blockquote class="twitter-tweet" lang="en"><p lang="en" dir="ltr">I want to name a package &quot;lieutenant&quot;, but it&#39;s too hard to spell.</p>&mdash; Nate Finch (@NateTheFinch) <a href="https://twitter.com/NateTheFinch/status/610481962311131136">June 15, 2015</a></blockquote>
<script async src="//platform.twitter.com/widgets.js" charset="utf-8"></script>

True story.  The idea was this package would be a lieutenant commander (get
it?)... but I also knew I didn't want to have to try to spell lieutenant
correctly every time I used the package.  So that's why it's called deputy.
He's the guy who's not in charge, but does all the work.

### Errors

At [Juju](https://github.com/juju/juju), we run a lot of external processes
using os/exec. However, the default functionality of an exec.Cmd object is kind
of lacking. The most obvious one is those error returns "exit status 1".
Fantastic.  Have you ever wished you could just have the stderr from the command
as the error text?  Well, now you can, with deputy.

	func main() {
		d := deputy.Deputy{
			Errors:    deputy.FromStderr,
		}
		cmd := exec.Command("foo", "bar", "baz")
		err := d.Run(cmd)
	}

In the above code, if the command run by Deputy exits with a non-zero exit
status, deputy will capture the text output to stderr and convert that into the
error text.  *e.g.* if the command returned exit status 1 and output "Error: No
such image or container: bar" to stderr, then the error's Error() text would
look like "exit status 1: Error: No such image or container: bar".  Bam, the
errors from commands you run are infinitely more useful.

### Logging

Another idiom we use is to pipe some of the output from a command to our logs. This can be super useful for debugging purposes.  With deputy, this is again easy:

	func main() {
		d := deputy.Deputy{
			Errors:    deputy.FromStderr,
			StdoutLog: func(b []byte) { log.Print(string(b)) },
		}
		cmd := exec.Command("foo", "bar", "baz")
		err := d.Run(cmd)
	}


That's it.  Now every line written to stdout by the process will be piped as a
log message to your log.

### Timeouts

Finally, an idiom we don't use often enough, but should, is to add a timeout to
command execution.  What happens if you run a command as part of your pipeline
and that command hangs for 30 seconds, or 30 minutes, or forever?  Do you just
assume it'll always finish in a reasonable time?  Adding a timeout to running
commands requires some tricky coding with goroutines, channels, selects, and
killing the process... and deputy wraps all that up for you in a simple API:

	func main() {
		d := deputy.Deputy{
			Errors:    deputy.FromStderr,
			StdoutLog: func(b []byte) { log.Print(string(b)) },
			Timeout:   time.Second * 10,
		}
		cmd := exec.Command("foo", "bar", "baz")
		err := d.Run(cmd)
	}

The above code adds a 10 second timeout.  After that time, if the process has
not finished, it will be killed and an error returned.

That's it.  Give deputy a spin and let me know what you think.
