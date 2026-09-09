package admin_test

import (
	"context"
	"encoding/json"
	"errors"

	"strings"
	"testing"

	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	"github.com/iamzhangxin/xuandu-gateway/internal/testutil"
)

func fixture(t *testing.T) (*admin.Service, *testutil.Store, *testutil.Fetcher, *testutil.Builder, admin.CreateAppRequest) {
	t.Helper()
	store := testutil.NewStore()
	f := &testutil.Fetcher{Revision: &archiveidl.Revision{Digest: strings.Repeat("a", 64), Files: map[string]string{"idl/main.thrift": `struct Q {1:string id} service S {Q Get(1:Q req)(api.get="/p")}`}}}
	b := &testutil.Builder{}
	s := admin.NewService(store, f, b, rt.NewManager())
	req := admin.CreateAppRequest{Name: "product", Domain: "product.example", ServiceName: "product", RPCTimeout: "1s", IDL: admin.ImportSource{Type: "zip", URL: "https://example.com/idl.zip"}}
	t.Cleanup(func() { s.Manager.Close(context.Background()) })
	return s, store, f, b, req
}
func TestCreateAndUpdateLKG(t *testing.T) {
	s, store, f, b, req := fixture(t)
	ctx := context.Background()
	a, e := s.CreateApp(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if a.IDL.ResolvedRevision != f.Revision.Digest {
		t.Fatal("not pinned")
	}
	if _, e = s.CreateApp(ctx, req); !errors.Is(e, metadata.ErrConflict) {
		t.Fatal(e)
	}
	old := s.Manager.Load()
	calls := b.Calls
	result, e := s.UpdateAppWithRequest(ctx, a.Name, admin.UpdateAppRequest{IDL: &req.IDL})
	if e != nil || result.Changed || b.Calls != calls || s.Manager.Load() != old {
		t.Fatal("unchanged rebuilt")
	}
	f.Revision.Digest = strings.Repeat("b", 64)
	store.Fail = true
	if _, e = s.UpdateAppWithRequest(ctx, a.Name, admin.UpdateAppRequest{IDL: &req.IDL}); e == nil || s.Manager.Load() != old {
		t.Fatal("CAS failure published")
	}
	store.Fail = false
	b.Fail = true
	if _, e = s.UpdateAppWithRequest(ctx, a.Name, admin.UpdateAppRequest{IDL: &req.IDL}); e == nil || s.Manager.Load() != old {
		t.Fatal("failed builder published")
	}
	b.Fail = false
	f.Revision.Files["idl/main.thrift"] = "not thrift !!!"
	if _, e = s.UpdateAppWithRequest(ctx, a.Name, admin.UpdateAppRequest{IDL: &req.IDL}); e == nil || s.Manager.Load() != old {
		t.Fatal("bad IDL published")
	}
	f.Revision.Files["idl/main.thrift"] = `struct Q {1:string id} service S {Q Get(1:Q req)(api.get="/new")}`
	result, e = s.UpdateAppWithRequest(ctx, a.Name, admin.UpdateAppRequest{IDL: &req.IDL})
	if e != nil || !result.Changed {
		t.Fatal(result, e)
	}
	if _, ok := s.Manager.Load().Match("product", "GET", "/new"); !ok {
		t.Fatal("route not updated")
	}
}
func TestCreateFailureDoesNotPersist(t *testing.T) {
	s, store, f, b, req := fixture(t)
	ctx := context.Background()
	f.Err = errors.New("git")
	if _, e := s.CreateApp(ctx, req); e == nil {
		t.Fatal("fetch failure")
	}
	f.Err = nil
	b.Fail = true
	if _, e := s.CreateApp(ctx, req); e == nil {
		t.Fatal("builder failure")
	}
	apps, _ := store.List(ctx)
	if len(apps) != 0 {
		t.Fatal("failure persisted")
	}
	b.Fail = false
	if _, e := s.CreateApp(ctx, req); e != nil {
		t.Fatal(e)
	}
	req.Name = "second"
	f.Revision.Files["idl/other.thrift"] = `struct Q {1:string id} service Duplicate {Q Get(1:Q req)(api.get="/p")}`
	if _, e := s.CreateApp(ctx, req); e == nil {
		t.Fatal("route conflict accepted")
	}
	if _, e := store.Get(ctx, "second"); !errors.Is(e, metadata.ErrNotFound) {
		t.Fatal("conflict persisted")
	}
}
func TestWatcherExactRevisionAndReadiness(t *testing.T) {
	s, store, f, b, req := fixture(t)
	ctx := context.Background()
	if ok, _ := s.Ready(); ok {
		t.Fatal("ready before load")
	}
	s.Reconcile(ctx, nil)
	if ok, _ := s.Ready(); !ok {
		t.Fatal("empty catalog not ready")
	}
	s.CreateApp(ctx, req)
	old := s.Manager.Load()
	apps, _ := store.List(ctx)
	apps[0].IDL.ResolvedRevision = strings.Repeat("b", 64)
	f.Revision.Digest = apps[0].IDL.ResolvedRevision
	store.PutContract(ctx, f.Revision)
	downloads := len(f.Refs)
	b.Fail = true
	s.Reconcile(ctx, apps)
	if s.Manager.Load() != old {
		t.Fatal("lost LKG")
	}
	if len(f.Refs) != downloads {
		t.Fatal("watch downloaded a URL")
	}
	ok, details := s.Ready()
	if !ok || len(details) != 1 {
		t.Fatal("LKG readiness", ok, details)
	}
	b.Fail = false
	s.Reconcile(ctx, apps)
	if s.Manager.Load().Runtimes[req.Name].Revision != apps[0].IDL.ResolvedRevision {
		t.Fatal("did not recover unchanged target")
	}
	s.Reconcile(ctx, nil)
	if len(s.Manager.Load().Runtimes) != 0 {
		t.Fatal("delete not reconciled")
	}
	s.Stop()
	if ok, _ := s.Ready(); ok {
		t.Fatal("ready after stop")
	}
}

func TestUpdateConfigurationAtSameRevision(t *testing.T) {
	s, _, _, b, req := fixture(t)
	ctx := context.Background()
	s.CreateApp(ctx, req)
	calls := b.Calls
	timeout := "2s"
	r, e := s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{RPCTimeout: &timeout})
	if e != nil || !r.Changed || b.Calls != calls+1 {
		t.Fatal(r, e)
	}
	if s.Manager.Load().Runtimes[req.Name].Config.RPCTimeout != "2s" {
		t.Fatal("timeout not published")
	}
	enabled := false
	if _, e = s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if len(s.Manager.Load().Runtimes) != 0 {
		t.Fatal("disabled runtime still published")
	}
	enabled = true
	if _, e = s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{Enabled: &enabled}); e != nil {
		t.Fatal(e)
	}
	if len(s.Manager.Load().Runtimes) != 1 {
		t.Fatal("enable not published")
	}
}
func TestFailedCreateDoesNotPoisonReadiness(t *testing.T) {
	s, _, _, _, req := fixture(t)
	ctx := context.Background()
	s.Reconcile(ctx, nil)
	s.CreateApp(ctx, req)
	req.Name = "product"
	if _, e := s.CreateApp(ctx, req); e == nil {
		t.Fatal("expected conflict")
	}
	if ok, _ := s.Ready(); !ok {
		t.Fatal("unpublished app affected readiness")
	}
}

func TestMigrateLegacySource(t *testing.T) {
	s, store, _, _, req := fixture(t)
	ctx := context.Background()
	legacy := &metadata.App{Name: req.Name, Domain: req.Domain, ServiceName: req.ServiceName, RPCTimeout: "1s", Enabled: true, IDL: metadata.IDLSource{Type: "git", ResolvedRevision: strings.Repeat("a", 40)}}
	store.Create(ctx, legacy)
	s.Reconcile(ctx, []*metadata.App{legacy})
	if ok, _ := s.Ready(); ok {
		t.Fatal("legacy source must not be ready")
	}
	_, e := s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{IDL: &req.IDL})
	if e != nil {
		t.Fatal(e)
	}
	if s.Manager.Load().Runtimes[req.Name] == nil {
		t.Fatal("migration did not publish")
	}
}

func TestDetailIncludesRoutesFromAllIDLFiles(t *testing.T) {
	s, _, f, _, req := fixture(t)
	f.Revision.Files["idl/other.thrift"] = `struct Q {1:string id} service Other {Q Put(1:Q req)(api.put="/other")}`
	if _, err := s.CreateApp(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	detail, err := s.Get(context.Background(), req.Name)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Routes) != 2 || detail.RouteCount != 2 || detail.CurrentRevision != f.Revision.Digest {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	paths := map[string]string{}
	for _, route := range detail.Routes {
		paths[route.Path] = route.IDLPath
	}
	if paths["/p"] != "idl/main.thrift" || paths["/other"] != "idl/other.thrift" {
		t.Fatalf("missing IDL paths: %+v", paths)
	}
	// Returned route metadata must not mutate the published runtime.
	detail.Routes[0].Path = "/changed"
	again, err := s.Get(context.Background(), req.Name)
	if err != nil || again.Routes[0].Path == "/changed" {
		t.Fatal("detail aliases runtime routes", err)
	}
}

func TestContractPersistenceFailureDoesNotPublish(t *testing.T) {
	s, store, f, _, req := fixture(t)
	ctx := context.Background()
	store.ContractFail = true
	if _, err := s.CreateApp(ctx, req); err == nil {
		t.Fatal("persistence failure ignored")
	}
	if len(store.Apps) != 0 || len(s.Manager.Load().Runtimes) != 0 {
		t.Fatal("unpersisted contract published")
	}
	store.ContractFail = false
	if _, err := s.CreateApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	old := s.Manager.Load()
	f.Revision.Digest = strings.Repeat("b", 64)
	store.ContractFail = true
	if _, err := s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{IDL: &req.IDL}); err == nil {
		t.Fatal("update persistence failure ignored")
	}
	if s.Manager.Load() != old || store.Apps[req.Name].IDL.ResolvedRevision != strings.Repeat("a", 64) {
		t.Fatal("failed update changed published version")
	}
}

func TestOpenAPIUsesCurrentRevisionAfterFailedUpdate(t *testing.T) {
	s, _, f, b, req := fixture(t)
	ctx := context.Background()
	if _, err := s.CreateApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	old := f.Revision.Digest
	f.Revision.Digest = strings.Repeat("b", 64)
	b.Fail = true
	s.UpdateAppWithRequest(ctx, req.Name, admin.UpdateAppRequest{IDL: &req.IDL})
	doc, err := s.OpenAPI(ctx, req.Name)
	if err != nil {
		t.Fatal(err)
	}
	if doc["info"].(map[string]any)["version"] != old {
		t.Fatal("documentation used failed revision")
	}
	if _, err := s.OpenAPI(ctx, "missing"); !errors.Is(err, metadata.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDomainsPublicationAndLegacyUpgrade(t *testing.T) {
	s, store, f, b, req := fixture(t)
	ctx := context.Background()
	req.Domain = "Product.Example."
	if _, err := s.CreateApp(ctx, req); err != nil {
		t.Fatal(err)
	}
	a, _ := store.Get(ctx, req.Name)
	if a.Domain != "product.example" {
		t.Fatal("domain not normalized")
	}
	req.Name = "second"
	req.Domain = "second.example"
	if _, err := s.CreateApp(ctx, req); err != nil {
		t.Fatal("same path on another domain must be accepted", err)
	}
	downloads := len(f.Refs)
	domain := "new.example"
	if _, err := s.UpdateAppWithRequest(ctx, "product", admin.UpdateAppRequest{Domain: &domain}); err != nil {
		t.Fatal(err)
	}
	if len(f.Refs) != downloads {
		t.Fatal("domain change re-downloaded contract")
	}
	if s.Manager.Load().HasApp("product.example", "product") || !s.Manager.Load().HasApp(domain, "product") {
		t.Fatal("old host binding retained")
	}
	enabled := false
	if _, err := s.UpdateAppWithRequest(ctx, "product", admin.UpdateAppRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAppWithRequest(ctx, "second", admin.UpdateAppRequest{Domain: &domain}); err != nil {
		t.Fatal("applications must be able to share a domain", err)
	}
	// Simulate persisted, pre-domain metadata on restart.
	apps, _ := store.List(ctx)
	for _, a := range apps {
		a.Domain = ""
		if err := store.CompareAndSwap(ctx, a, a.ModifyIndex); err != nil {
			t.Fatal(err)
		}
	}
	apps, _ = store.List(ctx)
	s.Reconcile(ctx, apps)
	if len(s.Manager.Load().Runtimes) != 0 {
		t.Fatal("legacy identities remained routable")
	}
	if ready, _ := s.Ready(); !ready {
		t.Fatal("unconfigured legacy domain blocked console through Kubernetes readiness")
	}
	detail, err := s.Get(ctx, "second")
	if err != nil || detail.RuntimeStatus != "unconfigured" {
		t.Fatal("missing unconfigured status", err)
	}
	domain = "restored.example"
	if _, err := s.UpdateAppWithRequest(ctx, "second", admin.UpdateAppRequest{Domain: &domain}); err != nil {
		t.Fatal(err)
	}
	if !s.Manager.Load().HasApp(domain, "second") {
		t.Fatal("legacy contract not restored")
	}
	// A newly observed domain cannot retain an old host when its rebuild fails.
	apps, _ = store.List(ctx)
	for _, a := range apps {
		if a.Name == "second" {
			a.Domain = "changed.example"
		}
	}
	b.Fail = true
	s.Reconcile(ctx, apps)
	if s.Manager.Load().HasApp(domain, "second") {
		t.Fatal("LKG retained obsolete identity")
	}
}

func TestOpenAPIApplicationIdentity(t *testing.T) {
	s, _, _, _, req := fixture(t)
	if _, err := s.CreateApp(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	doc, err := s.OpenAPI(context.Background(), req.Name)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Servers []struct{ URL string }
		Paths   map[string]map[string]struct {
			Parameters []struct {
				Name     string
				In       string
				Required bool
				Schema   struct{ Example string }
			}
		}
	}
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Servers) != 2 || output.Servers[0].URL != "https://product.example" {
		t.Fatal("missing application servers")
	}
	for _, methods := range output.Paths {
		for _, op := range methods {
			found := false
			for _, p := range op.Parameters {
				if p.Name == "X-App-Code" && p.In == "header" && p.Required && p.Schema.Example == req.Name {
					found = true
				}
			}
			if !found {
				t.Fatal("missing application header")
			}
		}
	}
}
