package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	"github.com/iamzhangxin/xuandu-gateway/internal/openapi"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	"github.com/iamzhangxin/rpcxcommon/rpcmeta"
)

const fixture = `namespace go api
struct Req { 1: string spu_id (api.query='spuId') }
struct ListReq { 1: i32 page (api.body='page') }
struct Node { 1: string id; 2: list<Node> children }
struct Res { 1: string id; 2: list<Node> nodes }
service Product {
 Res Get(1: Req req) (api.get='/product/get')
 Res List(1: ListReq req) (api.post='/product/list')
}`

type memoryRepo struct {
	mu   sync.Mutex
	c    Catalog
	fail bool
}

func (r *memoryRepo) Load(context.Context) (Catalog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return Catalog{}, fmt.Errorf("offline")
	}
	b, _ := json.Marshal(r.c)
	var c Catalog
	json.Unmarshal(b, &c)
	return c, nil
}
func (r *memoryRepo) Save(_ context.Context, c Catalog, v uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.c.Version != v {
		return ErrConflict
	}
	c.Version = v + 1
	r.c = c
	return nil
}

type contracts struct {
	metadata.Store
	revision *archiveidl.Revision
	app      *metadata.App
}

func (c *contracts) List(context.Context) ([]*metadata.App, error) {
	return []*metadata.App{c.app}, nil
}
func (c *contracts) GetContract(_ context.Context, d string) (*archiveidl.Revision, error) {
	if d != c.revision.Digest {
		return nil, fmt.Errorf("missing")
	}
	return c.revision, nil
}

type fakeRPC struct {
	mu    sync.Mutex
	calls int
	spu   string
	user  string
	body  string
	err   error
}

func (f *fakeRPC) GenericCall(ctx context.Context, _ string, arg any, _ ...callopt.Option) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	req := arg.(*generic.HTTPRequest)
	f.spu = req.Request.URL.Query().Get("spuId")
	f.user = rpcmeta.UserId(ctx)
	f.body = string(req.RawBody)
	if f.err != nil {
		return nil, f.err
	}
	return &generic.HTTPResponse{StatusCode: 200, RawBody: []byte(`{"id":"9007199254740993","nodes":[{"id":"a","children":[{"id":"b","children":[]}]}]}`)}, nil
}
func (f *fakeRPC) Close() error { return nil }

type keyTransport struct {
	key  string
	base http.RoundTripper
}

func (k keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("X-MCP-Key", k.key)
	r.Header.Set("X-User-ID", "forged-user")
	return k.base.RoundTrip(r)
}
func setup(t *testing.T) (*Service, *httptest.Server, *fakeRPC, *memoryRepo) {
	t.Helper()
	files := map[string]string{"idl/product.thrift": fixture}
	_, routes, e := idl.LoadArchive(files)
	if e != nil {
		t.Fatal(e)
	}
	app := &metadata.App{Name: "product", Enabled: true, ServiceName: "product", IDL: metadata.IDLSource{Type: "zip", ResolvedRevision: strings.Repeat("a", 64)}}
	revision := &archiveidl.Revision{Digest: app.IDL.ResolvedRevision, Files: files}
	manager := rt.NewManager()
	rpc := &fakeRPC{}
	if e = manager.ReplaceApp(app.Name, &rt.ServiceRuntime{AppName: app.Name, Revision: revision.Digest, Routes: routes, Client: rpc, Timeout: time.Second}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { manager.Close(context.Background()) })
	repo := &memoryRepo{}
	s := &Service{repository: repo, contracts: &contracts{app: app, revision: revision}, manager: manager, config: config.McpConfig{Enabled: true}}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	doc, e := openapi.Generate(app.Name, revision.Digest, files)
	if e != nil {
		t.Fatal(e)
	}
	d, _ := documentFrom(doc)
	repo.c = Catalog{Version: 1, Servers: []ServerConfig{{Id: "server", Name: "Product MCP", App: app.Name, Endpoints: []string{"/product/mcp", "/alias/mcp"}, Header: "X-MCP-Key", Enabled: true}}, Tools: []ToolConfig{{Id: "get", ServerId: "server", Name: "get_product", OperationId: d.Paths["/product/get"]["get"].Id}, {Id: "list", ServerId: "server", Name: "list_products", OperationId: d.Paths["/product/list"]["post"].Id}}, Keys: []KeyConfig{{Id: "reader", Name: "Reader", Enabled: true, Hash: keyHash("test-key"), Grants: []Grant{{ServerId: "server", ToolIds: []string{"get"}}}}}}
	if e = s.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	if msg := s.current.Load().Servers["server"].Error; msg != "" {
		t.Fatal(msg)
	}
	return s, server, rpc, repo
}
func connect(t *testing.T, url, key string) *sdk.ClientSession {
	t.Helper()
	client := sdk.NewClient(&sdk.Implementation{Name: "gateway-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, e := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: url, HTTPClient: &http.Client{Transport: keyTransport{key, http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { session.Close() })
	return session
}
func TestSDKDiscoveryCallAndAuthorization(t *testing.T) {
	s, server, rpc, _ := setup(t)
	cs := connect(t, server.URL+"/product/mcp", "test-key")
	ctx := context.Background()
	tools, e := cs.ListTools(ctx, nil)
	if e != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "get_product" {
		t.Fatalf("tools: %+v %v", tools, e)
	}
	result, e := cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_product", Arguments: map[string]any{"spuId": "9007199254740993"}})
	if e != nil || result.IsError {
		t.Fatalf("call: %+v %v", result, e)
	}
	if !strings.Contains(result.Content[0].(*sdk.TextContent).Text, `"id":"9007199254740993"`) || result.StructuredContent == nil {
		t.Fatalf("result lost precision: %+v", result)
	}
	rpc.mu.Lock()
	if rpc.spu != "9007199254740993" || rpc.user != "" {
		t.Fatalf("binding / identity: %+v", rpc)
	}
	count := rpc.calls
	rpc.mu.Unlock()
	result, e = cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_products", Arguments: map[string]any{}})
	if e == nil && (result == nil || !result.IsError) {
		t.Fatal("unauthorized tool allowed")
	}
	rpc.mu.Lock()
	if rpc.calls != count {
		t.Fatal("unauthorized RPC executed")
	}
	rpc.mu.Unlock()
	cs = connect(t, server.URL+"/product/mcp", "test-key")
	result, e = cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_product", Arguments: map[string]any{"spuId": "x", "x-user-id": "forged"}})
	if e == nil && !result.IsError {
		t.Fatal("forged identity accepted")
	}
	alias := connect(t, server.URL+"/alias/mcp", "test-key")
	if _, e = alias.ListTools(ctx, nil); e != nil {
		t.Fatal(e)
	}
	rpc.mu.Lock()
	rpc.err = kerrors.NewBizStatusError(400001, "invalid argument")
	rpc.mu.Unlock()
	result, e = cs.CallTool(ctx, &sdk.CallToolParams{Name: "get_product", Arguments: map[string]any{}})
	if e != nil || !result.IsError || result.StructuredContent != nil || !strings.Contains(result.Content[0].(*sdk.TextContent).Text, "400001") {
		t.Fatalf("business error: %+v %v", result, e)
	}
	view, _ := s.View()
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), keyHash("test-key")) {
		t.Fatal("key hash leaked")
	}
}
func TestRoutingRevocationAndFreshness(t *testing.T) {
	s, server, _, repo := setup(t)
	probe := func(path, key, host string) int {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+path, strings.NewReader(`{}`))
		if key != "" {
			r.Header.Set("X-MCP-Key", key)
		}
		if host != "" {
			r.Host = host
		}
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	if probe("/product/mcp", "", "") != 401 || probe("/test/mcp", "test-key", "") != 404 || probe("/product/mcp", "", "other.example:9090") != 401 {
		t.Fatal("routing/auth isolation failed")
	}
	repo.mu.Lock()
	repo.c.Keys[0].Enabled = false
	repo.c.Version++
	repo.mu.Unlock()
	if e := s.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	if probe("/product/mcp", "test-key", "") != 401 {
		t.Fatal("revoked key allowed")
	}
	old := s.current.Load()
	next := *old
	next.Fresh = time.Now().Add(-61 * time.Second)
	s.current.Store(&next)
	if probe("/product/mcp", "test-key", "") != 503 {
		t.Fatal("stale auth allowed")
	}
}
func TestCatalogCASAndCredentialLifecycle(t *testing.T) {
	s, _, _, _ := setup(t)
	ctx := context.Background()
	res, e := s.Mutate(ctx, Mutation{Version: 1, Kind: "key", Key: KeyConfig{Name: "new", Enabled: true, Grants: []Grant{{ServerId: "server", ToolIds: []string{"get"}}}}})
	if e != nil {
		t.Fatal(e)
	}
	secret := res["secret"].(string)
	if !strings.HasPrefix(secret, "xuandu_") {
		t.Fatal("missing secret")
	}
	if _, e = s.Mutate(ctx, Mutation{Version: 1, Kind: "key", Key: KeyConfig{Name: "conflict"}}); e != ErrConflict {
		t.Fatalf("CAS: %v", e)
	}
	v, _ := s.View()
	encoded, _ := json.Marshal(v)
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), keyHash(secret)) {
		t.Fatal("secret leaked in view")
	}
	if _, e = s.Mutate(ctx, Mutation{Version: v.Version, Kind: "server", Server: ServerConfig{Name: "duplicate", App: "product", Endpoints: []string{s.current.Load().Catalog.Servers[0].Endpoints[0]}, Header: "X-MCP-Key"}}); e == nil {
		t.Fatal("endpoint collision allowed")
	}
}
func TestBodyBindingAndRecursiveSchema(t *testing.T) {
	s, _, _, _ := setup(t)
	b := s.current.Load().Servers["server"].Bindings["list"]
	r, e := b.Request(json.RawMessage(`{"page":2}`))
	if e != nil {
		t.Fatal(e)
	}
	if r.Method != "POST" || r.URL.Path != "/product/list" {
		t.Fatal("invalid body binding")
	}
	encoded, _ := json.Marshal(b.Output)
	if !strings.Contains(string(encoded), "#/$defs/") {
		t.Fatal("recursive schema closure missing")
	}
}

func TestIndependentServerAuthorizationAndSharedCatalog(t *testing.T) {
	s, server, _, repo := setup(t)
	ctx := context.Background()
	res, e := s.Mutate(ctx, Mutation{Version: 1, Kind: "server", Server: ServerConfig{Name: "Second MCP", App: "product", Endpoints: []string{"/test/mcp"}, Header: "Authorization", Bearer: true, Enabled: true}})
	if e != nil {
		t.Fatal(e)
	}
	secondId := res["id"].(string)
	// Another replica reads the same persisted catalog and independently builds SDK views.
	other := &Service{repository: repo, contracts: s.contracts, manager: s.manager, config: config.McpConfig{Enabled: true}}
	if e = other.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", server.URL+"/test/mcp", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	other.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("key crossed server boundary: %d", w.Code)
	}
	before, _ := s.View()
	key := before.Keys[0]
	key.Grants = append(key.Grants, Grant{ServerId: secondId, ToolIds: []string{"get"}})
	if _, e = s.Mutate(ctx, Mutation{Version: before.Version, Kind: "key", Id: key.Id, Key: key}); e == nil {
		t.Fatal("foreign-server tool grant accepted")
	}
}
func TestSchemaLiteralAndValidationIsolation(t *testing.T) {
	components := map[string]schema{"Node": {"type": "object", "properties": schema{"next": schema{"$ref": "#/components/schemas/Node"}}}}
	source := schema{"type": "object", "required": []any{"node"}, "properties": schema{"node": schema{"$ref": "#/components/schemas/Node", "description": "retain sibling"}}, "example": schema{"$ref": "not a schema reference"}}
	original, _ := json.Marshal(source)
	closed, e := closeSchema(source, components, true)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(source)
	if string(original) != string(after) {
		t.Fatal("source mutated")
	}
	if closed["required"] != nil {
		t.Fatal("business required unexpectedly imposed")
	}
	if closed["example"].(map[string]any)["$ref"] != "not a schema reference" {
		t.Fatal("literal rewritten")
	}
	if _, e = closeSchema(schema{"type": "object", "properties": schema{"x": schema{"$ref": "https://example.invalid/a"}}}, nil, true); e == nil {
		t.Fatal("external ref accepted")
	}
}
func TestRuntimeRevisionFence(t *testing.T) {
	s, server, rpc, _ := setup(t)
	old := s.manager.Load().Runtimes["product"]
	if e := s.manager.ReplaceApp("product", &rt.ServiceRuntime{AppName: "product", Revision: strings.Repeat("b", 64), Routes: old.Routes, Client: rpc, Timeout: time.Second}); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("POST", server.URL+"/product/mcp", strings.NewReader(`{}`))
	r.Header.Set("X-MCP-Key", "test-key")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("mixed contract revision allowed: %d", w.Code)
	}
	if e := s.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s.current.Load().Servers["server"].Error == "" {
		t.Fatal("missing updated contract considered ready")
	}
	if s.manager.Load().Runtimes["product"].Revision != strings.Repeat("b", 64) {
		t.Fatal("MCP failure modified HTTP runtime")
	}
}

type blockingRPC struct {
	entered chan struct{}
	release chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *blockingRPC) GenericCall(ctx context.Context, _ string, _ any, _ ...callopt.Option) (any, error) {
	close(b.entered)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-b.release:
		return &generic.HTTPResponse{StatusCode: 200, RawBody: []byte(`{"id":"old","nodes":[]}`)}, nil
	}
}
func (b *blockingRPC) Close() error { b.once.Do(func() { close(b.closed) }); return nil }
func TestInflightLeaseAndRequestCancellation(t *testing.T) {
	for _, cancelRequest := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelRequest), func(t *testing.T) {
			s, server, _, _ := setup(t)
			old := s.manager.Load().Runtimes["product"]
			block := &blockingRPC{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
			runtime := &rt.ServiceRuntime{AppName: old.AppName, Revision: old.Revision, Routes: old.Routes, Client: block, Timeout: 5 * time.Second}
			if e := s.manager.ReplaceApp("product", runtime); e != nil {
				t.Fatal(e)
			}
			if e := s.Refresh(context.Background()); e != nil {
				t.Fatal(e)
			}
			client := connect(t, server.URL+"/product/mcp", "test-key")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				r, e := client.CallTool(ctx, &sdk.CallToolParams{Name: "get_product", Arguments: map[string]any{"spuId": "x"}})
				if e == nil && r.IsError {
					e = fmt.Errorf("tool error")
				}
				done <- e
			}()
			select {
			case <-block.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("RPC not reached")
			}
			next := &rt.ServiceRuntime{AppName: old.AppName, Revision: strings.Repeat("b", 64), Routes: old.Routes, Client: &fakeRPC{}, Timeout: time.Second}
			if e := s.manager.ReplaceApp("product", next); e != nil {
				t.Fatal(e)
			}
			select {
			case <-block.closed:
				t.Fatal("inflight client closed early")
			default:
			}
			if cancelRequest {
				cancel()
			} else {
				close(block.release)
			}
			select {
			case e := <-done:
				if !cancelRequest && e != nil {
					t.Fatal(e)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("call did not end")
			}
			select {
			case <-block.closed:
			case <-time.After(3 * time.Second):
				t.Fatal("retired client not released / cancellation not propagated")
			}
		})
	}
}
func TestServerAccessMutationPreservesOtherGrants(t *testing.T) {
	s, _, _, _ := setup(t)
	ctx := context.Background()
	v, _ := s.View()
	_, e := s.Mutate(ctx, Mutation{Kind: "access", Version: v.Version, Id: "server", Access: []ServerAccess{{KeyId: "reader", ToolIds: []string{"get", "list"}}}})
	if e != nil {
		t.Fatal(e)
	}
	v, _ = s.View()
	if len(v.Keys[0].Grants[0].ToolIds) != 2 {
		t.Fatal("server grant not saved")
	}
	_, e = s.Mutate(ctx, Mutation{Kind: "access", Version: v.Version, Id: "server", Access: []ServerAccess{}})
	if e != nil {
		t.Fatal(e)
	}
	v, _ = s.View()
	if len(v.Keys[0].Grants) != 0 {
		t.Fatal("server access not revoked")
	}
}

func TestPathOnlyRouting(t *testing.T) {
	_, server, _, _ := setup(t)
	for _, base := range []string{server.URL, strings.Replace(server.URL, "127.0.0.1", "localhost", 1)} {
		session := connect(t, base+"/product/mcp", "test-key")
		listed, err := session.ListTools(context.Background(), nil)
		if err != nil || len(listed.Tools) != 1 {
			t.Fatalf("discovery: %+v %v", listed, err)
		}
	}
}

func TestLegacyURLConfigurationRejected(t *testing.T) {
	s, _, _, repo := setup(t)
	view, _ := s.View()
	server := view.Servers[0]
	server.Endpoints = []string{"https://old.example/product/mcp"}
	if _, err := s.Mutate(context.Background(), Mutation{Version: view.Version, Kind: "server", Id: server.Id, Server: server}); err == nil {
		t.Fatal("full URL accepted by management API")
	}
	saved, _ := repo.Load(context.Background())
	if saved.Version != view.Version || saved.Servers[0].Endpoints[0] != "/product/mcp" {
		t.Fatal("rejected mutation changed stored configuration")
	}
	repo.mu.Lock()
	repo.c.Servers[0] = server
	repo.c.Version++
	repo.mu.Unlock()
	// Simulate restart with persisted old configuration: reject it without migration.
	s.current.Store(nil)
	if err := s.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "不支持完整 URL") {
		t.Fatalf("legacy load: %v", err)
	}
	if s.current.Load() != nil {
		t.Fatal("invalid configuration published")
	}
	saved, _ = repo.Load(context.Background())
	if saved.Servers[0].Endpoints[0] != server.Endpoints[0] {
		t.Fatal("legacy URL rewritten")
	}
}

func TestPathRoutingOriginAndExactMatch(t *testing.T) {
	s, _, _, _ := setup(t)
	for _, tc := range []struct {
		path, host, origin string
		status             int
	}{
		{"/product/mcp", "gateway.example:8081", "http://gateway.example:8081", 401},
		{"/product/mcp", "gateway.example", "https://gateway.example", 401},
		{"/product/mcp", "gateway.example", "https://other.example", 403},
		{"/product/mcp", "gateway.example", "null", 403},
		{"/product/mcp", "gateway.example", "https://gateway.example/path", 403},
		{"/product/mcp", "gateway.example", "https://gateway.example:8081", 403},
		{"/product/mcp/", "gateway.example", "", 404},
		{"/product/%6dcp", "gateway.example", "", 404},
		{"/product/other", "gateway.example", "", 404},
	} {
		r := httptest.NewRequest("POST", "http://"+tc.host+tc.path, nil)
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%+v: got %d", tc, w.Code)
		}
	}
}

func TestEndpointPathValidation(t *testing.T) {
	for _, raw := range []string{"product/mcp", "//host/mcp", "/", "/healthz", "/a/", "/a//b", "/a/../b", "/a/./b", "/a?x=1", "/a?", "/a#", "/a#b", "/a%2Fb", "/a/{id}", "/a*b", "/a b", "https://host/mcp", "http://127.0.0.1:8081/mcp", "ftp://host/mcp", "https://user:pass@host/mcp"} {
		if _, err := endpointKey(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}
