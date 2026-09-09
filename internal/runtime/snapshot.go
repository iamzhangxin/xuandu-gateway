package runtime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/router"
)

// Snapshot and all published maps are immutable. Acquire pins its runtimes under
// the same lock as publication, eliminating the load-then-increment retirement race.
type Snapshot struct {
	Version  uint64
	Routes   map[string]router.Table
	Domains  map[string]map[string]bool
	Runtimes map[string]*ServiceRuntime
}

// HasApp 按域名和应用编码查找已发布的应用。
func (s *Snapshot) HasApp(domain, app string) bool { return s.Domains[domain][app] }

func (s *Snapshot) Match(app, method, path string) (router.Target, bool) {
	table := s.Routes[app]
	if table == nil {
		return router.Target{}, false
	}
	return table.Match(method, path)
}

type Manager struct {
	publishMu sync.Mutex
	current   atomic.Pointer[Snapshot]
	mu        sync.Mutex
	closed    bool
	active    int
	drained   chan struct{}
}
type Lease struct {
	Snapshot *Snapshot
	manager  *Manager
	once     sync.Once
}

func NewManager() *Manager {
	m := &Manager{drained: make(chan struct{})}
	m.current.Store(&Snapshot{Routes: map[string]router.Table{}, Domains: map[string]map[string]bool{}, Runtimes: map[string]*ServiceRuntime{}})
	return m
}
func (m *Manager) Load() *Snapshot { return m.current.Load() }
func (m *Manager) Acquire() *Lease {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	s := m.current.Load()
	for _, r := range s.Runtimes {
		r.refs++
	}
	m.active++
	return &Lease{Snapshot: s, manager: m}
}
func (l *Lease) Release() {
	l.once.Do(func() {
		m := l.manager
		m.mu.Lock()
		var closeList []*ServiceRuntime
		for _, r := range l.Snapshot.Runtimes {
			r.refs--
			if r.refs == 0 && r.retired {
				closeList = append(closeList, r)
			}
		}
		m.mu.Unlock()
		for _, r := range closeList {
			r.Close()
		}
		m.mu.Lock()
		m.active--
		if m.closed && m.active == 0 {
			close(m.drained)
		}
		m.mu.Unlock()
	})
}

// Publish serializes validation, durable CAS and the local atomic swap. persist
// must not call back into Manager. A failed CAS never publishes the candidate.
func (m *Manager) Publish(name string, next *ServiceRuntime, persist func() error) error {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return fmt.Errorf("gateway is shutting down")
	}
	old := m.current.Load()
	m.mu.Unlock()
	rs := make(map[string]*ServiceRuntime, len(old.Runtimes)+1)

	for n, r := range old.Runtimes {
		if n != name {
			rs[n] = r
		}
	}
	if next != nil {
		rs[name] = next
	}
	tables := map[string]router.Table{}
	domains := map[string]map[string]bool{}
	for n, r := range rs {
		domain, err := metadata.NormalizeDomain(r.Config.Domain)
		if err != nil {
			return err
		}
		if domains[domain] == nil {
			domains[domain] = map[string]bool{}
		}
		table, err := router.Build(map[string][]idl.Route{n: r.Routes})
		if err != nil {
			return err
		}
		domains[domain][n], tables[n] = true, table
	}
	if persist != nil {
		if e := persist(); e != nil {
			return e
		}
	}
	m.mu.Lock()
	m.current.Store(&Snapshot{Version: old.Version + 1, Routes: tables, Domains: domains, Runtimes: rs})
	retired := old.Runtimes[name]
	closeNow := false
	if retired != nil && retired != next {
		retired.retired = true
		closeNow = retired.refs == 0
	}
	m.mu.Unlock()
	if closeNow {
		retired.Close()
	}
	return nil
}
func (m *Manager) ReplaceApp(n string, r *ServiceRuntime) error { return m.Publish(n, r, nil) }
func (m *Manager) RemoveApp(n string) error                     { return m.Publish(n, nil, nil) }
func (m *Manager) Close(ctx context.Context) error {
	m.publishMu.Lock()
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		var closeList []*ServiceRuntime
		for _, r := range m.current.Load().Runtimes {
			r.retired = true
			if r.refs == 0 {
				closeList = append(closeList, r)
			}
		}
		if m.active == 0 {
			close(m.drained)
		}
		m.mu.Unlock()
		for _, r := range closeList {
			r.Close()
		}
	} else {
		m.mu.Unlock()
	}
	m.publishMu.Unlock()
	select {
	case <-m.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
