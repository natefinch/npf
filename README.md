npf.io
===

This is the code behind the site [npf.io](https://npf.io), used by
[Hugo](https://gohugo.io) to generate a static HTML site. Pushes to `master`
are built and deployed to GitHub Pages by
[.github/workflows/hugo.yml](.github/workflows/hugo.yml).

## Running locally

The CI pins a specific Hugo version — use the same one locally to avoid
surprises. Check the current pin in the workflow file (look for
`hugo-version:`).

### Install Hugo

macOS (Homebrew):

```sh
brew install hugo
```

Or download a release binary matching the pinned version from
<https://github.com/gohugoio/hugo/releases>.

Verify the version:

```sh
hugo version
```

### Live preview

From the repo root:

```sh
hugo server -D
```

Then open <http://localhost:1313>. The server watches for changes and
live-reloads the browser. `-D` includes draft posts.

### Production build

Build the same output CI produces into `./public/`:

```sh
hugo --minify
```

A clean build should finish with no `ERROR` or `WARN` lines. Deprecation
warnings are worth fixing — they break on the next Hugo release.

### Authoring a new post

```sh
hugo new blog/my-new-post.md
```

Set `draft = false` in the front matter when ready to publish.
