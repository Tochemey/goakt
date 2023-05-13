package embed

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	prefix          = "goaktetcd"
	electionKey     = prefix + "/election"
	volunteerPrefix = prefix + "/volunteers/"
	nomineePrefix   = prefix + "/nominees/"
	idealSizeKey    = prefix + "/idealSize"
)

// startCampaign starts the leadership election campaign
func (es *Embed) startCampaign() {
	go func() {
		// create an instance of election
		election := concurrency.NewElection(es.session, electionKey)
		for {
			// Campaign for the becoming the leader
			// Stop campaigning if the context is canceled
			// else ignore errors and retry campaign
			es.logger.Debug("campaigning to become leader")
			err := election.Campaign(es.client.Ctx(), es.embedConfig.Name)
			if err != nil {
				switch {
				case err == context.Canceled:
					return
				default:
					continue
				}
			}
			es.logger.Debug("won election to become leader")
			// resign as leader if you cannot become the leader
			if err := es.startLeader(); err != nil {
				// log the error
				es.logger.Error(errors.Wrap(err, "failed to start leadership role, resigning"))
				// resign from leadership role
				if err := election.Resign(es.client.Ctx()); err != nil {
					// log the error
					es.logger.Error(errors.Wrap(err, "failed to resign from leadership role"))
					return
				}
				continue
			}
			// You are the leader till. So stop campaigning
			// TODO: Do we need to check for leadership periodically, or are we the leader till we die?
			// TODO: What happens during cluster splits? Etcd should handle that, but need to verify.
			return
		}
	}()
}

// startLeader starts the leader
func (es *Embed) startLeader() error {
	es.watchVolunteers()
	es.watchIdealSize()
	return nil
}

// watchVolunteers watches for volunteers for nomination
func (es *Embed) watchVolunteers() {
	es.logger.Debug("watching for changes to volunteers list")
	f := func(_ clientv3.WatchResponse) {
		es.logger.Debug("volunteer list had a change, doing nominations again")
		es.startNominations()
	}
	es.watch(volunteerPrefix, f, clientv3.WithPrefix())
}

// watchIdealSize watches cluster ideal size
func (es *Embed) watchIdealSize() {
	es.logger.Debug("watching for changes to ideal cluster size")

	f := func(resp clientv3.WatchResponse) {
		for _, ev := range resp.Events {
			size, err := strconv.Atoi(string(ev.Kv.Value))
			if err != nil {
				es.logger.Error(errors.Wrap(err, "failed to parse ideal cluster size, ignoring update"))
				continue
			}

			if size != es.config.Size() {
				es.logger.Debugf("cluster ideal size has changed to=%d, doing nominations again", size)
				es.config.size = size
				es.startNominations()
			}
		}
	}
	es.watch(idealSizeKey, f)
}

// startNominations starts the nomination process
func (es *Embed) startNominations() {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	// We shouldn't ever hit this, but better be safe
	if es.isStopped {
		return
	}

	// add some logging
	es.logger.Info("performing nomination...")

	// grab the client context
	ctx := es.client.Ctx()

	// let us prepare lists of the current nominees and volunteers.
	nomineesResp, err := es.client.Get(ctx, nomineePrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to get the list of nominees"))
		return
	}

	// let us grab the volunteers
	volunteersResp, err := es.client.Get(ctx, volunteerPrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to get the list of volunteers"))
		return
	}

	// extract the nominees and the volunteers
	nominees := keysFromGetResp(nomineesResp, nomineePrefix)
	volunteers := keysFromGetResp(volunteersResp, volunteerPrefix)

	// add some debug logging
	es.logger.Debugf("existing nominees=[%s]", strings.Join(nominees, ","))
	es.logger.Debugf("existing volunteers=[%s]", strings.Join(volunteers, ","))

	// check if anyone rejects the nomination and remove them from the volunteer list
	rejectionList := diffStringSlices(nominees, volunteers)

	// add some debug logging
	es.logger.Debugf("existing non-volunteers=[%s]", strings.Join(rejectionList, ","))

	// remove the non-volunteer from the nomination
	for _, h := range rejectionList {
		if err := es.removeFromNomination(h); err != nil {
			// log failure and continue
			es.logger.Error(err)
		}
	}

	// Update the nominee list after the nominee removals
	nominees = diffStringSlices(nominees, rejectionList)
	nomineeCount := len(nominees)
	es.logger.Debugf("updated nominees=[%s]", strings.Join(nominees, ","))

	// let us prepare a map of volunteers names and their published CURLs
	volunteersMap, err := urlsMapFromGetResp(volunteersResp, volunteerPrefix)
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to prepare volunteers map"))
		return
	}

	switch {
	// If idealSize is not met, nominate more servers till the size is met
	case nomineeCount < es.config.Size():
		// You cannot do nominations if all volunteers have been nominated
		if compareStringSlices(nominees, volunteers) {
			es.logger.Debug("all available volunteers have been nominated")
			return
		}

		// Filter out already nominated servers
		available := diffStringSlices(volunteers, nominees)

		// Keep nominating in a round-robin fashion till the required nominations are done
		for _, host := range available {
			err := es.nominate(host, volunteersMap[host])
			if err != nil {
				es.logger.Error(err)
				continue
			}
			// add some debug logging
			es.logger.Debugf("nominated new host=%s", host)

			nomineeCount++
			if nomineeCount == es.config.Size() {
				break
			}
		}

		// If idealSize is exceeded, remove server nominations till idealSize is reached
	case nomineeCount > es.config.Size():
		// Remove nominations in a round-robin fashion till the required nominations are removed
		for _, host := range nominees {
			if host == es.config.Name() {
				// skip yourself
				continue
			}
			if err := es.removeFromNomination(host); err != nil {
				es.logger.Error(err)
				continue
			}
			nomineeCount--
			if nomineeCount == es.config.Size() {
				break
			}
		}
	}
	es.logger.Info("nomination completed...")
}

// nominate nominates a host
// The following lines explain the process to nominate:
//
// Add the new host to the nominees list.
// Wait a little while for the nominee to pick up the nomination.
// Then add the new host as an etcd member.
// If we add the new host as a member first, the embedded etcd will begin
// trying to connect to the newly added member and which causes new requests
// to be blocked, for example the PUT request to add the new nominee.
// If we don't wait for a little while before adding the nominee as a member,
// the nominee will not be able to read the nominee list it requires to start
// its server.
// This is only required for the first nominee added. When more than one
// server is present the etcd cluster will serve requests when a member is
// added.
func (es *Embed) nominate(host string, urls types.URLs) error {
	// add some logging
	es.logger.Infof("nominating host=%s", host)
	// handle the error
	if err := es.addToNominees(host, urls); err != nil {
		return err
	}

	// wait for the nomination to pick
	time.Sleep(2 * time.Second)

	// get the client context
	ctx := es.client.Ctx()

	// add the host as a cluster member
	_, err := es.client.MemberAdd(ctx, urls.StringSlice())
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to add host as etcd cluster member"))
		// let try removing the host from the nominees list
		return es.removeFromNominees(host)
	}

	return nil
}

// addToNominees add a given host to the nomination list
func (es *Embed) addToNominees(host string, urls types.URLs) error {
	// construct the nominee key
	key := nomineePrefix + host
	// get the client context
	ctx := es.client.Ctx()

	// set the key the given URLs
	_, err := es.client.Put(ctx, key, urls.String())
	// handle the error
	if err != nil {
		es.logger.Error(errors.Wrapf(err, "failed to add host=%s to the nominees list", host))
		return err
	}
	return nil
}

// removeFromNomination removes a given host from nomination
func (es *Embed) removeFromNomination(host string) error {
	// add some logging
	es.logger.Infof("removing host=%s from nomination", host)
	// Remove host from nomination list
	if err := es.removeFromNominees(host); err != nil {
		return err
	}

	// remove host as an etcd member
	ctx := es.client.Ctx()
	memberlist, err := es.client.MemberList(ctx)
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to get memberlist while removing host from nomination"))
		return err
	}

	// let us grab the host member
	var member *etcdserverpb.Member
	for _, member = range memberlist.Members {
		if member.Name == host {
			break
		}
	}

	// remove the host from the cluster
	_, err = es.client.MemberRemove(es.client.Ctx(), member.ID)
	// handle the error
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to remove host as etcd cluster member"))
		return err
	}

	return nil
}

// removeFromNominees removes a host from the nominees list
func (es *Embed) removeFromNominees(host string) error {
	key := nomineePrefix + host
	_, err := es.client.Delete(es.client.Ctx(), key)
	if err != nil {
		es.logger.Error(errors.Wrapf(err, "failed to remove host=%s from nominees list", host))
		return err
	}
	return nil
}

// volunteerSelf adds the self to the volunteer list and starts watching for the nomination
// The initial-cluster must be formed using the Peer URLs.
// The initial-cluster is formed by the urls of the nominees, which are in turn set to the urls set on volunteering.
func (es *Embed) volunteerSelf() error {
	// build the volunteer key
	key := volunteerPrefix + es.embedConfig.Name
	// define the variable holding the associated value to the key
	var val string
	// check for the default Peer URLs
	if isDefaultPeerURL(es.config.PeerURLs()) {
		val = defaultPeerURLs.String()
	} else {
		val = es.config.PeerURLs().String()
	}

	// set the volunteer key
	_, err := es.client.Put(es.client.Ctx(), key, val, clientv3.WithLease(es.session.Lease()))
	// handle the error
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to add self to the volunteers list"))
		return err
	}

	// watch for nomination
	es.watchNomination()
	return nil
}

// watchNomination watches for nomination
func (es *Embed) watchNomination() {
	es.logger.Debug("watching for self nomination")
	// build the nomination key
	key := nomineePrefix + es.embedConfig.Name
	f := func(_ clientv3.WatchResponse) {
		es.handleNomination()
	}
	es.watch(key, f)
}

// handleNomination handles the nomination
func (es *Embed) handleNomination() {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	es.logger.Debug("handling nomination")

	// Get the current list of nominees
	nomineesResp, err := es.client.Get(es.client.Ctx(), nomineePrefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	// handle the error
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to get nominees"))
		return
	}

	// prepare the nominees map
	nominees, err := urlsMapFromGetResp(nomineesResp, nomineePrefix)
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to prepare nominees map"))
		return
	}

	// check if you are in the nominees, and start/stop you embedded server as
	// required
	if _, ok := nominees[es.embedConfig.Name]; ok {
		es.logger.Debug("nominated, starting server")
		// Sleeping to allow leader to add me as a etcd cluster member
		// TODO: figure a better to wait
		time.Sleep(time.Second)
		// start the server
		if err := es.startServer(nominees.String()); err != nil {
			es.logger.Error(errors.Wrap(err, "failed to start server after being nominated"))
			return
		}
	}

	es.logger.Debug("not nominated or nomination removed, stopping server")
	// let us stop the server
	if err := es.stopServer(); err != nil {
		es.logger.Error(errors.Wrap(err, "failed to stop server"))
	}
}
