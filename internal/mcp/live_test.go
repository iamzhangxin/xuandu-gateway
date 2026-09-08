package mcp

import (
	"context"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Opt-in: the caller explicitly selects an operation and should use a read-only endpoint.
// MCP configuration and keys are in memory; this never writes Consul metadata.
func TestLiveOperation(t *testing.T) {
	path := os.Getenv("XUANDU_MCP_VERIFY_CONFIG")
	if path == "" {
		t.Skip("set XUANDU_MCP_VERIFY_CONFIG for a real, read-only backend check")
	}
	appName := os.Getenv("XUANDU_MCP_VERIFY_APP")
	operation := os.Getenv("XUANDU_MCP_VERIFY_OPERATION")
	if appName == "" || operation == "" {
		t.Fatal("set XUANDU_MCP_VERIFY_APP and XUANDU_MCP_VERIFY_OPERATION")
	}
	c, e := config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	cc := metadata.NewConsulConfig(c)
	store, e := metadata.NewConsulStore(c, cc)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, e := store.Get(ctx, appName)
	if e != nil {
		t.Fatal(e)
	}
	revision, e := store.GetContract(ctx, a.IDL.ResolvedRevision)
	if e != nil {
		t.Fatal(e)
	}
	bundles, routes, e := idl.LoadArchive(revision.Files)
	if e != nil {
		t.Fatal(e)
	}
	runtime, e := rt.NewBuilder(cc).Build(ctx, a, revision, bundles, routes)
	if e != nil {
		t.Fatal(e)
	}
	manager := rt.NewManager()
	if e = manager.ReplaceApp(a.Name, runtime); e != nil {
		t.Fatal(e)
	}
	defer manager.Close(context.Background())

	repository := &memoryRepo{}
	s := &Service{repository: repository, contracts: store, manager: manager, config: config.McpConfig{Enabled: true}}
	server := httptest.NewServer(s)
	defer server.Close()
	secret := hexSecret()
	repository.c = Catalog{Version: 1, Servers: []ServerConfig{{Id: "live", Name: "Explicit operation verification", App: a.Name, Endpoints: []string{"/verify/mcp"}, Header: "X-MCP-Key", Enabled: true}}, Tools: []ToolConfig{{Id: "tree", ServerId: "live", Name: "verify_operation", OperationId: operation}}, Keys: []KeyConfig{{Id: "test", Name: "Ephemeral test key", Enabled: true, Hash: keyHash(secret), Grants: []Grant{{ServerId: "live", ToolIds: []string{"tree"}}}}}}
	if e = s.Refresh(ctx); e != nil {
		t.Fatal(e)
	}
	if msg := s.current.Load().Servers["live"].Error; msg != "" {
		t.Fatal(msg)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "gateway-live-test", Version: "1"}, nil)
	session, e := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL + "/verify/mcp", HTTPClient: &http.Client{Transport: liveDomainTransport{domain: a.Domain, base: keyTransport{secret, http.DefaultTransport}}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	result, e := session.CallTool(ctx, &sdk.CallToolParams{Name: "verify_operation", Arguments: map[string]any{}})
	if e != nil {
		t.Fatal(e)
	}
	if result.IsError {
		t.Fatalf("operation call failed: %+v", result.Content)
	}
	if result.StructuredContent == nil {
		t.Fatal("missing structured result")
	}
	t.Log("Consul contract -> SDK -> shared executor -> Kitex TTHeader operation succeeded")
}

// Override only the Host of the local test request; Consul metadata stays untouched.
type liveDomainTransport struct {
	domain string
	base   http.RoundTripper
}

func (t liveDomainTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = t.domain
	return t.base.RoundTrip(r)
}
