+++
date = "2015-06-26T06:41:56-04:00"
title = "Testing os/exec.Command"
type = "post"
tags = ["go", "golang", "testing", "executables"]
+++

In [Juju](https://github.com/juju/juju), we often have code that needs to run external
executables.  Testing this code is a nightmare... because you really don't want
to run those files on the dev's machine or the CI machine.  But mocking out
os/exec is really hard.  There's no interface to replace, there's no function to
mock out and replace.  In the end, your code calls the Run method on the
exec.Cmd struct.

There's a bunch of bad ways you can mock this out - you can write out scripts to
disk with the right name and structure their contents to write out the correct
data to stdout, stderr and return the right return code... but then you're
writing platform-specific code in your tests, which means you need a Windows
version and a Linux version... It also means you're writing shell scripts or
Windows batch files or whatever, instead of writing Go.  And we all know that we
want our tests to be in Go, not shell scripts.

So what's the answer?  Well, it turns out, if you want to mock out exec.Command,
the best place to look is in the exec package's tests themselves.  Lo and
behold, it's right there in the first function of [exec\_test.go](https://github.com/golang/go/blob/master/src/os/exec/exec_test.go#L31)

	func helperCommand(t *testing.T, s ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--"}
		cs = append(cs, s...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		return cmd
	}

<sub><sup>(one line elided for clarity) </sup></sub>

What the heck is that doing?  It's pretty slick, so I'll explain it.

First off, you have to understand how tests in Go work.  When running `go test`,
the go tool compiles an executable from your code, runs it, and passes it the
flags you passed to `go test`.  It's that executable which actually handles the
flags and runs the tests.  Thus, while your tests are running, os.Args[0] is the
name of the test executable.

This function is making an exec.Command that runs the test executable, and
passes it the flag to tell the executable just to run a single test.  It then
terminates the argument list with `--` and appends the command and arguments
that would have been given to exec.Command to run *your* command.  

The end result is that when you run the exec.Cmd that is returned, it will run
the single test from this package called "TestHelperProcess" and os.Args will
contain (after the `--`) the command and arguments from the original call.

The environment variable is there so that the test can know to do nothing unless
that environment variable is set.

This is awesome for a few reasons:

- It's all Go code. No more needing to write shell scripts.
- The code run in the excutable is compiled with the rest of your test code.  No more needing to worry about typos in the strings you're writing to disk.
- No need to create new files on disk - the executable is already there and runnable, by definition.

So, let's use this in a real example to make it more clear.

In your production code, you can do something like this:

	var execCommand = exec.Command
	func RunDocker(container string) ([]byte, error) {
		cmd := execCommand("docker", "run", "-d", container)
		out, err := cmd.CombinedOutput()
	}

Mocking this out in test code is now super easy:

	func fakeExecCommand(command string, args...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
		return cmd
	}

	const dockerRunResult = "foo!"
	func TestRunDocker(t *testing.T) {
		execCommand = fakeExecCommand
		defer func(){ execCommand = exec.Command }()
		out, err := RunDocker("docker/whalesay")
		if err != nil {
			t.Errorf("Expected nil error, got %#v", err)
		}
		if string(out) != dockerRunResult {
			t.Errorf("Expected %q, got %q", dockerRunResult, out)
		}
	}

	func TestHelperProcess(t *testing.T){
		if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
			return
		}
		// some code here to check arguments perhaps?
		fmt.Fprintf(os.Stdout, dockerRunResult)
		os.Exit(0)
	}

Of course, you can do a lot more interesting things. The environment variables
on the command that fakeExecCommand returns make a nice side channel for telling
the executable what you want it to do.  I use one to tell the process to exit
with a non-zero error code, which is great for testing your error handling code.
You can see how the standard library uses its TestHelperProcess test
[here](https://github.com/golang/go/blob/master/src/os/exec/exec_test.go#L559).

Hopefully this will help you avoid writing really gnarly testing code (or even worse,
not testing your code at all).
