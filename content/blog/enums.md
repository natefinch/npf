+++
date = "2015-12-02T00:00:19-04:00"
draft = false
title = "To Enum or Not To Enum"
type = "post"
tags = ["go", "golang", "enums"]
+++

Enum-like values have come up in my reviews of other people's code a few times, and I'd like to nail down what we feel is best practice.

I've seen many places what in other languages would be an enum, i.e. a bounded list of known values that encompass every value that should ever exist.  

The code I have been critical of simply calls these values strings, and creates a few well-known values, thusly:
package tool

// types of tools
const (
    ScrewdriverType = "screwdriver"
    HammerType = "hammer"
   // ...
)

type Tool struct {
    typ string
}

func NewTool(tooltype string) (Tool, error) {
    switch tooltype{
        case ScrewdriverType, HammerType:
            return Tool{typ:tooltype}, nil
        default:
            return Tool{}, errors.New("invalid type")
    }
}
The problem with this is that there's nothing stopping you from doing something totally wrong like this:
name := user.Name()

// ... some other stuff

a := NewTool(name)
That would fail only at runtime, which kind of defeats the purpose of having a compiler.

I'm not sure why we don't at least define the tool type as a named type of string, i.e.
package tool

type ToolType string

const (
    Screwdriver ToolType = "screwdriver"
    Hammer = "hammer"
   // ...
)

type Tool struct {
    typ ToolType
}

func NewTool(tooltype ToolType) Tool {
        return Tool{typ:tooltype}
}
Note that now we can drop the error checking in NewTool because the compiler does it for us.  The ToolType still works in all ways like a string, so it's trivial to convert for printing, serialization, etc.

However, this still lets you do something which is wrong but might not always look wrong:
a := NewTool("drill")
Because of how Go constants work, this will get converted to a ToolType, even though it's not one of the ones we have defined.

The final revision, which is the one I'd propose, removes even this possibility, by not using a string at all (it also uses a lot less memory and creates less garbage):
package tool

type ToolType int

const (
    Screwdriver ToolType = iota
    Hammer
   // ...
)

type Tool struct {
    typ ToolType
}

func NewTool(tooltype ToolType) Tool {
        return Tool{typ:tooltype}
}
This now prevents passing in a constant string that looks like it might be right. You can pass in a constant number, but NewTool(5) is a hell of a lot more obviously wrong than NewTool("drill"), IMO.

The push back I've heard about this is that then you have to manually write the String() function to make human-readable strings... but there are code generators that already do this for you in extremely optimized ways (see https://github.com/golang/tools/blob/master/cmd/stringer/stringer.go) 