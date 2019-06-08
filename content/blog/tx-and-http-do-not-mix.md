+++
title = "Transactions and HTTP Do Not Mix"
date = 2019-06-06T23:09:00-04:00
draft = true
type = "post"
+++

Unless you have a very boring system, your server probably does two very common things: it talks to a database, and it talks to other servers.

One of the great things about relational databases is transactions. You can save multiple pieces of data and if any of them fail, the whole thing rolls back automatically and you don't get half-saved data.  Awesome.  Now say you're doing that, you open a transaction, you're saving some data, reading other data, and then you need some data from an external system to save to your DB... you just make an HTTP call right? NO!

Here's the thing, the database transaction holds onto a database connection throughout the life of the transaction.  Not only that, but it can lock rows that block other changes from going through until the transaction runs.  Now imagine the HTTP server on the other end of your call hangs for 10 seconds. Now your DB connection is tied up for 10 seconds, the rows are possibly locked for 10 seconds... badness ensues.

We hit this kind of problem on a recent loadtest at Mattel.  A third party service that normally returns very quickly, got bogged down during the load test and suddenly started timing out.  Deep inside our code, we had abstracted away these calls so they just look like fnuciton calls (as you do)... but it means that we were actually doing web calls in the middle of an open database transaction. Badness ensued. The number of open database connections skyrocketed, requests to our service started timing out...

Luckily, we had a pretty good idea of what the problem was, because we'd already been looking into exactly this problem... there were just a bunch of places we hadn't gotten to yet. Well, there's no time like the present.

Now, detangling all these calls is hard enough. Most of the HTTP calls inside transactions followed two general patterns: either a function was trying to do two things at once (DoThingOrOtherThing), or it didn't need a transaction at all. 


For the first... very often we'd be doing calls out to external services to read/write data and then use the returned data to save stuff to the database. This is easy to fix by making them separate functions, and putting the external access code outside the transaction, and then use the data it returns in a transaction. 

For the second, lots of times we do multiple reads from the database, and then use that data to write back to the DB, but all the writes end up using foreign keys to the data we read, so if those rows were to be deleted in the middle, our writes would just fail with a foreign key violation, and any previously written rows would have been deleted via a cascading delete. No big deal.  (Also, deletes are super uncommon in our code)

So, we can fix this through careful examination and refactoring the code... but how do we prevent it in the first place.  As a developer working on a largish project, how do you know if any random interface method calls out to an external server or if it's in-memory? It's just an interface, right? Well, I have an idea about that, though to be honest, I don't know if it's a good idea or not. 

What I want is a flag I can put on a function that makes it very clear from the line where you call the function that it should not be called while a database transaction is open.  I think we can do this without 