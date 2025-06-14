/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/timer"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/passivation"
)

type nameType int

const (
	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = time.Minute
	// DefaultInitTimeout defines the default init timeout
	DefaultInitTimeout = time.Second
	// DefaultPeerStateLoopInterval defines the default peer state loop interval
	DefaultPeerStateLoopInterval = 10 * time.Second
	// DefaultAskTimeout defines the default ask timeout
	DefaultAskTimeout = 5 * time.Second
	// DefaultMaxReadFrameSize defines the default HTTP maximum read frame size
	DefaultMaxReadFrameSize = 16 * size.MB
	// eventsTopic defines the events topic
	eventsTopic = "topic.events"

	systemNamePrefix = "GoAkt"
	routeeNamePrefix = "GoAktRoutee"

	routerType nameType = iota
	rebalancerType
	rootGuardianType
	userGuardianType
	systemGuardianType
	deathWatchType
	deadletterType
	singletonManagerType
	topicActorType
)

var (
	// 1<<63-1 nanoseconds equals about 292 years, which isn't truly "forever" but is the longest valid time.Duration value in Golang.
	longLived time.Duration = 1<<63 - 1
	// NoSender means that there is no sender
	NoSender *PID
	// DefaultSupervisorDirective defines the default supervisory strategy directive
	DefaultSupervisorDirective = StopDirective
	timers                     = timer.NewPool()

	systemNames = map[nameType]string{
		routerType:           "GoAktRouter",
		rebalancerType:       "GoAktRebalancer",
		rootGuardianType:     "GoAktRootGuardian",
		userGuardianType:     "GoAktUserGuardian",
		systemGuardianType:   "GoAktSystemGuardian",
		deathWatchType:       "GoAktDeathWatch",
		deadletterType:       "GoAktDeadletter",
		singletonManagerType: "GoAktSingletonManager",
		topicActorType:       "GoAktTopicActor",
	}
)

// encodeDependencies transforms a list of dependencies into their serialized protobuf representations.
// Returns a slice of internalpb.Dependency or an error if serialization fails.
func encodeDependencies(dependencies ...extension.Dependency) ([]*internalpb.Dependency, error) {
	var output []*internalpb.Dependency
	for _, dependency := range dependencies {
		bytea, err := dependency.MarshalBinary()
		if err != nil {
			return nil, err
		}

		output = append(output, &internalpb.Dependency{
			Id:       dependency.ID(),
			TypeName: types.Name(dependency),
			Bytea:    bytea,
		})
	}
	return output, nil
}

func passivationStrategyToProto(strategy passivation.Strategy) *internalpb.PassivationStrategy {
	switch s := strategy.(type) {
	case *passivation.TimeBasedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_TimeBased{
				TimeBased: &internalpb.TimeBasedPassivation{
					PassivateAfter: durationpb.New(s.Timeout()),
				},
			},
		}
	case *passivation.MessagesCountBasedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_MessagesCountBased{
				MessagesCountBased: &internalpb.MessagesCountBasedPassivation{
					MaxMessages: int64(s.MaxMessages()),
				},
			},
		}
	case *passivation.LongLivedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_LongLived{
				LongLived: new(internalpb.LongLivedPassivation),
			},
		}
	default:
		return nil
	}
}

func passivationStrategyFromProto(proto *internalpb.PassivationStrategy) passivation.Strategy {
	if proto == nil {
		return nil
	}

	switch s := proto.Strategy.(type) {
	case *internalpb.PassivationStrategy_TimeBased:
		return passivation.NewTimeBasedStrategy(s.TimeBased.GetPassivateAfter().AsDuration())
	case *internalpb.PassivationStrategy_MessagesCountBased:
		return passivation.NewMessageCountBasedStrategy(int(s.MessagesCountBased.GetMaxMessages()))
	case *internalpb.PassivationStrategy_LongLived:
		return passivation.NewLongLivedStrategy()
	default:
		return nil
	}
}
