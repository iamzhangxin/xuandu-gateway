// Package testutil contains deterministic control-plane test doubles.
package testutil

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

type Store struct {
	mu           sync.Mutex
	Apps         map[string]*metadata.App
	Contracts    map[string]*archiveidl.Revision
	ContractFail bool
	Fail         bool
	writes       uint64
}

func NewStore() *Store { return &Store{Apps: map[string]*metadata.App{}} }
func (s *Store) Get(_ context.Context, n string) (*metadata.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.Apps[n]
	if a == nil {
		return nil, metadata.ErrNotFound
	}
	out := *a
	return &out, nil
}
func (s *Store) List(_ context.Context) ([]*metadata.App, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*metadata.App
	for _, a := range s.Apps {
		v := *a
		out = append(out, &v)
	}
	return out, nil
}
func (s *Store) Create(ctx context.Context, a *metadata.App) error {
	return s.CompareAndSwap(ctx, a, 0)
}
func (s *Store) CompareAndSwap(_ context.Context, a *metadata.App, i uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.Apps[a.Name]
	if s.Fail || (old != nil && old.ModifyIndex != i) || (old == nil && i != 0) {
		return metadata.ErrConflict
	}
	s.writes++
	out := *a
	out.ModifyIndex = s.writes
	s.Apps[a.Name] = &out
	return nil
}
func (s *Store) Delete(_ context.Context, n string, i uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.Apps[n]
	if a == nil {
		return metadata.ErrNotFound
	}
	if a.ModifyIndex != i {
		return metadata.ErrConflict
	}
	delete(s.Apps, n)
	return nil
}
func (s *Store) Watch(ctx context.Context, h func([]*metadata.App)) error {
	<-ctx.Done()
	return ctx.Err()
}

type Fetcher struct {
	Revision *archiveidl.Revision
	Err      error
	Refs     []string
}

func (f *Fetcher) Resolve(_ context.Context, s archiveidl.Source) (*archiveidl.Revision, error) {
	f.Refs = append(f.Refs, s.ExpectedDigest)
	if f.Err != nil {
		return nil, f.Err
	}
	r := *f.Revision
	return &r, nil
}

type Builder struct {
	Fail  bool
	Calls int
}

func (b *Builder) Build(_ context.Context, a *metadata.App, r *archiveidl.Revision, _ []*idl.Bundle, routes []idl.Route) (*rt.ServiceRuntime, error) {
	b.Calls++
	if b.Fail {
		return nil, errors.New("build failed")
	}
	return &rt.ServiceRuntime{AppName: a.Name, Revision: r.Digest, Routes: routes, Config: *a, Timeout: time.Second}, nil
}

func (s *Store) PutContract(_ context.Context, r *archiveidl.Revision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ContractFail {
		return errors.New("contract persistence failed")
	}
	if s.Contracts == nil {
		s.Contracts = map[string]*archiveidl.Revision{}
	}
	s.Contracts[r.Digest] = cloneRevision(r)
	return nil
}
func (s *Store) GetContract(_ context.Context, digest string) (*archiveidl.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Contracts[digest] == nil {
		return nil, errors.New("persisted contract missing; import a new ZIP link")
	}
	return cloneRevision(s.Contracts[digest]), nil
}
func cloneRevision(r *archiveidl.Revision) *archiveidl.Revision {
	out := *r
	out.Files = map[string]string{}
	for k, v := range r.Files {
		out.Files[k] = v
	}
	out.Archive = append([]byte(nil), r.Archive...)
	return &out
}
