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
	"strings"
)

type nameType int

const (
	rebalancerType nameType = iota
	rootGuardianType
	userGuardianType
	systemGuardianType
	deathWatchType
	deadletterType
	singletonManagerType
	topicActorType
	noSenderType
	peersStatesWriterType
)

const (
	// eventsTopic defines the events topic
	eventsTopic = "topic.events"

	reservedNamesPrefix = "GoAkt"
	routeeNamePrefix    = "Routee"
)

var (
	reservedNames = map[nameType]string{
		rebalancerType:        "GoAktRebalancer",
		rootGuardianType:      "GoAktRootGuardian",
		userGuardianType:      "GoAktUserGuardian",
		systemGuardianType:    "GoAktSystemGuardian",
		deathWatchType:        "GoAktDeathWatch",
		deadletterType:        "GoAktDeadletter",
		singletonManagerType:  "GoAktSingletonManager",
		topicActorType:        "GoAktTopicActor",
		noSenderType:          "GoAktNoSender",
		peersStatesWriterType: "GoAktPeerStatesWriter",
	}
)

func isSystemName(name string) bool {
	return strings.HasPrefix(name, reservedNamesPrefix)
}
