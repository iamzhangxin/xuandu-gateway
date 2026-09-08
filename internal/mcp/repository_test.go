package mcp

import (
	"context"
	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"strings"
	"testing"
)

func TestMcpCatalogNeverFallsUnderApplicationPrefix(t *testing.T) {
	for _, prefix := range []string{"xuandu/apps", "custom", "namespace/custom/apps", "apps"} {
		c := &config.Config{Consul: config.ConsulConfig{MetadataPrefix: prefix}}
		r, e := NewRepository(c, consul.DefaultConfig())
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasPrefix(r.path, prefix+"/") {
			t.Fatalf("MCP metadata pollutes application catalog: %s", r.path)
		}
		if prefix == "xuandu/apps" && r.path != "xuandu/mcp/catalog" {
			t.Fatal(r.path)
		}
	}
}
func TestListenerFailureIsIsolated(t *testing.T) {
	s, _, _, _ := setup(t)
	s.config.Address = "invalid address"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	if !s.listenerFailed.Load() {
		t.Fatal("listener failure not recorded")
	}
	if s.manager.Load().Runtimes["product"] == nil {
		t.Fatal("HTTP runtime affected")
	}
	v, e := s.View()
	if e != nil || v.ListenerError == "" {
		t.Fatal("listener failure missing from admin status")
	}
}
