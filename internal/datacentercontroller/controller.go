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

package datacentercontroller

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/log"
)

type (
	Config                = datacenter.Config
	ControlPlane          = datacenter.ControlPlane
	ControlPlaneEvent     = datacenter.ControlPlaneEvent
	ControlPlaneEventType = datacenter.ControlPlaneEventType
	DataCenter            = datacenter.DataCenter
	DataCenterRecord      = datacenter.DataCenterRecord
	DataCenterState       = datacenter.DataCenterState
)

// Controller coordinates multi–data center (multi‑DC) control‑plane interactions and
// maintains a best‑effort in‑memory cache of ACTIVE data centers for routing/selection.
//
// Responsibilities:
//   - Registers the local data center record on Start and transitions it to ACTIVE.
//   - Periodically renews the local lease via heartbeats.
//   - Periodically refreshes the cache via polling (ListActive).
//   - Optionally subscribes to change events (Watch) and incrementally applies them
//     to the cache when supported by the control plane.
//
// Lifecycle & concurrency:
//   - Start/Stop are idempotent and safe to call multiple times.
//   - Background goroutines (heartbeat, refresh, optional watch) are started by Start
//     and are stopped/cancelled by Stop.
//   - ActiveRecords returns a snapshot copy of cached records and a “stale” indicator
//     based on MaxCacheStaleness.
//
// Notes:
//   - When Watch is enabled but not supported by the control plane, the controller
//     logs the condition and continues operating with polling only.
type Controller struct {
	config       *Config
	controlPlane ControlPlane
	cache        *recordCache
	watcher      <-chan ControlPlaneEvent
	logger       log.Logger

	lifecycleMu sync.Mutex
	mu          sync.RWMutex
	recordID    string
	recordVer   uint64
	leaseExpiry time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started        atomic.Bool
	watchSupported atomic.Bool
}

// NewController constructs a Controller from the provided configuration.
//
// It sanitizes and validates the config before creating the controller. The returned
// controller is not running until Start is called.
//
// Requirements:
//   - config must be non‑nil.
//   - config.ControlPlane must be set and config.Validate must succeed.
//
// On success, the controller is initialized with an empty cache and the configured
// logger/control plane. Background work is started only by Start.
//
// Returns an error if the config is nil or invalid.
func NewController(config *Config) (*Controller, error) {
	if config == nil {
		return nil, errors.New("multidc: config is required")
	}

	config.Sanitize()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &Controller{
		config:       config,
		controlPlane: config.ControlPlane,
		cache:        newRecordCache(),
		logger:       config.Logger,
	}, nil
}

// Start registers the local datacenter, starts the heartbeat loop, and begins cache refresh.
//
// Start is idempotent: repeated calls return nil after the manager is already started.
// The caller must provide a non-nil context; it is used as the parent for all background
// operations and cancellation.
func (x *Controller) Start(ctx context.Context) error {
	x.lifecycleMu.Lock()
	defer x.lifecycleMu.Unlock()

	if x.started.Load() {
		return nil
	}

	x.ctx, x.cancel = context.WithCancel(ctx)

	if err := x.register(x.ctx); err != nil {
		if x.cancel != nil {
			x.cancel()
		}
		x.started.Store(false)
		return err
	}

	if err := x.refreshCache(x.ctx); err != nil {
		x.logger.Warnf("multidc manager: initial cache refresh failed: %v", err)
	}

	x.wg.Add(1)
	go x.heartbeatLoop()

	x.wg.Add(1)
	go x.refreshLoop()

	x.watchSupported.Store(false)
	if x.config.WatchEnabled {
		x.wg.Add(1)
		go x.watchLoop()
	}

	x.started.Store(true)
	return nil
}

// Stop stops background tasks and marks the local datacenter inactive.
//
// Stop is idempotent: repeated calls return nil after the manager is already stopped.
func (x *Controller) Stop(ctx context.Context) error {
	x.lifecycleMu.Lock()
	defer x.lifecycleMu.Unlock()

	if !x.started.Load() {
		return nil
	}

	x.started.Store(false)

	if x.cancel != nil {
		x.cancel()
	}

	x.wg.Wait()
	x.watchSupported.Store(false)

	id, version := x.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := x.withTimeout(ctx)
	newVersion, err := x.controlPlane.SetState(opCtx, id, datacenter.DataCenterDraining, version)
	cancel()
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	opCtx, cancel = x.withTimeout(ctx)
	newVersion, err = x.controlPlane.SetState(opCtx, id, datacenter.DataCenterInactive, newVersion)
	cancel()
	if err != nil && !errors.Is(err, gerrors.ErrRecordNotFound) {
		return err
	}

	x.mu.Lock()
	x.recordVer = newVersion
	x.mu.Unlock()

	x.cache.reset()
	return nil
}

// ActiveRecords returns the cached active records and whether the cache is stale.
//
// The stale flag is true if the cache has never been refreshed or if the last refresh
// time exceeds MaxCacheStaleness.
func (x *Controller) ActiveRecords() ([]DataCenterRecord, bool) {
	records, refreshedAt := x.cache.snapshot()
	if refreshedAt.IsZero() {
		return records, true
	}
	return records, time.Since(refreshedAt) > x.config.MaxCacheStaleness
}

// heartbeatLoop periodically renews the local record lease while the manager is running.
func (x *Controller) heartbeatLoop() {
	defer x.wg.Done()
	x.runLoop("Heartbeat", x.config.HeartbeatInterval, x.heartbeatOnce)
}

// refreshLoop periodically refreshes the local cache of active records.
func (x *Controller) refreshLoop() {
	defer x.wg.Done()
	x.runLoop("Cache Refresh", x.config.CacheRefreshInterval, x.refreshCache)
}

// watchLoop applies control plane change events to the local cache and
// re-establishes watches with backoff on failures.
func (x *Controller) watchLoop() {
	defer x.wg.Done()

	failures := 0
watchLoop:
	for {
		select {
		case <-x.ctx.Done():
			return
		default:
		}

		events, err := x.controlPlane.Watch(x.ctx)
		if err != nil {
			if errors.Is(err, gerrors.ErrWatchNotSupported) {
				x.logger.Infof("Control plane watch not supported, polling enabled")
				return
			}

			failures++
			x.logger.Warnf("Watch setup failed: %v", err)
			if !x.sleepBackoff(x.config.CacheRefreshInterval, failures) {
				return
			}
			continue
		}

		x.watchSupported.Store(true)
		x.watcher = events
		failures = 0

		for {
			select {
			case <-x.ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					if x.ctx.Err() == nil {
						x.logger.Warn("Control plane watch closed")
					}

					x.watchSupported.Store(false)
					failures++
					if !x.sleepBackoff(x.config.CacheRefreshInterval, failures) {
						return
					}
					continue watchLoop
				}

				if event.Record.ID == "" {
					continue
				}

				x.cache.apply(event)
			}
		}
	}
}

// heartbeatOnce executes a single heartbeat attempt and updates local lease metadata.
func (x *Controller) heartbeatOnce(ctx context.Context) error {
	id, version := x.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := x.withTimeout(ctx)
	defer cancel()

	newVersion, leaseExpiry, err := x.controlPlane.Heartbeat(opCtx, id, version)
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) || errors.Is(err, gerrors.ErrRecordConflict) {
			return x.register(ctx)
		}
		return err
	}

	x.mu.Lock()
	x.recordVer = newVersion
	x.leaseExpiry = leaseExpiry
	x.mu.Unlock()
	return nil
}

// refreshCache reloads the list of active records into the local cache.
func (x *Controller) refreshCache(ctx context.Context) error {
	opCtx, cancel := x.withTimeout(ctx)
	defer cancel()

	records, err := x.controlPlane.ListActive(opCtx)
	if err != nil {
		return err
	}

	if x.watchSupported.Load() {
		x.cache.merge(records)
	} else {
		x.cache.replace(records)
	}
	return nil
}

// register registers the local record and transitions it to ACTIVE.
func (x *Controller) register(ctx context.Context) error {
	record := x.record()

	id, version, err := x.registerRecord(ctx, record)
	if err != nil {
		return err
	}

	version, err = x.setRecordState(ctx, id, datacenter.DataCenterActive, version)
	if err != nil {
		return err
	}

	x.setRecordRef(id, version)
	return nil
}

func (x *Controller) registerRecord(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
	opCtx, cancel := x.withTimeout(ctx)
	defer cancel()
	return x.controlPlane.Register(opCtx, record)
}

func (x *Controller) setRecordState(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
	opCtx, cancel := x.withTimeout(ctx)
	defer cancel()
	return x.controlPlane.SetState(opCtx, id, state, version)
}

func (x *Controller) setRecordRef(id string, version uint64) {
	x.mu.Lock()
	x.recordID = id
	x.recordVer = version
	x.mu.Unlock()
}

// record builds the current local DataCenterRecord for registration.
func (x *Controller) record() DataCenterRecord {
	x.mu.RLock()
	recordID := x.recordID
	x.mu.RUnlock()

	if recordID == "" {
		recordID = x.config.DataCenter.ID()
		if recordID == "" {
			recordID = x.config.DataCenter.Name
		}
	}

	return DataCenterRecord{
		ID:         recordID,
		DataCenter: x.config.DataCenter,
		Endpoints:  x.config.Endpoints,
		State:      datacenter.DataCenterRegistered,
	}
}

// recordRef returns the current record ID and version under lock.
func (x *Controller) recordRef() (string, uint64) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.recordID, x.recordVer
}

// withTimeout scopes control plane calls to the configured RequestTimeout.
func (x *Controller) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, x.config.RequestTimeout)
}

// runLoop executes fn on a jittered interval with bounded backoff on errors.
func (x *Controller) runLoop(name string, interval time.Duration, fn func(context.Context) error) {
	failures := 0
	timer := time.NewTimer(x.nextDelay(interval, failures))
	defer timer.Stop()

	for {
		select {
		case <-x.ctx.Done():
			return
		case <-timer.C:
			if err := fn(x.ctx); err != nil {
				failures++
				x.logger.Warnf("DataCenter Controller: %s failed: %v", name, err)
			} else {
				failures = 0
			}
			timer.Reset(x.nextDelay(interval, failures))
		}
	}
}

func (x *Controller) nextDelay(interval time.Duration, failures int) time.Duration {
	delay := interval
	if failures > 0 {
		delay = x.backoffDelay(interval, failures)
	}
	return jitterDuration(delay, x.config.JitterRatio)
}

func (x *Controller) backoffDelay(base time.Duration, failures int) time.Duration {
	delay := base
	for i := 1; i < failures && delay < x.config.MaxBackoff; i++ {
		delay *= 2
		if delay > x.config.MaxBackoff {
			delay = x.config.MaxBackoff
			break
		}
	}
	if delay <= 0 {
		return base
	}
	return delay
}

func (x *Controller) sleepBackoff(base time.Duration, failures int) bool {
	delay := x.nextDelay(base, failures)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-x.ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func jitterDuration(base time.Duration, ratio float64) time.Duration {
	if ratio <= 0 {
		return base
	}
	delta := ratio * float64(base)
	jitter := (secureFloat64()*2 - 1) * delta
	delay := base + time.Duration(jitter)
	if delay <= 0 {
		return base
	}
	return delay
}

func secureFloat64() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0.5
	}
	// Use 53 bits to match float64 mantissa precision.
	raw := binary.LittleEndian.Uint64(buf[:]) >> 11
	return float64(raw) / (1 << 53)
}

type recordCache struct {
	mu          sync.RWMutex
	records     map[string]DataCenterRecord
	tombstones  map[string]uint64
	refreshedAt time.Time
}

// newRecordCache creates an empty record cache.
func newRecordCache() *recordCache {
	return &recordCache{
		records:    make(map[string]DataCenterRecord),
		tombstones: make(map[string]uint64),
	}
}

// replace atomically replaces the cache contents and updates the refresh timestamp.
func (c *recordCache) replace(records []DataCenterRecord) {
	c.mu.Lock()
	newRecords := make(map[string]DataCenterRecord, len(records))
	for _, record := range records {
		if record.ID == "" || record.State != datacenter.DataCenterActive {
			continue
		}
		if !c.shouldApplyLocked(record.ID, record.Version) {
			continue
		}
		newRecords[record.ID] = record
		delete(c.tombstones, record.ID)
	}
	c.records = newRecords
	c.refreshedAt = time.Now()
	c.mu.Unlock()
}

// merge applies a non-authoritative refresh without removing existing entries.
func (c *recordCache) merge(records []DataCenterRecord) {
	c.mu.Lock()
	for _, record := range records {
		if record.ID == "" || record.State != datacenter.DataCenterActive {
			continue
		}
		if !c.shouldApplyLocked(record.ID, record.Version) {
			continue
		}
		c.records[record.ID] = record
		delete(c.tombstones, record.ID)
	}
	c.refreshedAt = time.Now()
	c.mu.Unlock()
}

// apply updates the cache using a watch event.
func (c *recordCache) apply(event ControlPlaneEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if event.Record.ID == "" {
		return
	}

	eventType := event.Type
	if eventType == datacenter.ControlPlaneEventUpsert && event.Record.State != datacenter.DataCenterActive {
		eventType = datacenter.ControlPlaneEventDelete
	}

	if !c.shouldApplyLocked(event.Record.ID, event.Record.Version) {
		return
	}

	switch eventType {
	case datacenter.ControlPlaneEventDelete:
		if event.Record.Version > 0 {
			c.tombstones[event.Record.ID] = event.Record.Version
		}
		delete(c.records, event.Record.ID)
	default:
		c.records[event.Record.ID] = event.Record
		delete(c.tombstones, event.Record.ID)
	}
	c.refreshedAt = time.Now()
}

// snapshot returns a copy of cached records and the last refresh timestamp.
func (c *recordCache) snapshot() ([]DataCenterRecord, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	records := make([]DataCenterRecord, 0, len(c.records))
	for _, record := range c.records {
		records = append(records, record)
	}
	return records, c.refreshedAt
}

// reset clears the cache and resets the refresh timestamp.
func (c *recordCache) reset() {
	c.mu.Lock()
	c.records = make(map[string]DataCenterRecord)
	c.tombstones = make(map[string]uint64)
	c.refreshedAt = time.Time{}
	c.mu.Unlock()
}

func (c *recordCache) shouldApplyLocked(id string, version uint64) bool {
	if version == 0 {
		current := c.currentVersionLocked(id)
		return current == 0
	}
	return version >= c.currentVersionLocked(id)
}

func (c *recordCache) currentVersionLocked(id string) uint64 {
	if record, ok := c.records[id]; ok {
		return record.Version
	}
	if version, ok := c.tombstones[id]; ok {
		return version
	}
	return 0
}
