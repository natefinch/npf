+++
title = "Announcing Gnorm"
type = "post"
date = "2017-09-05T15:55:43+01:00"
draft = true
+++

I'd like to announce my latest project - [GNORM](https://gnorm.org).  GNORM is
Not an ORM, it is a database-first code generator.  Gnorm is language agnostic -
it can generate whatever text you want - from HTML documentation to web APIs to
DB wrappers in any language.

Gnorm works by reading the schema of an existing database via SQL, and uses
templates you write to output text based on the schema.  It is configurable to
allow you to output one or many files in one or many directories.  It is
intended to be configurable in many useful ways without requiring much work to
get started.

