+++
title = "Retooling Retool"
date = 2019-05-11T21:42:34-04:00
type = "post"
+++

I was so happy when I discovered [retool](github.com/twitchtv/retool). It's a go tool that builds and caches go binaries into a local directory so that your dev tools stay in sync across your team. It fixes all those problems where slight difference in binary versions produce different output and cause code churn. We use it at Mattel for our projects, because we tend to have a large number of external tools that we use for managing code generation, database migrations, release management, etc.

However, retool [doesn't work very well with modules](https://github.com/twitchtv/retool/issues/49#issuecomment-471622108), and trying to run it with modules turned off sometimes misbehaves, and some tools just fail to compile that way.

So what to do? Well, it turns out that in the module world, retool can be replaced by a very small [mage](https://github.com/magefile/mage) script:


```
func Tools() error {
	update, err := envBool("UPDATE")
	if err != nil {
		return err
	}

	if err := os.MkdirAll("_tools", 0700); err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	env := map[string]string{"GOBIN": filepath.Join(wd, "_tools")}
	args := []string{"get"}
	if update {
		args = []string{"get", "-u"}
	}
	for _, t := range tools {
		err := sh.RunWith(env, "go", append(args, t)...)
		if err != nil {
			return err
		}
	}
	return nil
}
```

This code is pretty simple — it ensures the `_tools` directory exists (which is where retool puts its binaries as well, so I just reused that spot since our .gitignore already ignored it). Then it sets `GOBIN` to the _tools directory, so binaries built by the go tool will go there, and runs `go get importpath@<tag|hash>`. That's it. The first time, it'll take a while to download all the libraries it needs to build the binaries into the modules cache, but after that it'll figure out it doesn't need to do anything pretty quick.

Now just use the tool helper function below in your magefile to run the right versions of the binaries (and/or add _tools to your PATH if you use something like direnv).

```
// tool runs a command using a cached binary.
func tool(cmd string, args ...string) error {
	return sh.Run(filepath.Join("_tools", cmd), args...)
}
```

Now all the devs on your team will be using the same versions of their (go) dev tools, and you don't even need a fancy third party tool to do it (aside from mage).
The list of tools then is just a simple slice of strings, thusly:

```
var tools = []string{
	"github.com/jteeuwen/go-bindata/go-bindata@6025e8de665b31fa74ab1a66f2cddd8c0abf887e",
	"github.com/golang/protobuf/protoc-gen-go@v1.3.1",
	"gnorm.org/gnorm@v1.0.0",
	"github.com/goreleaser/goreleaser@v0.106.0",
}
```

For most maintained libraries, you'll get a nice semver release number in there, so it's perfectly clear what you're running (but for anything without tags, you can use a commit hash).

I'm really happy that this was as straightforward as I was hoping it would be, and it seems just as usable as retool for my use case.