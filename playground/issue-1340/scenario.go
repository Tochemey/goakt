// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"fmt"
	"strings"
	"time"

	goakt "github.com/tochemey/goakt/v4/actor"
)

const (
	// the two optional environment variables the scenarios vary. Both are
	// empty in the default scenario, which is the one the issue was reported
	// against.
	envConvergenceTimeout = "CONVERGENCE_TIMEOUT"
	envNetworkProfile     = "NETWORK_PROFILE"

	// the values NETWORK_PROFILE accepts.
	profileLAN   = "lan"
	profileLocal = "local"
	profileWAN   = "wan"

	// settingDefault is how the report describes a setting left untouched.
	settingDefault = "default"
)

// scenario holds the two cluster settings the demo varies: the bound on the
// wait for the cluster state to converge on a membership change, and the
// network profile that decides how quickly a failure is confirmed. Both are
// optional, and an unset one leaves the framework default in force.
type scenario struct {
	// convergenceTimeout is zero when CONVERGENCE_TIMEOUT is unset.
	convergenceTimeout time.Duration

	// networkProfile is only meaningful when profileSet is true, because the
	// zero value of the type is a valid profile.
	networkProfile goakt.NetworkProfile
	profileSet     bool
}

// newScenario reads the optional cluster settings from the environment and
// fails on a value it cannot make sense of, rather than running a measurement
// that does not match what was asked for.
func newScenario() (*scenario, error) {
	out := new(scenario)

	if value := envOr(envConvergenceTimeout, ""); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", envConvergenceTimeout, value, err)
		}

		if timeout <= 0 {
			return nil, fmt.Errorf("invalid %s %q: the timeout must be positive", envConvergenceTimeout, value)
		}

		out.convergenceTimeout = timeout
	}

	value := strings.ToLower(envOr(envNetworkProfile, ""))
	if value == "" {
		return out, nil
	}

	switch value {
	case profileLAN:
		out.networkProfile = goakt.NetworkProfileLAN
	case profileLocal:
		out.networkProfile = goakt.NetworkProfileLocal
	case profileWAN:
		out.networkProfile = goakt.NetworkProfileWAN
	default:
		return nil, fmt.Errorf("unknown %s %q: use one of %s, %s, %s", envNetworkProfile, value, profileLAN, profileLocal, profileWAN)
	}

	out.profileSet = true
	return out, nil
}

// apply adds the settings this scenario sets to the cluster configuration.
// Nothing is applied for an unset setting, so the default scenario runs on the
// untouched framework defaults.
func (x *scenario) apply(config *goakt.ClusterConfig) {
	if x.convergenceTimeout > 0 {
		config.WithConvergenceTimeout(x.convergenceTimeout)
	}

	if x.profileSet {
		config.WithNetworkProfile(x.networkProfile)
	}
}

// convergenceTimeoutLabel describes the convergence timeout for the report.
func (x *scenario) convergenceTimeoutLabel() string {
	if x.convergenceTimeout <= 0 {
		return settingDefault
	}

	return x.convergenceTimeout.String()
}

// networkProfileLabel describes the network profile for the report.
func (x *scenario) networkProfileLabel() string {
	if !x.profileSet {
		return settingDefault
	}

	switch x.networkProfile {
	case goakt.NetworkProfileLocal:
		return profileLocal
	case goakt.NetworkProfileWAN:
		return profileWAN
	default:
		return profileLAN
	}
}
