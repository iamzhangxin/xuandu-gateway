package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	consul "github.com/hashicorp/consul/api"

	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

type ConsulStore struct {
	client *consul.Client
	prefix string
}

func NewConsulConfig(c *config.Config) *consul.Config {
	out := consul.DefaultConfig()
	out.Address = c.Consul.Address
	out.Scheme = c.Consul.Scheme
	out.Token = os.Getenv(c.Consul.TokenEnv)
	if c.Consul.TokenFile != "" {
		out.Token = c.Consul.Token
	}
	out.HttpClient = &http.Client{Transport: out.Transport, Timeout: 35 * time.Second}
	return out
}
func NewConsulStore(c *config.Config, cc *consul.Config) (*ConsulStore, error) {
	cl, e := consul.NewClient(cc)
	if e != nil {
		return nil, e
	}
	return &ConsulStore{cl, strings.Trim(c.Consul.MetadataPrefix, "/") + "/"}, nil
}
func (s *ConsulStore) key(name string) (string, error) {
	if !NamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid app name")
	}
	return s.prefix + name, nil
}
func decode(p *consul.KVPair) (*App, error) {
	var a App
	if e := json.Unmarshal(p.Value, &a); e != nil {
		return nil, fmt.Errorf("invalid metadata JSON")
	}
	// Legacy records remain visible/deletable so the admin can replace their
	// source with a ZIP URL. They are never built by the new data plane.
	if a.IDL.Type == "git" {
		if !NamePattern.MatchString(a.Name) {
			return nil, fmt.Errorf("invalid legacy app name")
		}
		a.ModifyIndex = p.ModifyIndex
		return &a, nil
	}
	if e := a.Validate(); e != nil {
		return nil, e
	}
	if !RevisionPattern.MatchString(a.IDL.ResolvedRevision) {
		return nil, fmt.Errorf("metadata requires ZIP SHA-256")
	}
	a.ModifyIndex = p.ModifyIndex
	return &a, nil
}
func (s *ConsulStore) Get(ctx context.Context, n string) (*App, error) {
	key, e := s.key(n)
	if e != nil {
		return nil, e
	}
	p, _, e := s.client.KV().Get(key, (&consul.QueryOptions{RequireConsistent: true}).WithContext(ctx))
	if e != nil {
		return nil, e
	}
	if p == nil {
		return nil, ErrNotFound
	}
	a, e := decode(p)
	if e == nil && a.Name != n {
		return nil, fmt.Errorf("metadata key mismatch")
	}
	return a, e
}
func (s *ConsulStore) catalog(ctx context.Context, index uint64) ([]*App, uint64, uint64, error) {
	pairs, meta, e := s.client.KV().List(s.prefix, (&consul.QueryOptions{WaitIndex: index, WaitTime: 25 * time.Second, RequireConsistent: true}).WithContext(ctx))
	if e != nil {
		return nil, 0, 0, e
	}
	var epoch uint64
	for _, p := range pairs {
		if p.Key == s.prefix+"_catalog" {
			epoch = p.ModifyIndex
		}
	}
	apps := make([]*App, 0, len(pairs))
	for _, p := range pairs {
		if p.Key == s.prefix+"_catalog" {
			continue
		}
		a, e := decode(p)
		if e != nil {
			return nil, 0, 0, e
		}
		if p.Key != s.prefix+a.Name {
			return nil, 0, 0, fmt.Errorf("metadata key mismatch")
		}
		a.CatalogIndex = epoch
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Name < apps[j].Name })
	return apps, meta.LastIndex, epoch, nil
}
func (s *ConsulStore) list(ctx context.Context, index uint64) ([]*App, uint64, error) {
	a, i, _, e := s.catalog(ctx, index)
	return a, i, e
}
func (s *ConsulStore) Catalog(ctx context.Context) ([]*App, uint64, error) {
	a, _, epoch, e := s.catalog(ctx, 0)
	return a, epoch, e
}
func (s *ConsulStore) List(ctx context.Context) ([]*App, error) {
	a, _, e := s.list(ctx, 0)
	return a, e
}
func (s *ConsulStore) Create(ctx context.Context, a *App) error { return s.CompareAndSwap(ctx, a, 0) }
func (s *ConsulStore) CompareAndSwap(ctx context.Context, a *App, index uint64) error {
	if e := a.Validate(); e != nil {
		return e
	}
	if !RevisionPattern.MatchString(a.IDL.ResolvedRevision) {
		return fmt.Errorf("ZIP SHA-256 required")
	}
	key, e := s.key(a.Name)
	if e != nil {
		return e
	}
	b, e := json.Marshal(a)
	if e != nil {
		return e
	}
	return s.transact(ctx, consul.KVCAS, key, b, index, a.CatalogIndex)
}
func (s *ConsulStore) transact(ctx context.Context, verb consul.KVOp, key string, b []byte, index, epoch uint64) error {
	check := &consul.KVTxnOp{Verb: consul.KVCheckIndex, Key: s.prefix + "_catalog", Index: epoch}
	if epoch == 0 {
		check.Verb = consul.KVCheckNotExists
	}
	// Consul may elide same-value writes; change the marker every time so its
	// ModifyIndex actually advances and fences concurrent different-app writes.
	ok, _, _, e := s.client.Txn().Txn(consul.TxnOps{{KV: check}, {KV: &consul.KVTxnOp{Verb: verb, Key: key, Value: b, Index: index}}, {KV: &consul.KVTxnOp{Verb: consul.KVSet, Key: s.prefix + "_catalog", Value: strconv.AppendUint(nil, epoch+1, 10)}}}, (&consul.QueryOptions{}).WithContext(ctx))
	if e != nil {
		return e
	}
	if !ok {
		return ErrConflict
	}
	return nil
}
func (s *ConsulStore) Delete(ctx context.Context, n string, index uint64) error {
	key, e := s.key(n)
	if e != nil {
		return e
	}
	_, epoch, e := s.Catalog(ctx)
	if e != nil {
		return e
	}
	return s.transact(ctx, consul.KVDeleteCAS, key, nil, index, epoch)
}
func (s *ConsulStore) Watch(ctx context.Context, handler func([]*App)) error {
	var index uint64
	for {
		apps, next, e := s.list(ctx, index)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		handler(apps)
		if next < index {
			index = 0
		} else {
			index = next
		}
		if index == 0 {
			index = 1
		}
	}
}
