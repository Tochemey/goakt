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
func (ctrl *Controller) Start(ctx context.Context) error {
	ctrl.lifecycleMu.Lock()
	defer ctrl.lifecycleMu.Unlock()

	if ctrl.started.Load() {
		return nil
	}

	ctrl.ctx, ctrl.cancel = context.WithCancel(ctx)

	if err := ctrl.register(ctrl.ctx); err != nil {
		if ctrl.cancel != nil {
			ctrl.cancel()
		}
		ctrl.started.Store(false)
		return err
	}

	if err := ctrl.refreshCache(ctrl.ctx); err != nil {
		ctrl.logger.Warnf("multidc manager: initial cache refresh failed: %v", err)
	}

	ctrl.wg.Add(1)
	go ctrl.heartbeatLoop()

	ctrl.wg.Add(1)
	go ctrl.refreshLoop()

	ctrl.watchSupported.Store(false)
	if ctrl.config.WatchEnabled {
		ctrl.wg.Add(1)
		go ctrl.watchLoop()
	}

	ctrl.started.Store(true)
	return nil
}

// Stop stops background tasks and marks the local datacenter inactive.
//
// Stop is idempotent: repeated calls return nil after the manager is already stopped.
func (ctrl *Controller) Stop(ctx context.Context) error {
	ctrl.lifecycleMu.Lock()
	defer ctrl.lifecycleMu.Unlock()

	if !ctrl.started.Load() {
		return nil
	}

	ctrl.started.Store(false)

	if ctrl.cancel != nil {
		ctrl.cancel()
	}

	ctrl.wg.Wait()
	ctrl.watchSupported.Store(false)

	id, version := ctrl.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := ctrl.withTimeout(ctx)
	newVersion, err := ctrl.controlPlane.SetState(opCtx, id, datacenter.DataCenterDraining, version)
	cancel()
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	opCtx, cancel = ctrl.withTimeout(ctx)
	newVersion, err = ctrl.controlPlane.SetState(opCtx, id, datacenter.DataCenterInactive, newVersion)
	cancel()
	if err != nil && !errors.Is(err, gerrors.ErrRecordNotFound) {
		return err
	}

	ctrl.mu.Lock()
	ctrl.recordVer = newVersion
	ctrl.mu.Unlock()

	ctrl.cache.reset()
	return nil
}

// ActiveRecords returns the cached active records and whether the cache is stale.
//
// The stale flag is true if the cache has never been refreshed or if the last refresh
// time exceeds MaxCacheStaleness.
func (ctrl *Controller) ActiveRecords() ([]DataCenterRecord, bool) {
	records, refreshedAt := ctrl.cache.snapshot()
	if refreshedAt.IsZero() {
		return records, true
	}
	return records, time.Since(refreshedAt) > ctrl.config.MaxCacheStaleness
}

// heartbeatLoop periodically renews the local record lease while the manager is running.
func (ctrl *Controller) heartbeatLoop() {
	defer ctrl.wg.Done()
	ctrl.runLoop("heartbeat", ctrl.config.HeartbeatInterval, ctrl.heartbeatOnce)
}

// refreshLoop periodically refreshes the local cache of active records.
func (ctrl *Controller) refreshLoop() {
	defer ctrl.wg.Done()
	ctrl.runLoop("cache refresh", ctrl.config.CacheRefreshInterval, ctrl.refreshCache)
}

// watchLoop applies control plane change events to the local cache and
// re-establishes watches with backoff on failures.
func (ctrl *Controller) watchLoop() {
	defer ctrl.wg.Done()

	failures := 0
watchLoop:
	for {
		select {
		case <-ctrl.ctx.Done():
			return
		default:
		}

		events, err := ctrl.controlPlane.Watch(ctrl.ctx)
		if err != nil {
			if errors.Is(err, gerrors.ErrWatchNotSupported) {
				ctrl.logger.Infof("multidc manager: control plane watch not supported, polling enabled")
				return
			}
			failures++
			ctrl.logger.Warnf("multidc manager: watch setup failed: %v", err)
			if !ctrl.sleepBackoff(ctrl.config.CacheRefreshInterval, failures) {
				return
			}
			continue
		}

		ctrl.watchSupported.Store(true)
		ctrl.watcher = events
		failures = 0

		for {
			select {
			case <-ctrl.ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					if ctrl.ctx.Err() == nil {
						ctrl.logger.Warn("multidc manager: control plane watch closed")
					}
					ctrl.watchSupported.Store(false)
					failures++
					if !ctrl.sleepBackoff(ctrl.config.CacheRefreshInterval, failures) {
						return
					}
					continue watchLoop
				}
				if event.Record.ID == "" {
					continue
				}
				ctrl.cache.apply(event)
			}
		}
	}
}

// heartbeatOnce executes a single heartbeat attempt and updates local lease metadata.
func (ctrl *Controller) heartbeatOnce(ctx context.Context) error {
	id, version := ctrl.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := ctrl.withTimeout(ctx)
	defer cancel()

	newVersion, leaseExpiry, err := ctrl.controlPlane.Heartbeat(opCtx, id, version)
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) || errors.Is(err, gerrors.ErrRecordConflict) {
			return ctrl.register(ctx)
		}
		return err
	}

	ctrl.mu.Lock()
	ctrl.recordVer = newVersion
	ctrl.leaseExpiry = leaseExpiry
	ctrl.mu.Unlock()
	return nil
}

// refreshCache reloads the list of active records into the local cache.
func (ctrl *Controller) refreshCache(ctx context.Context) error {
	opCtx, cancel := ctrl.withTimeout(ctx)
	defer cancel()

	records, err := ctrl.controlPlane.ListActive(opCtx)
	if err != nil {
		return err
	}

	if ctrl.watchSupported.Load() {
		ctrl.cache.merge(records)
	} else {
		ctrl.cache.replace(records)
	}
	return nil
}

// register registers the local record and transitions it to ACTIVE.
func (ctrl *Controller) register(ctx context.Context) error {
	record := ctrl.record()

	id, version, err := ctrl.registerRecord(ctx, record)
	if err != nil {
		return err
	}

	version, err = ctrl.setRecordState(ctx, id, datacenter.DataCenterActive, version)
	if err != nil {
		return err
	}

	ctrl.setRecordRef(id, version)
	return nil
}

func (ctrl *Controller) registerRecord(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
	opCtx, cancel := ctrl.withTimeout(ctx)
	defer cancel()
	return ctrl.controlPlane.Register(opCtx, record)
}

func (ctrl *Controller) setRecordState(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
	opCtx, cancel := ctrl.withTimeout(ctx)
	defer cancel()
	return ctrl.controlPlane.SetState(opCtx, id, state, version)
}

func (ctrl *Controller) setRecordRef(id string, version uint64) {
	ctrl.mu.Lock()
	ctrl.recordID = id
	ctrl.recordVer = version
	ctrl.mu.Unlock()
}

// record builds the current local DataCenterRecord for registration.
func (ctrl *Controller) record() DataCenterRecord {
	ctrl.mu.RLock()
	recordID := ctrl.recordID
	ctrl.mu.RUnlock()

	if recordID == "" {
		recordID = ctrl.config.DataCenter.ID()
		if recordID == "" {
			recordID = ctrl.config.DataCenter.Name
		}
	}

	return DataCenterRecord{
		ID:         recordID,
		DataCenter: ctrl.config.DataCenter,
		Endpoints:  ctrl.config.Endpoints,
		State:      datacenter.DataCenterRegistered,
	}
}

// recordRef returns the current record ID and version under lock.
func (ctrl *Controller) recordRef() (string, uint64) {
	ctrl.mu.RLock()
	defer ctrl.mu.RUnlock()
	return ctrl.recordID, ctrl.recordVer
}

// withTimeout scopes control plane calls to the configured RequestTimeout.
func (ctrl *Controller) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, ctrl.config.RequestTimeout)
}

// runLoop executes fn on a jittered interval with bounded backoff on errors.
func (ctrl *Controller) runLoop(name string, interval time.Duration, fn func(context.Context) error) {
	failures := 0
	timer := time.NewTimer(ctrl.nextDelay(interval, failures))
	defer timer.Stop()

	for {
		select {
		case <-ctrl.ctx.Done():
			return
		case <-timer.C:
			if err := fn(ctrl.ctx); err != nil {
				failures++
				ctrl.logger.Warnf("multidc manager: %s failed: %v", name, err)
			} else {
				failures = 0
			}
			timer.Reset(ctrl.nextDelay(interval, failures))
		}
	}
}

func (ctrl *Controller) nextDelay(interval time.Duration, failures int) time.Duration {
	delay := interval
	if failures > 0 {
		delay = ctrl.backoffDelay(interval, failures)
	}
	return jitterDuration(delay, ctrl.config.JitterRatio)
}

func (ctrl *Controller) backoffDelay(base time.Duration, failures int) time.Duration {
	delay := base
	for i := 1; i < failures && delay < ctrl.config.MaxBackoff; i++ {
		delay *= 2
		if delay > ctrl.config.MaxBackoff {
			delay = ctrl.config.MaxBackoff
			break
		}
	}
	if delay <= 0 {
		return base
	}
	return delay
}

func (ctrl *Controller) sleepBackoff(base time.Duration, failures int) bool {
	delay := ctrl.nextDelay(base, failures)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctrl.ctx.Done():
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
