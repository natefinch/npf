+++
title = "Gnorm"
type = "post"
date = "2018-10-02T15:55:43+01:00"
draft = true
+++


I'd like to introduce you to [Gnorm](https://gnorm.org).  Gnorm is a code
generating tool that reads the schema of your database and feeds that info into
your own templates to generate code, docs, protobufs, or any other kind of
textual output.  Gnorm is language and platform neutral - it can generate code
for any programming language, and can do so using any templating language.

Gnorm is highly flexible in its configuration.  It allows you to do smart
transforms of your database schema to make it easy to use in your
templates.  You can precisely control the files output by the generation,
including templated filepaths and multiple files per schema/table/etc.

I built gnorm after being frustrated with code-first ORMs that trying to
generate a DB from your code's datastructures, and then use magic to convert
those structures to SQL queries.  These types of ORMs always gave me fits,
because I knew how I wanted my database to look, but I couldn't figure out how
to contort my code to get the ORM to create that database.

What I want, and what Gnorm gives me, is the ability to define the exact
database schema that I want, using the tools built to do that - SQL and
migration tools. Then Gnorm takes over and generates exactly the right code to
use with that database and schema.

