package host

import (
	"fmt"
	"net"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
)

// Addresses returns the running node etcd addresses
func Addresses() ([]string, error) {
	// grab all the IP interfaces on the host machine
	addresses, err := net.InterfaceAddrs()
	// handle the error
	if err != nil {
		// panic because we need to set the default URLs
		return nil, errors.Wrap(err, "failed to get the assigned ip addresses of the host")
	}

	var (
		addrs = goset.NewSet[string]()
	)

	// iterate the assigned addresses
	for _, address := range addresses {
		// let us grab the CIDR
		// no need to handle the error because the address is return by golang which
		// automatically a valid address
		ip, _, _ := net.ParseCIDR(address.String())
		// let us ignore loop-back ip address
		if ip.IsLoopback() {
			continue
		}

		// grab the ip string representation
		repr := ip.String()
		// check whether it is an IPv4 or IPv6
		if ip.To4() == nil {
			// Enclose IPv6 addresses with '[]' or the formed URLs will fail parsing
			repr = fmt.Sprintf("[%s]", ip.String())
		}
		// set the various URLs
		addrs.Add(repr)
	}

	return addrs.ToSlice(), nil
}
