package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"

	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/openapi"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

type RuntimeBuilder interface {
	Build(context.Context, *metadata.App, *archiveidl.Revision, []*idl.Bundle, []idl.Route) (*rt.ServiceRuntime, error)
}
type Status struct {
	CurrentRevision  string    `json:"currentRevision"`
	TargetRevision   string    `json:"targetRevision"`
	RuntimeStatus    string    `json:"runtimeStatus"`
	RouteCount       int       `json:"routeCount"`
	LastUpdateTime   time.Time `json:"lastUpdateTime"`
	LastUpdateResult string    `json:"lastUpdateResult"`
}
type View struct {
	metadata.App
	Status
}
type Service struct {
	Store    metadata.Store
	Fetcher  archiveidl.Fetcher
	Builder  RuntimeBuilder
	Manager  *rt.Manager
	mu       sync.Mutex
	statusMu sync.RWMutex
	status   map[string]Status
	loaded   bool
	stopping bool
	lifetime context.Context
	cancel   context.CancelFunc
}

func NewService(s metadata.Store, f archiveidl.Fetcher, b RuntimeBuilder, m *rt.Manager) *Service {
	lifetime, cancel := context.WithCancel(context.Background())
	return &Service{lifetime: lifetime, cancel: cancel, Store: s, Fetcher: f, Builder: b, Manager: m, status: map[string]Status{}}
}

type ImportSource struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type CreateAppRequest struct {
	Name        string       `json:"name"`
	ServiceName string       `json:"serviceName"`
	RPCTimeout  string       `json:"rpcTimeout"`
	IDL         ImportSource `json:"idl"`
	Enabled     *bool        `json:"enabled,omitempty"`
}
type UpdateResult struct {
	Changed     bool   `json:"changed"`
	OldRevision string `json:"oldRevision"`
	NewRevision string `json:"newRevision"`
	RouteCount  int    `json:"routeCount"`
}

func (s *Service) build(ctx context.Context, a *metadata.App, digest string) (*rt.ServiceRuntime, error) {
	var r *archiveidl.Revision
	var e error
	if digest == "" {
		r, e = s.Fetcher.Resolve(ctx, archiveidl.Source{URL: a.IDL.URL})
	} else {
		r, e = s.Store.GetContract(ctx, digest)
	}
	if e != nil {
		return nil, e
	}
	a.IDL.ResolvedRevision = r.Digest
	a.IDL.URL = ""
	b, routes, e := idl.LoadArchive(r.Files)
	if e != nil {
		return nil, e
	}
	candidate, e := s.Builder.Build(ctx, a, r, b, routes)
	if e != nil {
		return nil, e
	}
	if digest == "" {
		if e = s.Store.PutContract(ctx, r); e != nil {
			candidate.Close()
			return nil, e
		}
	}
	return candidate, nil
}
func (s *Service) record(a *metadata.App, e error) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	v := Status{TargetRevision: a.IDL.ResolvedRevision, RuntimeStatus: "ready", LastUpdateTime: time.Now().UTC(), LastUpdateResult: "success"}
	if r := s.Manager.Load().Runtimes[a.Name]; r != nil {
		v.CurrentRevision = r.Revision
		v.RouteCount = len(r.Routes)
	}
	if !a.Enabled {
		v.RuntimeStatus = "disabled"
	}
	if e != nil {
		v.RuntimeStatus = "degraded"
		v.LastUpdateResult = "build or publication failed"
	}
	s.status[a.Name] = v
	slog.Info("contract update", "app", a.Name, "current_revision", v.CurrentRevision, "target_revision", v.TargetRevision, "result", v.LastUpdateResult, "route_count", v.RouteCount)
}
func (s *Service) isStopping() bool {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	return s.stopping
}
func (s *Service) CreateApp(ctx context.Context, req CreateAppRequest) (a *metadata.App, err error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isStopping() {
		return nil, fmt.Errorf("gateway shutting down")
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	a = &metadata.App{Name: req.Name, ServiceName: req.ServiceName, RPCTimeout: req.RPCTimeout, IDL: metadata.IDLSource{Type: req.IDL.Type, URL: req.IDL.URL}, Enabled: enabled}
	a.IDL.ResolvedRevision = ""
	if a.IDL.Type == "" {
		a.IDL.Type = "zip"
	}
	if err = a.Validate(); err != nil {
		return nil, err
	}
	epoch, e := s.refresh(ctx, "")
	if e != nil {
		return nil, e
	}
	a.CatalogIndex = epoch
	if _, err = s.Store.Get(ctx, a.Name); err == nil {
		return nil, metadata.ErrConflict
	} else if !errors.Is(err, metadata.ErrNotFound) {
		return nil, err
	}
	candidate, err := s.build(ctx, a, "")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil || !a.Enabled {
			candidate.Close()
		}
		if err == nil {
			s.record(a, nil)
		}
	}()
	next := candidate
	if !a.Enabled {
		next = nil
	}
	err = s.Manager.Publish(a.Name, next, func() error { return s.Store.Create(ctx, a) })
	return a, err
}

type UpdateAppRequest struct {
	ServiceName *string       `json:"serviceName,omitempty"`
	RPCTimeout  *string       `json:"rpcTimeout,omitempty"`
	Enabled     *bool         `json:"enabled,omitempty"`
	IDL         *ImportSource `json:"idl,omitempty"`
}

func (s *Service) UpdateApp(ctx context.Context, name string) (*UpdateResult, error) {
	return s.UpdateAppWithRequest(ctx, name, UpdateAppRequest{})
}
func (s *Service) UpdateAppWithRequest(ctx context.Context, name string, req UpdateAppRequest) (result *UpdateResult, err error) {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isStopping() {
		return nil, fmt.Errorf("gateway shutting down")
	}
	epoch, e := s.refresh(ctx, name)
	if e != nil {
		return nil, e
	}
	a, err := s.Store.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	a.CatalogIndex = epoch
	old := a.IDL.ResolvedRevision
	original := *a
	if req.ServiceName != nil {
		a.ServiceName = *req.ServiceName
	}
	if req.RPCTimeout != nil {
		a.RPCTimeout = *req.RPCTimeout
	}
	if req.Enabled != nil {
		a.Enabled = *req.Enabled
	}
	if req.IDL != nil {
		a.IDL = metadata.IDLSource{Type: req.IDL.Type, URL: req.IDL.URL, ResolvedRevision: old}
		if a.IDL.Type == "" {
			a.IDL.Type = "zip"
		}
		if err = metadata.ValidateDownloadURL(req.IDL.URL); err != nil {
			return nil, err
		}
	}
	if err = a.Validate(); err != nil {
		return nil, err
	}
	a.IDL.URL = ""
	configChanged := !same(original, *a)
	var r *archiveidl.Revision
	if req.IDL != nil {
		r, err = s.Fetcher.Resolve(ctx, archiveidl.Source{URL: req.IDL.URL})
	} else {
		r, err = s.Store.GetContract(ctx, old)
	}
	if err != nil {
		s.record(a, err)
		return nil, err
	}
	if r.Digest == old && !configChanged && (s.Manager.Load().Runtimes[name] != nil || !a.Enabled) {
		s.record(a, nil)
		count := 0
		if current := s.Manager.Load().Runtimes[name]; current != nil {
			count = len(current.Routes)
		}
		return &UpdateResult{OldRevision: old, NewRevision: old, RouteCount: count}, nil
	}
	a.IDL.ResolvedRevision = r.Digest
	a.IDL.URL = ""
	defer func() { s.record(a, err) }()
	b, routes, err := idl.LoadArchive(r.Files)
	if err != nil {
		return nil, err
	}
	candidate, err := s.Builder.Build(ctx, a, r, b, routes)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil || !a.Enabled {
			candidate.Close()
		}
	}()
	if req.IDL != nil {
		if err = s.Store.PutContract(ctx, r); err != nil {
			return nil, err
		}
	}
	next := candidate
	if !a.Enabled {
		next = nil
	}
	err = s.Manager.Publish(name, next, func() error { return s.Store.CompareAndSwap(ctx, a, a.ModifyIndex) })
	if err != nil {
		return nil, err
	}
	return &UpdateResult{true, old, r.Digest, len(routes)}, nil
}
func (s *Service) DeleteApp(ctx context.Context, name string) error {
	ctx, cancel := s.operationContext(ctx)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isStopping() {
		return fmt.Errorf("gateway shutting down")
	}
	a, e := s.Store.Get(ctx, name)
	if e != nil {
		return e
	}
	e = s.Manager.Publish(name, nil, func() error { return s.Store.Delete(ctx, name, a.ModifyIndex) })
	if e == nil {
		s.statusMu.Lock()
		delete(s.status, name)
		s.statusMu.Unlock()
	}
	return e
}
func (s *Service) List(ctx context.Context) ([]View, error) {
	apps, e := s.Store.List(ctx)
	if e != nil {
		return nil, e
	}
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	out := make([]View, 0, len(apps))
	for _, a := range apps {
		out = append(out, View{*a, s.status[a.Name]})
	}
	return out, nil
}

type Detail struct {
	View
	Routes []idl.Route `json:"routes"`
}

func (s *Service) Get(ctx context.Context, n string) (*Detail, error) {
	a, e := s.Store.Get(ctx, n)
	if e != nil {
		return nil, e
	}
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	out := &Detail{View: View{*a, s.status[n]}, Routes: []idl.Route{}}
	if r := s.Manager.Load().Runtimes[n]; r != nil {
		out.Routes = append(out.Routes, r.Routes...)
		out.CurrentRevision = r.Revision
		out.RouteCount = len(r.Routes)
	}
	return out, nil
}
func same(a, b metadata.App) bool {
	a.CatalogIndex = 0
	b.CatalogIndex = 0
	a.ModifyIndex = 0
	b.ModifyIndex = 0
	return reflect.DeepEqual(a, b)
}
func (s *Service) Reconcile(ctx context.Context, apps []*metadata.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(ctx, apps)
}
func (s *Service) reconcileLocked(ctx context.Context, apps []*metadata.App) {
	if s.isStopping() {
		return
	}
	wanted := map[string]bool{}
	for _, target := range apps {
		a := *target
		wanted[a.Name] = true
		current := s.Manager.Load().Runtimes[a.Name]
		if !a.Enabled {
			e := s.Manager.RemoveApp(a.Name)
			s.record(&a, e)
			continue
		}
		if current != nil && same(current.Config, a) {
			continue
		}
		if !metadata.RevisionPattern.MatchString(a.IDL.ResolvedRevision) {
			s.record(&a, fmt.Errorf("exact revision required"))
			continue
		}
		candidate, e := s.build(ctx, &a, a.IDL.ResolvedRevision)
		if e == nil {
			e = s.Manager.ReplaceApp(a.Name, candidate)
			if e != nil {
				candidate.Close()
			}
		}
		s.record(&a, e)
	}
	for name := range s.Manager.Load().Runtimes {
		if !wanted[name] {
			s.Manager.RemoveApp(name)
		}
	}
	s.statusMu.Lock()
	for n := range s.status {
		if !wanted[n] {
			delete(s.status, n)
		}
	}
	s.loaded = true
	s.statusMu.Unlock()
}
func (s *Service) Ready() (bool, map[string]Status) {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()
	ok := s.loaded && !s.stopping
	out := map[string]Status{}
	for n, v := range s.status {
		if v.RuntimeStatus == "degraded" {
			out[n] = v
			if v.CurrentRevision == "" {
				ok = false
			}
		}
	}
	return ok, out
}
func (s *Service) Stop() {
	s.statusMu.Lock()
	s.stopping = true
	s.cancel()
	s.statusMu.Unlock()
}

// Re-list under the same service lock ordering through Reconcile on every watch
// wake; periodic polling also retries unchanged revisions that failed to build.
func (s *Service) Watch(ctx context.Context) error {
	return s.Store.Watch(ctx, func(_ []*metadata.App) {
		s.mu.Lock()
		defer s.mu.Unlock()
		apps, e := s.Store.List(ctx)
		if e == nil {
			s.reconcileLocked(ctx, apps)
		}
	})
}
func (s *Service) refresh(ctx context.Context, replacing string) (uint64, error) {
	store, ok := s.Store.(metadata.CatalogStore)
	if !ok {
		return 0, nil
	}
	apps, epoch, e := store.Catalog(ctx)
	if e != nil {
		return 0, e
	}
	s.reconcileLocked(ctx, apps)
	for _, a := range apps {
		r := s.Manager.Load().Runtimes[a.Name]
		if a.IDL.Type == "zip" && a.Name != replacing && a.Enabled && (r == nil || !same(r.Config, *a)) {
			return 0, fmt.Errorf("local catalog has not converged")
		}
	}
	return epoch, nil
}

func (s *Service) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	stop := context.AfterFunc(s.lifetime, cancel)
	return ctx, func() { stop(); cancel() }
}

// OpenAPI describes the current runtime revision, falling back to the stored
// revision when no runtime is loaded (for example a disabled application).
func (s *Service) OpenAPI(ctx context.Context, name string) (openapi.Object, error) {
	detail, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	revision := detail.CurrentRevision
	if revision == "" {
		revision = detail.IDL.ResolvedRevision
	}
	contract, err := s.Store.GetContract(ctx, revision)
	if err != nil {
		return nil, err
	}
	return openapi.Generate(name, revision, contract.Files)
}
