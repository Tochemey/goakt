# Routers

Routers help send the same type of message to a set of actors to be processed in parallel depending upon
the type of the router used. Routers should be used with caution because they can hinder performance
Go-Akt comes shipped with the following routers:

## Broadcast

This router broadcasts the given message to all its available routees in parallel. 
When the router receives a message to broadcast, every routee is checked whether alive or not.
When a routee is not alive the router removes it from its set of routees.
When the last routee stops the router itself stops.

## Random

This type of router randomly pick a routee in its set of routees and send the message to it.
Only routees that are alive are considered when the router receives a message.

## Round Robin

This type of router rotates over the set of routees making sure that if there are n routees.
Only routees that are alive are considered when the router receives a message.
For n messages sent through the router, each actor is forwarded one message.