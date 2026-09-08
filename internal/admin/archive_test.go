package admin_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	"github.com/iamzhangxin/xuandu-gateway/internal/testutil"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

func zipBody(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	var names []string
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte(files[n]))
	}
	w.Close()
	return b.Bytes()
}
func TestZIPPublicationAtomic(t *testing.T) {
	files := map[string]string{"idl/common.thrift": `struct Q {1:string name}`, "idl/a.thrift": `include "common.thrift" service A {common.Q Get(1:common.Q req)(api.get="/a")}`, "idl/b.thrift": `include "common.thrift" service B {common.Q Get(1:common.Q req)(api.get="/b")}`}
	data := zipBody(t, files)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer server.Close()
	ctx := context.Background()
	store := testutil.NewStore()
	manager := rt.NewManager()
	defer manager.Close(ctx)
	service := admin.NewService(store, archiveidl.NewFetcher(), rt.NewBuilder(consul.DefaultConfig()), manager)
	req := admin.CreateAppRequest{Name: "p", Domain: "p.example", ServiceName: "p", RPCTimeout: "1s", IDL: admin.ImportSource{Type: "zip", URL: server.URL}}
	a, e := service.CreateApp(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	old := manager.Load()
	for _, p := range []string{"/a", "/b"} {
		if _, ok := old.Match("p", "GET", p); !ok {
			t.Fatal("missing route", p)
		}
	}
	r, e := service.UpdateAppWithRequest(ctx, "p", admin.UpdateAppRequest{IDL: &admin.ImportSource{Type: "zip", URL: server.URL}})
	if e != nil || r.Changed || manager.Load() != old {
		t.Fatal("identical ZIP rebuilt", r, e)
	}
	data = []byte("not zip")
	if _, e = service.UpdateAppWithRequest(ctx, "p", admin.UpdateAppRequest{IDL: &admin.ImportSource{Type: "zip", URL: server.URL}}); e == nil || manager.Load() != old {
		t.Fatal("bad ZIP replaced runtime")
	}
	files["idl/b.thrift"] = "invalid !"
	data = zipBody(t, files)
	if _, e = service.UpdateAppWithRequest(ctx, "p", admin.UpdateAppRequest{IDL: &admin.ImportSource{Type: "zip", URL: server.URL}}); e == nil || manager.Load() != old {
		t.Fatal("bad second IDL replaced runtime")
	}
	files["idl/b.thrift"] = `include "common.thrift" service B {common.Q Get(1:common.Q req)(api.get="/new")}`
	data = zipBody(t, files)
	service.Reconcile(ctx, []*metadata.App{a})
	if manager.Load() != old {
		t.Fatal("watch accepted unpinned bytes")
	}
	r, e = service.UpdateAppWithRequest(ctx, "p", admin.UpdateAppRequest{IDL: &admin.ImportSource{Type: "zip", URL: server.URL}})
	if e != nil || !r.Changed {
		t.Fatal(r, e)
	}
	if _, ok := manager.Load().Match("p", "GET", "/new"); !ok {
		t.Fatal("second IDL update lost")
	}
	if _, ok := manager.Load().Match("p", "GET", "/b"); ok {
		t.Fatal("old route retained")
	}
	if _, ok := manager.Load().Match("p", "GET", "/a"); !ok {
		t.Fatal("first IDL route lost")
	}
}

func TestRestartRestoresWithoutDownloadURL(t *testing.T) {
	data := zipBody(t, map[string]string{"idl/a.thrift": `struct Q {1:string id} service A {Q Get(1:Q req)(api.get="/a")}`, "idl/b.thrift": `struct Q {1:string id} service B {Q Get(1:Q req)(api.get="/b")}`})
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); w.Write(data) }))
	store := testutil.NewStore()
	first := rt.NewManager()
	service := admin.NewService(store, archiveidl.NewFetcher(), &testutil.Builder{}, first)
	ctx := context.Background()
	req := admin.CreateAppRequest{Name: "p", Domain: "p.example", ServiceName: "p", RPCTimeout: "1s", IDL: admin.ImportSource{Type: "zip", URL: server.URL + "?secret=one-use"}}
	if _, err := service.CreateApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	server.Close()
	first.Close(ctx)
	apps, _ := store.List(ctx)
	raw, _ := json.Marshal(apps)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "url") || apps[0].IDL.URL != "" {
		t.Fatal("download URL retained")
	}
	second := rt.NewManager()
	defer second.Close(ctx)
	restarted := admin.NewService(store, archiveidl.NewFetcher(), &testutil.Builder{}, second)
	restarted.Reconcile(ctx, apps)
	if ready, _ := restarted.Ready(); !ready {
		t.Fatal("restart not ready")
	}
	detail, err := restarted.Get(ctx, "p")
	if err != nil || len(detail.Routes) != 2 || detail.CurrentRevision != apps[0].IDL.ResolvedRevision {
		t.Fatal("routes not restored", err)
	}
	timeout := "2s"
	if _, err := restarted.UpdateAppWithRequest(ctx, "p", admin.UpdateAppRequest{RPCTimeout: &timeout}); err != nil {
		t.Fatal(err)
	}
	if downloads.Load() != 1 {
		t.Fatal("download repeated")
	}
}
