package eventsourcing

// Topic defines the event sourcing journal topic.
//
// Every resulting event from an event sourced actor will be published into that topic to allow
// downstream consumers to process them. For instance one use case is to implement a Projection, another use case might be to push
// those events into kafka
const Topic = "goakt.es.journals"
