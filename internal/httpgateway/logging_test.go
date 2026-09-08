package httpgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
	common "github.com/iamzhangxin/rpcxcommon/errors"
)

func TestRequestLogDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, body, level, source, reason, code string
		err                                     error
	}{
		{name: "business", body: `{}`, level: "WARN", source: "upstream_business", reason: "business_error", code: "400001", err: common.ToRpc(context.Background(), common.ErrInvalidArgument)},
		{name: "bad JSON", body: `{secret-body`, level: "WARN", source: "gateway_validation", reason: "invalid_json", code: "400001"},
		{name: "RPC failure", body: `{}`, level: "ERROR", source: "upstream_rpc", reason: "rpc_call_failed", code: "500203", err: kerrors.ErrGetConnection},
		{name: "success", body: `{}`, level: "INFO", code: "000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := rt.NewManager()
			defer manager.Close(context.Background())
			cli := &fakeClient{err: tc.err}
			manager.ReplaceApp("product", &rt.ServiceRuntime{AppName: "product", ServiceName: "product-service", Revision: "revision", Timeout: time.Second, Routes: []idl.Route{{HTTPMethod: "POST", Path: "/products/:id", RPCService: "Product", RPCMethod: "Get", IDLPath: "idl/product.thrift"}}, Client: cli})
			h := NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 1024}}, manager)
			var logs bytes.Buffer
			h.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			engine := server.New()
			engine.NoRoute(h.Serve)
			res := ut.PerformRequest(engine.Engine, "POST", "/products/123?token=secret-query&id=private-query-a&id=private-query-b", &ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)})
			var event map[string]any
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{"level": tc.level, "code": tc.code, "rpc_service": "Product", "rpc_method": "Get", "idl_file": "idl/product.thrift", "route": "/products/:id"} {
				if event[key] != want {
					t.Fatalf("%s: %v", key, event[key])
				}
			}
			if tc.source != "" {
				if event["error_source"] != tc.source || event["error_reason"] != tc.reason {
					t.Fatalf("wrong error source: %v", event)
				}
			} else if _, ok := event["error_source"]; ok {
				t.Fatal("success has error source")
			}
			if event["request_id"] != res.Header().Get("X-Request-ID") {
				t.Fatal("request ID not correlated")
			}
			if _, ok := event["duration_ms"].(float64); !ok {
				t.Fatal("missing millisecond duration")
			}
			keys, _ := json.Marshal(event["query_keys"])
			if string(keys) != `["id","token"]` {
				t.Fatalf("query keys: %s", keys)
			}
			for _, secret := range []string{"secret-query", "secret-body", "private-query-a", "private-query-b"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("logged request value %s", secret)
				}
			}
			if tc.name == "business" && event["message"] != "invalid argument" {
				t.Fatal("business description lost")
			}
		})
	}
}
