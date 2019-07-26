+++
title = "Games, Names, and Going to 60k CCU"
date = 2019-06-17T08:17:35-04:00
draft = true
type = "post"
+++

On Friday, June 14th at 8am, the press embargo lifted for Mattel's newest – and by some accounts, largest – product launch ever: Hotwheels ID. While the game and smart track are covered well elsewhere, some of the more interesting technical pieces flew under the radar. 

Hotwheels ID is a unique product unlike almost any other – combining a mobile game / app, a hardware smart track connected via bluetooth to the phone, the phone scanning NFC chips in hotwheels cars, an online game engine backend, and something you rarely see online games - support for children under the age of 13 creating persistent accounts.

Let's talk about the last part first. Hotwheels ID's user management leverages a new service that my team created, called Mattel Login. This is a standard oauth2 service that supports both adults and children under 13 (or whatever the local age of consent is for your country). Adults register with an email and password (bcrypt hashed, of course) from an oauth2 provider my team wrote in Go (and which we eventually intend to open source). 

Children are shunted off into a [3rd party oauth service](https://www.superawesome.com/) specifically designed for children.  They do not give their own email address, but instead register with a username and password, and a parent's email. Both these login backends are then federated through my team's main oauth service so that other systems may use them transparently, passing around auth tokens as per normal oauth2.

One part of our service has been in production for months – a gRPC (written in Go, naturally) OTA (over the air) firmware downloading service. Teams at Mattel can use gRPC or REST calls (using GCP's extensible service proxy) to easily upload and then have devices request new versions of firmware. Currently-shipping devices are already using this service to keep their firmware up to date, and the new Hotwheels Smart Track uses it as well.

The final bit of our system is a gRPC API that stores product information and validates the data from NFC chips. The unique part of this API is the ability to use the unique ID in an NFC chip to keep virtual stats on one specific real-world object. In our case it is hotwheels cars. We can track how far a your specific car has driven, even if you lend it to a friend or get it off ebay. The stats you bring up on your phone when you scan it are unique to that physical car.  This is a merging of the digital and real world that is fascinating to me.

Getting all this working was a hell of a feat of engineering and cross-team communication. The backend team and the firmware team and the hardware team and the mobile app team all needed to coordinate very carefully so that we could take info from the car to the track to the app to the backend.  We had to create a unique way to verify the authenticity of any car the app scans. This is where Hotwheels mass production had to coordinate with our backend team. Production had to give each car an NFC chip with a unique piece of information, which we call the birth certificate.  This is a unique ID and product ID of the specific car, signed by a certificate that the backend could then use to verify the authenticity of this car.



