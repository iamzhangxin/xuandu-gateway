package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/kitex/pkg/generic"
	"github.com/cloudwego/kitex/pkg/kerrors"
	"github.com/cloudwego/kitex/pkg/transmeta"
	"github.com/cloudwego/kitex/server"
	"github.com/cloudwego/kitex/server/genericserver"
	consul "github.com/hashicorp/consul/api"
	common "github.com/iamzhangxin/rpcxcommon/errors"
	"github.com/iamzhangxin/rpcxcommon/rpcmeta"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/idl"
	"github.com/iamzhangxin/xuandu-gateway/internal/metadata"
)

// Exercise the actual TTHeader wire and generic decoder, including the empty
// Thrift result returned by Kitex when a handler returns a business error.
func TestBusinessErrorAcrossTTHeader(t *testing.T) {
	// Kitex shares balancer factories by resolver name across clients. Isolate
	// the fake Consul endpoint from clients constructed by other package tests.
	if os.Getenv("XUANDU_WIRE_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBusinessErrorAcrossTTHeader$", "-test.timeout=30s")
		cmd.Env = append(os.Environ(), "XUANDU_WIRE_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("wire test failed: %v\n%s", err, output)
		}
		return
	}

	files := map[string]string{"idl/main.thrift": `struct Q {1:string name} service S {Q Good(1:Q req)(api.get="/good") Q Bad(1:Q req)(api.get="/bad")}`}
	provider, err := generic.NewThriftContentWithAbsIncludePathProvider("idl/main.thrift", files)
	if err != nil {
		t.Fatal(err)
	}
	g, err := generic.MapThriftGeneric(provider)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	svr := genericserver.NewServerV2(&generic.ServiceV2{GenericCall: func(ctx context.Context, service, method string, request interface{}) (interface{}, error) {
		if service != "S" {
			return nil, common.ToRpc(ctx, common.ErrNotFound)
		}
		if method == "Bad" {
			return nil, common.ToRpc(ctx, common.ErrInvalidArgument)
		}
		payload, _ := json.Marshal(rpcmeta.FromContext(ctx))
		return map[string]interface{}{"name": string(payload)}, nil
	}}, g, server.WithListener(listener), server.WithMetaHandler(transmeta.ServerTTHeaderHandler))
	done := make(chan error, 1)
	go func() { done <- svr.Run() }()
	defer func() { svr.Stop(); <-done }()
	address := listener.Addr().(*net.TCPAddr)
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]*consul.ServiceEntry{{Service: &consul.AgentService{Service: "business-error-wire-test", Address: "127.0.0.1", Port: address.Port, Weights: consul.AgentWeights{Passing: 1}}}})
	}))
	defer discovery.Close()
	cc := consul.DefaultConfig()
	cc.Address = strings.TrimPrefix(discovery.URL, "http://")
	bundles, routes, err := idl.LoadArchive(files)
	if err != nil {
		t.Fatal(err)
	}
	a := &metadata.App{Name: "p", Domain: "p.example", ServiceName: "business-error-wire-test", RPCTimeout: "3s", IDL: metadata.IDLSource{Type: "zip", ResolvedRevision: strings.Repeat("a", 64)}}
	runtime, err := NewBuilder(cc).Build(context.Background(), a, &archiveidl.Revision{Digest: a.IDL.ResolvedRevision}, bundles, routes)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for i, path := range []string{"/bad", "/good", "/good"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		userID := ""
		if i == 1 {
			userID = "user-wire-123"
		}
		info := rpcmeta.RequestInfo{}
		if i == 1 {
			info = rpcmeta.RequestInfo{UserId: userID, AppCode: "p", DeviceId: "device-wire", DeviceType: "mobile", DeviceName: "手机"}
		}
		ctx = rpcmeta.WithRequestInfo(ctx, info)
		req, _ := http.NewRequest("GET", "http://gateway"+path, nil)
		input, _ := generic.FromHTTPRequest(req)
		response, err := runtime.Client.GenericCall(ctx, "", input)
		cancel()
		if path == "/bad" {
			biz, ok := kerrors.FromBizStatusError(err)
			if !ok || biz.BizStatusCode() != 400001 {
				t.Fatalf("business error lost: %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(info)
			if response.(*generic.HTTPResponse).Body["name"] != string(want) {
				t.Fatalf("unexpected response: %+v", response)
			}
		}
	}
}
