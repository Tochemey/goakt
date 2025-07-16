/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/registry"
	"github.com/tochemey/goakt/v4/passivation"
)

func marshalPassivationStrategy(strategy passivation.Strategy) *internalpb.PassivationStrategy {
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

func unmarshalPassivationStrategy(proto *internalpb.PassivationStrategy) passivation.Strategy {
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

// marshalDependencies transforms a list of dependencies into their serialized protobuf representations.
// Returns a slice of internalpb.Dependency or an error if serialization fails.
func marshalDependencies(dependencies ...extension.Dependency) ([]*internalpb.Dependency, error) {
	var output []*internalpb.Dependency
	for _, dependency := range dependencies {
		bytea, err := dependency.MarshalBinary()
		if err != nil {
			return nil, err
		}

		output = append(output, &internalpb.Dependency{
			Id:       dependency.ID(),
			TypeName: registry.Name(dependency),
			Bytea:    bytea,
		})
	}
	return output, nil
}
