package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/kitex/client/genericclient"

	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
)

type closeClient struct {
	genericclient.Client
	count atomic.Int32
}

func (c *closeClient) Close() error { c.count.Add(1); return nil }
func fixture(n, p string) *ServiceRuntime {
	return &ServiceRuntime{Config: metadata.App{Domain: n + ".example"}, AppName: n, Routes: []idl.Route{{HTTPMethod: "GET", Path: p}}, Client: &closeClient{}}
}
func TestSnapshotDrainAcrossGenerations(t *testing.T) {
	m := NewManager()
	a, b := fixture("a", "/a"), fixture("b", "/b")
	m.ReplaceApp("a", a)
	lease1 := m.Acquire()
	m.ReplaceApp("b", b)
	lease2 := m.Acquire()
	next := fixture("a", "/a")
	m.ReplaceApp("a", next)
	if m.Load().Runtimes["b"] != b {
		t.Fatal("unrelated runtime replaced")
	}
	c := a.Client.(*closeClient)
	if c.count.Load() != 0 {
		t.Fatal("premature close")
	}
	lease2.Release()
	if c.count.Load() != 0 {
		t.Fatal("older snapshot still leased")
	}
	lease1.Release()
	if c.count.Load() != 1 {
		t.Fatal("retired runtime not closed")
	}
	a.Close()
	if c.count.Load() != 1 {
		t.Fatal("close not idempotent")
	}
	m.Close(context.Background())
}
func TestSnapshotCASFailureAndConflict(t *testing.T) {
	m := NewManager()
	m.ReplaceApp("a", fixture("a", "/a"))
	before := m.Load()
	persisted := false
	conflict := fixture("b", "/a")
	conflict.Config.Domain = "a.example"
	if e := m.Publish("b", conflict, func() error { persisted = true; return nil }); e == nil || persisted || m.Load() != before {
		t.Fatal("conflict changed state")
	}
	if e := m.Publish("a", fixture("a", "/new"), func() error { return errors.New("CAS") }); e == nil || m.Load() != before {
		t.Fatal("CAS failure published")
	}
}
func TestSnapshotPersistDoesNotBlockTraffic(t *testing.T) {
	m := NewManager()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		m.Publish("a", fixture("a", "/a"), func() error { close(entered); <-release; return nil })
		close(done)
	}()
	<-entered
	acquired := make(chan struct{})
	go func() { l := m.Acquire(); l.Release(); close(acquired) }()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("CAS blocks traffic")
	}
	close(release)
	<-done
	m.Close(context.Background())
}
func TestSnapshotConcurrent(t *testing.T) {
	m := NewManager()
	m.ReplaceApp("a", fixture("a", "/a"))
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				l := m.Acquire()
				l.Snapshot.Match("a", "GET", "/a")
				l.Release()
			}
		})
	}
	for i := 0; i < 50; i++ {
		m.ReplaceApp("a", fixture("a", "/a"))
	}
	wg.Wait()
	if e := m.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if m.Acquire() != nil {
		t.Fatal("lease after shutdown")
	}
}
