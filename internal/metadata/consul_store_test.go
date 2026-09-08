package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	consul "github.com/hashicorp/consul/api"

	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

func TestCatalogTransaction(t *testing.T) {
	accepted := true
	var ops consul.TxnOps
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/txn" {
			t.Errorf("path %s", r.URL.Path)
		}
		if e := json.NewDecoder(r.Body).Decode(&ops); e != nil {
			t.Error(e)
		}
		if !accepted {
			w.WriteHeader(409)
			w.Write([]byte(`{"Errors":[{"OpIndex":0,"What":"CAS failed"}]}`))
			return
		}
		w.Write([]byte(`{"Results":[]}`))
	}))
	defer ts.Close()
	c := &config.Config{Consul: config.ConsulConfig{Address: strings.TrimPrefix(ts.URL, "http://"), Scheme: "http", MetadataPrefix: "xuandu/apps"}}
	s, e := NewConsulStore(c, NewConsulConfig(c))
	if e != nil {
		t.Fatal(e)
	}
	a := &App{Name: "p", Domain: "p.example", ServiceName: "p", RPCTimeout: "1s", Enabled: true, CatalogIndex: 17, IDL: IDLSource{Type: "zip", URL: "https://example.com/idl.zip", ResolvedRevision: strings.Repeat("a", 64)}}
	if e = s.CompareAndSwap(context.Background(), a, 8); e != nil {
		t.Fatal(e)
	}
	if len(ops) != 3 || ops[0].KV.Index != 17 || ops[0].KV.Key != "xuandu/apps/_catalog" || ops[1].KV.Key != "xuandu/apps/p" || ops[1].KV.Index != 8 {
		t.Fatalf("%+v", ops)
	}
	if string(ops[2].KV.Value) != "18" {
		t.Fatal("catalog marker must change with epoch")
	}
	var persisted App
	json.Unmarshal(ops[1].KV.Value, &persisted)
	if persisted.RPCTimeout != "1s" {
		t.Fatal("timeout lost")
	}
	accepted = false
	if e = s.Create(context.Background(), a); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestCatalogList(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("consistent") == "" { // QueryOptions encodes this as a bare flag.
			if _, ok := r.URL.Query()["consistent"]; !ok {
				t.Error("missing consistent read")
			}
		}
		w.Header().Set("X-Consul-Index", "50")
		pairs := consul.KVPairs{{Key: "apps/_catalog", ModifyIndex: 40, Value: []byte("1")}}
		for _, n := range []string{"z", "a"} {
			a := App{Name: n, ServiceName: n, RPCTimeout: "1s", IDL: IDLSource{Type: "zip", URL: "https://example.com/idl.zip", ResolvedRevision: strings.Repeat("b", 64)}}
			b, _ := json.Marshal(a)
			pairs = append(pairs, &consul.KVPair{Key: "apps/" + n, ModifyIndex: 39, Value: b})
		}
		json.NewEncoder(w).Encode(pairs)
	}))
	defer ts.Close()
	c := &config.Config{Consul: config.ConsulConfig{Address: strings.TrimPrefix(ts.URL, "http://"), Scheme: "http", MetadataPrefix: "apps"}}
	s, _ := NewConsulStore(c, NewConsulConfig(c))
	apps, epoch, e := s.Catalog(context.Background())
	if e != nil || len(apps) != 2 || apps[0].Name != "a" || epoch != 40 || apps[0].CatalogIndex != 40 {
		t.Fatal(apps, epoch, e)
	}
}
