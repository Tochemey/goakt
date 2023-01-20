package projection

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/flowchartsman/retry"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/persistence"
	"github.com/tochemey/goakt/telemetry"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Handler is used to handle event and state consumed from the event store
type Handler func(ctx context.Context, persistenceID string, event *anypb.Any, state *anypb.Any, offset uint64) error

// Projection defines the projection
type Projection struct {
	mu sync.RWMutex
	// specifies the projection handler
	handler Handler
	// specifies the offset store
	offsetsStore persistence.OffsetStore
	// specifies the events store
	journalStore persistence.JournalStore
	// specifies the projection unique name
	// TODO: check whether we need it
	projectionName string
	// logger instance
	logger log.Logger
	// stop signal
	stopSignal chan struct{}
	// specifies the recovery setting
	recovery *RecoverySetting
}

// New create an instance of Projection given the name of the projection, the handler and the offsets store
func New(config *Config) *Projection {
	return &Projection{
		mu:             sync.RWMutex{},
		handler:        config.Handler,
		offsetsStore:   config.OffsetStore,
		projectionName: config.Name,
		journalStore:   config.JournalStore,
		logger:         config.Logger,
		recovery:       config.RecoverySetting,
	}
}

// Start starts the projection
func (p *Projection) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PreStart")
	defer span.End()

	// connect to the offset store
	if p.offsetsStore == nil {
		return errors.New("offsets store is not defined")
	}

	// call the connect method of the journal store
	if err := p.offsetsStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the offsets store: %v", err)
	}

	// connect to the events store
	if p.journalStore == nil {
		return errors.New("journal store is not defined")
	}

	// call the connect method of the journal store
	if err := p.journalStore.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to the journal store: %v", err)
	}

	// start processing
	go p.processingLoop(ctx)

	return nil
}

// Stop stops the projection
func (p *Projection) Stop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "PostStop")
	defer span.End()

	// send the stop
	close(p.stopSignal)

	// disconnect the events store
	if err := p.journalStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the journal store: %v", err)
	}

	// disconnect the offset store
	if err := p.offsetsStore.Disconnect(ctx); err != nil {
		return fmt.Errorf("failed to disconnect the offsets store: %v", err)
	}

	return nil
}

// processingLoop is a loop that continuously runs to process events persisted onto the journal store until the projection is stopped
func (p *Projection) processingLoop(ctx context.Context) {
	for {
		select {
		case <-p.stopSignal:
			return
		default:
			g, ctx := errgroup.WithContext(ctx)
			// define the channel holding the persistence id
			idsChan := make(chan string, 100)

			// fetch the persistence IDs
			g.Go(func() error {
				// close the channel when done
				defer close(idsChan)
				// let us fetch all the persistence ids
				ids, err := p.journalStore.PersistenceIDs(ctx)
				// handle the error
				if err != nil {
					return errors.Wrap(err, "failed to fetch the list of persistence IDs")
				}

				// push the fetched IDs onto the channel
				for _, id := range ids {
					idsChan <- id
				}
				return nil
			})

			// now let us process the fetched persistence IDs
			// start a fixed number of workers to process the persistence IDs read
			const workerCount = 20
			for i := 0; i < workerCount; i++ {
				g.Go(func() error {
					for persistenceID := range idsChan {
						if err := p.doProcess(ctx, persistenceID); err != nil {
							return err
						}
					}
					return nil
				})
			}

			// wait for all the processing to be done
			if err := g.Wait(); err != nil {
				// log the error
				p.logger.Error(err)
				switch p.recovery.RecoveryStrategy() {
				case pb.ProjectionRecoveryStrategy_FAIL, pb.ProjectionRecoveryStrategy_RETRY_AND_FAIL:
					// here we stop the projection
					err := p.Stop(ctx)
					// handle the error
					if err != nil {
						// log the error
						p.logger.Error(err)
						return
					}
					return
				default:
					// pass
				}
			}
		}
	}
}

// doProcess processes all events of a given persistent entity and hand them over to the handler
func (p *Projection) doProcess(ctx context.Context, persistenceID string) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "HandlePersistenceID")
	defer span.End()
	// get the latest offset persisted for the persistence id
	offset, err := p.offsetsStore.GetLatestOffset(ctx, persistence.NewProjectionID(p.projectionName, persistenceID))
	if err != nil {
		return err
	}

	// fetch events
	events, err := p.journalStore.ReplayEvents(ctx, persistenceID, offset.GetCurrentOffset()+1, math.MaxUint64, math.MaxUint64)
	if err != nil {
		return err
	}
	// grab the total number of events fetched
	eventsLen := len(events)
	for i := 0; i < eventsLen; i++ {
		// get the event envelope
		envelope := events[i]
		// grab the data to pass to the projection handler
		state := envelope.GetResultingState()
		event := envelope.GetEvent()
		seqNr := envelope.GetSequenceNumber()

		// send the request to the handler based upon the recovery strategy in place
		switch p.recovery.RecoveryStrategy() {
		case pb.ProjectionRecoveryStrategy_FAIL:
			// send the data to the handler. In case of error we log the error and fail the projection
			if err := p.handler(ctx, persistenceID, event, state, seqNr); err != nil {
				p.logger.Error(errors.Wrapf(err, "failed to process event for persistence id=%s, sequence=%d", persistenceID, seqNr))
				return err
			}

		case pb.ProjectionRecoveryStrategy_RETRY_AND_FAIL:
			// grab the max retries amd delay
			retries := p.recovery.Retries()
			delay := p.recovery.RetryDelay()
			// create a new exponential backoff that will try a maximum of retries times, with
			// an initial delay of 100 ms and a maximum delay
			backoff := retry.NewRetrier(int(retries), 100*time.Millisecond, delay)
			// pass the data to the projection handler
			if err := backoff.Run(func() error {
				if err := p.handler(ctx, persistenceID, event, state, seqNr); err != nil {
					p.logger.Error(errors.Wrapf(err, "failed to process event for persistence id=%s, sequence=%d", persistenceID, seqNr))
					return err
				}
				return nil
			}); err != nil {
				// because we fail we return the error without committing the offset
				return err
			}

		case pb.ProjectionRecoveryStrategy_RETRY_AND_SKIP:
			// grab the max retries amd delay
			retries := p.recovery.Retries()
			delay := p.recovery.RetryDelay()
			// create a new exponential backoff that will try a maximum of retries times, with
			// an initial delay of 100 ms and a maximum delay
			backoff := retry.NewRetrier(int(retries), 100*time.Millisecond, delay)
			// pass the data to the projection handler
			if err := backoff.Run(func() error {
				if err := p.handler(ctx, persistenceID, event, state, seqNr); err != nil {
					return err
				}
				return nil
			}); err != nil {
				// here we just log the error, but we skip the event and commit the offset
				p.logger.Error(errors.Wrapf(err, "failed to process event for persistence id=%s, sequence=%d", persistenceID, seqNr))
			}

			// here we commit the offset to the offset store and continue the next event
			offset = &pb.Offset{
				PersistenceId:  persistenceID,
				ProjectionName: p.projectionName,
				CurrentOffset:  seqNr,
				Timestamp:      timestamppb.Now().AsTime().UnixMilli(),
			}
			// write the given offset and return any possible error
			if err := p.offsetsStore.WriteOffset(ctx, offset); err != nil {
				return errors.Wrapf(err, "failed to persist offset for persistence id=%s, sequence=%d", persistenceID, seqNr)
			}

		case pb.ProjectionRecoveryStrategy_SKIP:
			// send the data to the handler. In case of error we just log the error and skip the event by committing the offset
			if err := p.handler(ctx, persistenceID, event, state, seqNr); err != nil {
				p.logger.Error(errors.Wrapf(err, "failed to process event for persistence id=%s, sequence=%d", persistenceID, seqNr))
			}

			// here we commit the offset to the offset store and continue the next event
			offset = &pb.Offset{
				PersistenceId:  persistenceID,
				ProjectionName: p.projectionName,
				CurrentOffset:  seqNr,
				Timestamp:      timestamppb.Now().AsTime().UnixMilli(),
			}
			// write the given offset and return any possible error
			if err := p.offsetsStore.WriteOffset(ctx, offset); err != nil {
				return errors.Wrapf(err, "failed to persist offset for persistence id=%s, sequence=%d", persistenceID, seqNr)
			}
		}
	}

	return nil
}
