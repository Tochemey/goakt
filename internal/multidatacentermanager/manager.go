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

package multidatacentermanager

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/multidatacenter"
)

type (
	Config                = multidatacenter.Config
	ControlPlane          = multidatacenter.ControlPlane
	ControlPlaneEvent     = multidatacenter.ControlPlaneEvent
	ControlPlaneEventType = multidatacenter.ControlPlaneEventType
	DataCenter            = multidatacenter.DataCenter
	DataCenterRecord      = multidatacenter.DataCenterRecord
	DataCenterState       = multidatacenter.DataCenterState
)

// Manager coordinates multi-DC control plane interactions and maintains a local cache.
//
// It owns the local DC registration lifecycle, periodic heartbeats, optional watch
// subscription, and a best-effort cache of active data centers for routing.
type Manager struct {
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

// NewManager creates a new multi-DC manager from the provided configuration.
//
// The config is sanitized and validated before the manager is created.
func NewManager(config *Config) (*Manager, error) {
	if config == nil {
		return nil, errors.New("multidc: config is required")
	}

	config.Sanitize()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return &Manager{
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
func (m *Manager) Start(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	if m.started.Load() {
		return nil
	}

	m.ctx, m.cancel = context.WithCancel(ctx)

	if err := m.register(m.ctx); err != nil {
		if m.cancel != nil {
			m.cancel()
		}
		m.started.Store(false)
		return err
	}

	if err := m.refreshCache(m.ctx); err != nil {
		m.logger.Warnf("multidc manager: initial cache refresh failed: %v", err)
	}

	m.wg.Add(1)
	go m.heartbeatLoop()

	m.wg.Add(1)
	go m.refreshLoop()

	m.watchSupported.Store(false)
	if m.config.WatchEnabled {
		m.wg.Add(1)
		go m.watchLoop()
	}

	m.started.Store(true)
	return nil
}

// Stop stops background tasks and marks the local datacenter inactive.
//
// Stop is idempotent: repeated calls return nil after the manager is already stopped.
func (m *Manager) Stop(ctx context.Context) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	if !m.started.Load() {
		return nil
	}

	m.started.Store(false)

	if m.cancel != nil {
		m.cancel()
	}

	m.wg.Wait()
	m.watchSupported.Store(false)

	id, version := m.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := m.withTimeout(ctx)
	newVersion, err := m.controlPlane.SetState(opCtx, id, multidatacenter.DataCenterDraining, version)
	cancel()
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	opCtx, cancel = m.withTimeout(ctx)
	newVersion, err = m.controlPlane.SetState(opCtx, id, multidatacenter.DataCenterInactive, newVersion)
	cancel()
	if err != nil && !errors.Is(err, gerrors.ErrRecordNotFound) {
		return err
	}

	m.mu.Lock()
	m.recordVer = newVersion
	m.mu.Unlock()

	m.cache.reset()
	return nil
}

// ActiveRecords returns the cached active records and whether the cache is stale.
//
// The stale flag is true if the cache has never been refreshed or if the last refresh
// time exceeds MaxCacheStaleness.
func (m *Manager) ActiveRecords() ([]DataCenterRecord, bool) {
	records, refreshedAt := m.cache.snapshot()
	if refreshedAt.IsZero() {
		return records, true
	}
	return records, time.Since(refreshedAt) > m.config.MaxCacheStaleness
}

// heartbeatLoop periodically renews the local record lease while the manager is running.
func (m *Manager) heartbeatLoop() {
	defer m.wg.Done()
	m.runLoop("heartbeat", m.config.HeartbeatInterval, m.heartbeatOnce)
}

// refreshLoop periodically refreshes the local cache of active records.
func (m *Manager) refreshLoop() {
	defer m.wg.Done()
	m.runLoop("cache refresh", m.config.CacheRefreshInterval, m.refreshCache)
}

// watchLoop applies control plane change events to the local cache and
// re-establishes watches with backoff on failures.
func (m *Manager) watchLoop() {
	defer m.wg.Done()

	failures := 0
watchLoop:
	for {
		select {
		case <-m.ctx.Done():
			return
		default:
		}

		events, err := m.controlPlane.Watch(m.ctx)
		if err != nil {
			if errors.Is(err, gerrors.ErrWatchNotSupported) {
				m.logger.Infof("multidc manager: control plane watch not supported, polling enabled")
				return
			}
			failures++
			m.logger.Warnf("multidc manager: watch setup failed: %v", err)
			if !m.sleepBackoff(m.config.CacheRefreshInterval, failures) {
				return
			}
			continue
		}

		m.watchSupported.Store(true)
		m.watcher = events
		failures = 0

		for {
			select {
			case <-m.ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					if m.ctx.Err() == nil {
						m.logger.Warn("multidc manager: control plane watch closed")
					}
					m.watchSupported.Store(false)
					failures++
					if !m.sleepBackoff(m.config.CacheRefreshInterval, failures) {
						return
					}
					continue watchLoop
				}
				if event.Record.ID == "" {
					continue
				}
				m.cache.apply(event)
			}
		}
	}
}

// heartbeatOnce executes a single heartbeat attempt and updates local lease metadata.
func (m *Manager) heartbeatOnce(ctx context.Context) error {
	id, version := m.recordRef()
	if id == "" {
		return nil
	}

	opCtx, cancel := m.withTimeout(ctx)
	defer cancel()

	newVersion, leaseExpiry, err := m.controlPlane.Heartbeat(opCtx, id, version)
	if err != nil {
		if errors.Is(err, gerrors.ErrRecordNotFound) || errors.Is(err, gerrors.ErrRecordConflict) {
			return m.register(ctx)
		}
		return err
	}

	m.mu.Lock()
	m.recordVer = newVersion
	m.leaseExpiry = leaseExpiry
	m.mu.Unlock()
	return nil
}

// refreshCache reloads the list of active records into the local cache.
func (m *Manager) refreshCache(ctx context.Context) error {
	opCtx, cancel := m.withTimeout(ctx)
	defer cancel()

	records, err := m.controlPlane.ListActive(opCtx)
	if err != nil {
		return err
	}

	if m.watchSupported.Load() {
		m.cache.merge(records)
	} else {
		m.cache.replace(records)
	}
	return nil
}

// register registers the local record and transitions it to ACTIVE.
func (m *Manager) register(ctx context.Context) error {
	record := m.record()

	id, version, err := m.registerRecord(ctx, record)
	if err != nil {
		return err
	}

	version, err = m.setRecordState(ctx, id, multidatacenter.DataCenterActive, version)
	if err != nil {
		return err
	}

	m.setRecordRef(id, version)
	return nil
}

func (m *Manager) registerRecord(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
	opCtx, cancel := m.withTimeout(ctx)
	defer cancel()
	return m.controlPlane.Register(opCtx, record)
}

func (m *Manager) setRecordState(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
	opCtx, cancel := m.withTimeout(ctx)
	defer cancel()
	return m.controlPlane.SetState(opCtx, id, state, version)
}

func (m *Manager) setRecordRef(id string, version uint64) {
	m.mu.Lock()
	m.recordID = id
	m.recordVer = version
	m.mu.Unlock()
}

// record builds the current local DataCenterRecord for registration.
func (m *Manager) record() DataCenterRecord {
	m.mu.RLock()
	recordID := m.recordID
	m.mu.RUnlock()

	if recordID == "" {
		recordID = m.config.DataCenter.ID()
		if recordID == "" {
			recordID = m.config.DataCenter.Name
		}
	}

	return DataCenterRecord{
		ID:         recordID,
		DataCenter: m.config.DataCenter,
		Endpoints:  m.config.Endpoints,
		State:      multidatacenter.DataCenterRegistered,
	}
}

// recordRef returns the current record ID and version under lock.
func (m *Manager) recordRef() (string, uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.recordID, m.recordVer
}

// withTimeout scopes control plane calls to the configured RequestTimeout.
func (m *Manager) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, m.config.RequestTimeout)
}

// runLoop executes fn on a jittered interval with bounded backoff on errors.
func (m *Manager) runLoop(name string, interval time.Duration, fn func(context.Context) error) {
	failures := 0
	timer := time.NewTimer(m.nextDelay(interval, failures))
	defer timer.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-timer.C:
			if err := fn(m.ctx); err != nil {
				failures++
				m.logger.Warnf("multidc manager: %s failed: %v", name, err)
			} else {
				failures = 0
			}
			timer.Reset(m.nextDelay(interval, failures))
		}
	}
}

func (m *Manager) nextDelay(interval time.Duration, failures int) time.Duration {
	delay := interval
	if failures > 0 {
		delay = m.backoffDelay(interval, failures)
	}
	return jitterDuration(delay, m.config.JitterRatio)
}

func (m *Manager) backoffDelay(base time.Duration, failures int) time.Duration {
	delay := base
	for i := 1; i < failures && delay < m.config.MaxBackoff; i++ {
		delay *= 2
		if delay > m.config.MaxBackoff {
			delay = m.config.MaxBackoff
			break
		}
	}
	if delay <= 0 {
		return base
	}
	return delay
}

func (m *Manager) sleepBackoff(base time.Duration, failures int) bool {
	delay := m.nextDelay(base, failures)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-m.ctx.Done():
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
		if record.ID == "" || record.State != multidatacenter.DataCenterActive {
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
		if record.ID == "" || record.State != multidatacenter.DataCenterActive {
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
	if eventType == multidatacenter.ControlPlaneEventUpsert && event.Record.State != multidatacenter.DataCenterActive {
		eventType = multidatacenter.ControlPlaneEventDelete
	}

	if !c.shouldApplyLocked(event.Record.ID, event.Record.Version) {
		return
	}

	switch eventType {
	case multidatacenter.ControlPlaneEventDelete:
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
