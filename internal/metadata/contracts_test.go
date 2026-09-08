package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

func TestPersistedContractChunksAndIntegrity(t *testing.T) {
	var mu sync.Mutex
	kv := map[string][]byte{}
	fail := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/v1/kv/")
		if key == fail {
			w.WriteHeader(500)
			return
		}
		if r.Method == "PUT" {
			if _, exists := kv[key]; exists {
				io.WriteString(w, "false")
				return
			}
			kv[key], _ = io.ReadAll(r.Body)
			io.WriteString(w, "true")
			return
		}
		data, exists := kv[key]
		if !exists {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode([]*consul.KVPair{{Key: key, Value: data}})
	}))
	defer server.Close()
	c := &config.Config{Consul: config.ConsulConfig{Address: strings.TrimPrefix(server.URL, "http://"), Scheme: "http", MetadataPrefix: "apps"}}
	store, _ := NewConsulStore(c, NewConsulConfig(c))
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, _ := z.CreateHeader(&zip.FileHeader{Name: "idl/main.thrift", Method: zip.Store})
	content := strings.Repeat("// contract\n", 30000)
	io.WriteString(f, content)
	z.Close()
	digest := fmtDigest(b.Bytes())
	r := &archiveidl.Revision{Digest: digest, Archive: b.Bytes()}
	key, _ := store.contractKey(digest)
	ctx := context.Background()
	fail = key + "000001"
	if err := store.PutContract(ctx, r); err == nil {
		t.Fatal("write failure ignored")
	}
	if _, err := store.GetContract(ctx, digest); err == nil {
		t.Fatal("partial contract readable")
	}
	fail = ""
	if err := store.PutContract(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := store.PutContract(ctx, r); err != nil {
		t.Fatal("idempotent import", err)
	}
	// A new storage client restores without any download source.
	restored, _ := NewConsulStore(c, NewConsulConfig(c))
	got, err := restored.GetContract(ctx, digest)
	if err != nil || got.Files["idl/main.thrift"] != content {
		t.Fatal("restore failed", err)
	}
	mu.Lock()
	kv[key+"000000"][10] ^= 1
	mu.Unlock()
	if _, err := restored.GetContract(ctx, digest); err == nil {
		t.Fatal("corruption accepted")
	}
}
func fmtDigest(data []byte) string { return strings.ToLower(fmt.Sprintf("%x", sha256.Sum256(data))) }
