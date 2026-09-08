package httpgateway

import (
	"context"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	"testing"
	"time"
)

func TestDomainAndAppCodeIsolation(t *testing.T) {
	m := rt.NewManager()
	defer m.Close(context.Background())
	clients := map[string]*fakeClient{"a": {}, "b": {}}
	for name, client := range clients {
		if err := m.ReplaceApp(name, &rt.ServiceRuntime{AppName: name, Config: metadata.App{Domain: name + ".example"}, Timeout: time.Second, Routes: []idl.Route{{HTTPMethod: "GET", Path: "/api/product/get"}}, Client: client}); err != nil {
			t.Fatal(err)
		}
	}
	engine := server.New()
	engine.NoRoute(NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 1024}}, m).Serve)
	for _, tc := range []struct {
		host      string
		codes     []string
		forwarded string
		status    int
		app       string
	}{
		{"a.example", []string{"a"}, "", 201, "a"}, {"B.Example.:8080", []string{"b"}, "", 201, "b"},
		{"a.example", nil, "", 403, ""}, {"a.example", []string{"b"}, "", 403, ""}, {"a.example", []string{"A"}, "", 403, ""},
		{"a.example", []string{"a", "a"}, "", 403, ""}, {"a.example", []string{"a, b"}, "", 403, ""},
		{"unknown.example", []string{"a"}, "a.example", 404, ""}, {"a.example", []string{"b"}, "b.example", 403, ""},
	} {
		beforeA, beforeB := clients["a"].calls, clients["b"].calls
		headers := []ut.Header{{Key: "Host", Value: tc.host}, {Key: "X-Forwarded-Host", Value: tc.forwarded}}
		for _, code := range tc.codes {
			headers = append(headers, ut.Header{Key: "X-App-Code", Value: code})
		}
		res := ut.PerformRequest(engine.Engine, "GET", "/api/product/get", nil, headers...)
		if res.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, res.Code, res.Body.String())
		}
		deltaA, deltaB := clients["a"].calls-beforeA, clients["b"].calls-beforeB
		if (tc.app == "a" && (deltaA != 1 || deltaB != 0)) || (tc.app == "b" && (deltaB != 1 || deltaA != 0)) || (tc.app == "" && deltaA+deltaB != 0) {
			t.Fatalf("wrong application invoked: %+v", tc)
		}
	}
}
