package httpgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	common "github.com/iamzhangxin/rpcxcommon/errors"
	"github.com/iamzhangxin/rpcxcommon/rpcmeta"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/kitex/client/callopt"
	"github.com/cloudwego/kitex/client/genericclient"
	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"

	"github.com/iamzhangxin/xuandu-gateway/internal/config"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
	rt "github.com/iamzhangxin/xuandu-gateway/internal/runtime"
)

type fakeClient struct {
	userID string
	info   rpcmeta.RequestInfo
	genericclient.Client
	response *generic.HTTPResponse
	err      error
	path     string
	method   string
	calls    int
}

func (f *fakeClient) Close() error { return nil }
func (f *fakeClient) GenericCall(ctx context.Context, m string, r any, _ ...callopt.Option) (any, error) {
	f.calls++
	f.userID = rpcmeta.UserId(ctx)
	f.info = rpcmeta.FromContext(ctx)
	f.method = m
	f.path = r.(*generic.HTTPRequest).Request.URL.RequestURI()
	if _, ok := ctx.Deadline(); !ok {
		panic("missing RPC deadline")
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.response != nil {
		return f.response, nil
	}
	return &generic.HTTPResponse{StatusCode: 201, RawBody: []byte(`{"ok":true}`)}, nil
}
func TestHandler(t *testing.T) {
	m := rt.NewManager()
	cli := &fakeClient{}
	m.ReplaceApp("p", &rt.ServiceRuntime{Config: metadata.App{Domain: "p.example"}, AppName: "p", Timeout: time.Second, Routes: []idl.Route{{HTTPMethod: "POST", Path: "/p/:id"}}, Client: cli})
	defer m.Close(context.Background())
	h := NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 16}}, m)
	engine := server.New()
	engine.NoRoute(h.Serve)
	body := func(s string) *ut.Body { return &ut.Body{Body: strings.NewReader(s), Len: len(s)} }
	for _, tc := range []struct {
		path, data string
		want       int
	}{{"/p/abc?x=1", `{"name":"x"}`, 201}, {"/none", `{}`, 404}, {"/p/1", `bad`, 400}, {"/p/1", `{"long":"1234567890"}`, 413}} {
		r := ut.PerformRequest(engine.Engine, "POST", tc.path, body(tc.data), ut.Header{Key: "Host", Value: "p.example"}, ut.Header{Key: "X-App-Code", Value: "p"})
		if r.Code != tc.want {
			t.Fatalf("%+v: %d", tc, r.Code)
		}
	}
	if cli.calls != 1 || cli.method != "" || cli.path != "/p/abc?x=1" {
		t.Fatal("request path/method changed", cli)
	}
	cli.err = kerrors.ErrNoInstance
	r := ut.PerformRequest(engine.Engine, "POST", "/p/1", body(`{}`), ut.Header{Key: "Host", Value: "p.example"}, ut.Header{Key: "X-App-Code", Value: "p"})
	if r.Code != 503 {
		t.Fatal(r.Code)
	}
}

func TestResponseEnvelope(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name                string
		err                 error
		out                 *generic.HTTPResponse
		status              int
		code, message, data string
	}{
		{name: "success raw JSON preserves numbers", out: &generic.HTTPResponse{RawBody: []byte(`{"id":9223372036854775807}`)}, status: 200, code: "000000", message: "success", data: `{"id":9223372036854775807}`},
		{name: "success object", out: &generic.HTTPResponse{Body: map[string]interface{}{"items": []string{"a"}}}, status: 200, code: "000000", message: "success", data: `{"items":["a"]}`},
		{name: "empty success", out: &generic.HTTPResponse{StatusCode: 204, RawBody: []byte{}}, status: 200, code: "000000", message: "success", data: `null`},
		{name: "参数无效", err: common.ToRpc(ctx, common.ErrInvalidArgument), status: 400, code: "400001", message: "参数无效", data: `null`},
		{name: "wrapped business error", err: fmt.Errorf("wrapper: %w", common.ToRpc(ctx, common.ErrNotFound)), status: 404, code: "404001", message: "资源不存在", data: `null`},
		{name: "internal cause is hidden", err: common.ToRpc(ctx, common.ErrReadFailed.WithCause(errors.New("database-password"))), status: 500, code: "500001", message: "内部错误", data: `null`},
		{name: "unclassified error", err: errors.New("secret"), status: 500, code: "500001", message: "内部错误", data: `null`},
		{name: "timeout", err: context.DeadlineExceeded, status: 504, code: "500203", message: "依赖不可用", data: `null`},
		{name: "no instance", err: kerrors.ErrNoInstance, status: 503, code: "500203", message: "依赖不可用", data: `null`},
		{name: "bad upstream JSON", out: &generic.HTTPResponse{RawBody: []byte("not JSON")}, status: 502, code: "500203", message: "依赖不可用", data: `null`},
		{name: "non-success upstream status", out: &generic.HTTPResponse{StatusCode: 403}, status: 403, code: "403001", message: "权限不足", data: `null`},
		{name: "invalid business code", err: kerrors.NewBizStatusError(0, "not success"), status: 500, code: "500001", message: "内部错误", data: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := rt.NewManager()
			defer m.Close(ctx)
			cli := &fakeClient{err: tc.err, response: tc.out}
			m.ReplaceApp("p", &rt.ServiceRuntime{Config: metadata.App{Domain: "p.example"}, AppName: "p", Timeout: time.Second, Routes: []idl.Route{{HTTPMethod: "GET", Path: "/p"}}, Client: cli})
			h := NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 1024}}, m)
			engine := server.New()
			engine.NoRoute(h.Serve)
			res := ut.PerformRequest(engine.Engine, "GET", "/p", nil, ut.Header{Key: "Host", Value: "p.example"}, ut.Header{Key: "X-App-Code", Value: "p"})
			var envelope struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if res.Code != tc.status || envelope.Code != tc.code || envelope.Message != tc.message || string(envelope.Data) != tc.data {
				t.Fatalf("HTTP %d: %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestUserIDHeaderPropagation(t *testing.T) {
	m := rt.NewManager()
	defer m.Close(context.Background())
	cli := &fakeClient{}
	m.ReplaceApp("p", &rt.ServiceRuntime{Config: metadata.App{Domain: "p.example"}, AppName: "p", Timeout: time.Second, Routes: []idl.Route{{HTTPMethod: "GET", Path: "/p"}}, Client: cli})
	h := NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 1024}}, m)
	engine := server.New()
	engine.NoRoute(h.Serve)
	for _, tc := range []struct{ header, value, want string }{
		{"X-User-ID", "user-123", "user-123"},
		{"x-user-id", "  user-456  ", "  user-456  "},
		{"X-User-ID", "", ""},
		{"Authorization", "Bearer ignored", ""},
		{"X-User-ID", "   ", "   "},
	} {
		response := ut.PerformRequest(engine.Engine, "GET", "/p", nil, ut.Header{Key: tc.header, Value: tc.value}, ut.Header{Key: "Host", Value: "p.example"}, ut.Header{Key: "X-App-Code", Value: "p"})
		if response.Code != 201 || cli.userID != tc.want {
			t.Fatalf("header %s: HTTP %d, user ID %q", tc.header, response.Code, cli.userID)
		}
	}
}

func TestLoginAndRequestMetadata(t *testing.T) {
	m := rt.NewManager()
	defer m.Close(context.Background())
	cli := &fakeClient{}
	files := map[string]string{"idl/main.thrift": `struct Q {1:string id} service S {Q Login(1:Q req)(api.get="/private",xuandu.auth="required") Q Public(1:Q req)(api.get="/public")}`}
	_, routes, err := idl.LoadArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceApp("p", &rt.ServiceRuntime{AppName: "p", Config: metadata.App{Domain: "p.example"}, Timeout: time.Second, Routes: routes, Client: cli}); err != nil {
		t.Fatal(err)
	}
	engine := server.New()
	engine.NoRoute(NewHandler(&config.Config{Server: config.ServerConfig{MaxRequestBodyBytes: 1024}}, m).Serve)
	for _, tc := range []struct {
		path, user string
		duplicate  bool
		want       int
	}{
		{"/private", "", false, 401}, {"/private", "   ", false, 201}, {"/private", "valid", true, 201}, {"/private", " user-1 ", false, 201}, {"/public", "", false, 201},
	} {
		headers := []ut.Header{{Key: "Host", Value: "p.example"}, {Key: "X-App-Code", Value: "p"}, {Key: "X-User-ID", Value: tc.user}, {Key: "X-Device-ID", Value: " device-1 "}, {Key: "X-Device-Type", Value: "mobile"}, {Key: "X-Device-Name", Value: "phone"}}
		if tc.duplicate {
			headers = append(headers, ut.Header{Key: "X-User-ID", Value: "forged"})
		}
		calls := cli.calls
		response := ut.PerformRequest(engine.Engine, "GET", tc.path, nil, headers...)
		if response.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, response.Code, response.Body.String())
		}
		if tc.want == 401 {
			var payload Response
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if cli.calls != calls || payload.Code != "401003" || payload.Message != "用户身份不能为空" || payload.Data != nil {
				t.Fatalf("identity gate: %+v calls=%d", payload, cli.calls)
			}
		} else {
			if cli.info != (rpcmeta.RequestInfo{UserId: tc.user, AppCode: "p", DeviceId: " device-1 ", DeviceType: "mobile", DeviceName: "phone"}) {
				t.Fatalf("metadata: %+v", cli.info)
			}
		}
	}
	response := ut.PerformRequest(engine.Engine, "GET", "/public", nil, ut.Header{Key: "Host", Value: "p.example"}, ut.Header{Key: "X-App-Code", Value: "p"})
	if response.Code != 201 || cli.info != (rpcmeta.RequestInfo{AppCode: "p"}) {
		t.Fatal("metadata leaked between requests")
	}
}
