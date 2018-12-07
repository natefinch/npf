+++
title = "Starlight"
date = 2018-12-07T09:59:12-05:00
type = "post"
+++

I'd like to announce [starlight](https://github.com/starlight-go/starlight) Starlight uses google's
Go implementation of the [starlark python dialect](https://github.com/google/starlark-go) (most
notably found in the Bazel build tool). Starlight makes it super easy for users to extend your
application by writing simple python scripts that interact seamlessly with your current Go code...
with no boilerplate on your part.

## Parser by google

The parser and runner are maintained by google's bazel team, which write starlark-go.  Starlight is
a wrapper on top of that, which makes it so much easier to use starlark-go.  The problem with the
starlark-go API is that it is more built to be a used as configuration, so it assumes you want to get
information out of starlark and into Go.  It's actually pretty difficult to get Go information into
a starlark script.... unless you use starlight.

## Easy two-way interaction

Starlight has adapters that use reflection to automatically make any Go value usable in a starlark
script.  Passing an `*http.Request` into a starlark script?  Sure, you can do `name =
r.URL.Query()["name"][0]` in the python without any work on your part.

Starlight is built to *just work* the way you hope it'll work.  You can access any Go methods or
fields, basic types get converted back and forth seamlessly... and even though it uses reflection,
it's not as slow as you'd think.  A basic benchmark wrapping a couple values and running a starlark
script to work with them runs in a tiny fraction of a millisecond.

The great thing is that the changes made by the python code are reflected in your go objects,
just as if it had been written in Go.  So, set a field on a pointer to a struct? Your go code will
see the change, no additional work needed.

## 100% Safe

The great thing about starlark and starlight is that the scripts are 100% safe to run.  By default
they have no access to other parts of your project or system - they can't write to disk or connect
to the internet.  The only access they have to the outside is what you give them.  Because of this,
it's safe to run untrusted scripts (as long as you're not giving them dangerous functions to run,
like `os.RemoveAll`).  But at the same time, if you're only running trusted scripts, you can give
them whatever you want (`http.Get`?  Sure, why not?)

## Example

Below is an example of a webserver that changes its output depending on the python script it runs.  This is the full code, it's not truncated for readability... this is all it takes.

First the go web server code. Super standard stuff, except a few lines to run starlight...

```
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/starlight-go/starlight"
)

func main() {
	http.HandleFunc("/", handle)
	port := ":8080"
	fmt.Printf("running web server on http://localhost%v?name=starlight&repeat=3\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	fmt.Println("handling request", r.URL)
	// here we define the global variables and functions we're making available
	// to the script.  These will define how the script can interact with our Go
	// code and the outside world.
	globals := map[string]interface{}{
		"r":       r,
		"w":       w,
		"Fprintf": fmt.Fprintf,
	}
	_, err := starlight.Eval("handle.star", globals, nil)
	if err != nil {
		fmt.Println(err)
	}
}
```

And the python handle.star:

```
# Globals are:
# w: the http.ResponseWriter for the request
# r: the *http.Request
# Fprintf: fmt.Fprintf

# for loops and if statements need to be in functions in starlark
def main():
  # Query returns a map[string][]string
  
  # this gets a value from a map, with a default if it doesn't exist
  # and then takes the first value in the list.
  repeat = r.URL.Query().get("repeat", ["1"])[0]
  name = r.URL.Query().get("name", ["starlight"])[0]

  for x in range(int(repeat)):
    Fprintf(w, "hello %s\n", name)

  # we can use pythonic truthy statements on the slices returned from the map to
  # check if they're empty.
  if not r.URL.Query().get("repeat") and not r.URL.Query().get("repeat"):
    w.Write("\nadd ?repeat=<int>&name=<string> to the URL to customize this output\n")

  w.Write("\ntry modifying the contents of output.star and see what happens.\n")

main()
```

You can run this example by running `go get github.com/starlight-go/starlight` and using `go run
main.go` in the [example folder](https://github.com/starlight-go/starlight/tree/master/example).
You can then update the python and watch the changes the next time you hit the server.  This just
uses `starlight.Eval`, which rereads and reparses the script every time.

## Caching

In a production environment, you probably want to only read a script once and parse it once.  You
can do that with starlight's `Cache`.  This cache takes a list of directories to look in for
scripts, which it will read and parse on-demand, and then store the parsed object in memory for
later use.  It also uses a cache for any `load()` calls the scripts use to load scripts they depend
on.

## Work Ongoing

Starlight is still a work in progress, so don't expect the API to be perfectly stable quite yet.
But it's getting pretty close, and there shouldn't be any earth shattering changes, but definitely
pin your imports.  Right now it's more about finding corner cases where the starlight wrappers don't
work quite like you'd expect, and supporting the last few things that aren't implemented yet (like
channels).

