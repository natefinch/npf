+++
title = "Retooling Retool"
date = 2019-05-10T21:42:34-04:00
type = "post"
+++

I love [retool](github.com/twitchtv/retool) - it is a great way to keep
development tools in sync across your team

However, retool [doesn't work with modules](https://github.com/twitchtv/retool/issues/49#issuecomment-471622108), and trying to run it with modules turned off causes weird behavior, and many tools just fail to compile if they use module-style imports.

So what to do? Well, it turns out that in the module world, retool can be replaced by a very small [mage](githib.com/magefile/mage) script.


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

// retool runs a command using a cached binary.
func retool(cmd string, args ...string) error {
	return sh.Run(filepath.Join("_tools", cmd), args...)
}

var tools = []string{
	"gnorm.org/gnorm@v1.0.0",
	"github.com/jackc/tern@v1.8.1",
	"github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.2.1",
	"github.com/matryer/moq@055cc3eebc2479586a18a9a835e2f3fdbae2f4e9",
}
```

The code is pretty simple, it ensures the _tools directory exists (which is where retool puts its binaries as well)
