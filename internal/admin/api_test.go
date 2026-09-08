package admin_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"

	"github.com/iamzhangxin/xuandu-gateway/internal/admin"
	"github.com/iamzhangxin/xuandu-gateway/internal/config"
)

func TestAPI(t *testing.T) {
	s, _, _, _, _ := fixture(t)
	h := admin.NewServer(&config.Config{Admin: config.AdminConfig{Enabled: true, Address: "127.0.0.1:0"}}, s)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/admin/apps/missing", "", 404}, {"POST", "/admin/apps", "{", 400}, {"POST", "/admin/apps", `{"name":"p","domain":"p.example","serviceName":"p","rpcTimeout":"1s","idl":{"type":"zip","url":"https://example.com/idl.zip"}}`, 200}, {"POST", "/admin/apps/p/update", "", 200}, {"POST", "/admin/apps/p/update", `{"rpcTimeout":"2s"}`, 200}, {"POST", "/admin/apps/p/update", `{"unexpected":true}`, 400}, {"GET", "/admin/apps/p", "", 200}, {"GET", "/", "", 200}, {"DELETE", "/admin/apps/p", "", 200}, {"GET", "/admin/apps/p", "", 404}} {
		var body *ut.Body
		if tc.body != "" {
			body = &ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)}
		}
		r := ut.PerformRequest(h.Engine, tc.method, tc.path, body)
		if r.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, r.Code, r.Body.String())
		}
	}
}

func TestConsoleAssets(t *testing.T) {
	s, _, _, _, _ := fixture(t)
	h := admin.NewServer(&config.Config{Admin: config.AdminConfig{Address: "127.0.0.1:0"}}, s)
	index := ut.PerformRequest(h.Engine, "GET", "/", nil)
	if index.Code != 200 || !strings.Contains(index.Body.String(), "xuandu") {
		t.Fatal("missing console")
	}
	icon := ut.PerformRequest(h.Engine, "GET", "/xuandu.jpg", nil)
	if !strings.Contains(index.Body.String(), `href="/xuandu.jpg"`) || icon.Code != 200 || !strings.HasPrefix(icon.Header().Get("Content-Type"), "image/jpeg") {
		t.Fatal("missing brand favicon")
	}
	scripts := regexp.MustCompile(`src="(/[^" ]+\.js)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(scripts) == 0 {
		t.Fatal("no built scripts")
	}
	for _, script := range scripts {
		asset := ut.PerformRequest(h.Engine, "GET", script[1], nil)
		if asset.Code != 200 || asset.Body.Len() == 0 {
			t.Fatalf("missing asset %s", script[1])
		}
	}
	for _, path := range []string{"/missing.js", "/admin/unknown", "/package.json"} {
		if r := ut.PerformRequest(h.Engine, "GET", path, nil); r.Code != 404 {
			t.Fatalf("%s: %d", path, r.Code)
		}
	}
}
