package openapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateCommentsAndMapping(t *testing.T) {
	files := map[string]string{
		"idl/common.thrift": `// 节点
struct Node {
 // 节点编号
 1: required i64 id (api.js_conv="true", go.tag='json:"nodeId"')
 2: optional list<Node> children
}
// 状态
enum State { // 开启
 ON=1, OFF=2 }
typedef Node Alias`,
		"idl/product.thrift": `include "common.thrift"
struct GetReq {
 // 商品编号
 1: required string spu_id (api.query="spuId")
 2: optional string category (api.path="category")
}
/** 商品详情 */
struct GetRes {1: common.Alias item}
// 商品服务
service Product {
 /** 查询商品
  * 返回商品详情
  */
 GetRes Get(1:GetReq req)(api.get="/product/:category")
}`,
		"idl/other.thrift": `struct Q {1: string name(api.body="name")} service Other { Q Get(1:Q req)(api.post="/other") }`,
	}
	doc, err := Generate("product", "revision", files)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	for _, want := range []string{"3.0.3", "查询商品", "返回商品详情", "商品编号", "nodeId", "000000", "X-User-ID", "/product/{category}", "/other"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s", want)
		}
	}
	op := doc["paths"].(Object)["/product/{category}"].(Object)["get"].(Object)
	if _, ok := op["requestBody"]; ok {
		t.Fatal("query fields incorrectly emitted as body")
	}
	params := op["parameters"].([]Object)
	if params[0]["in"] != "query" || params[0]["name"] != "spuId" || params[0]["required"] != true {
		t.Fatal(params)
	}
	if params[1]["in"] != "path" || params[1]["required"] != true {
		t.Fatal(params)
	}
	schemas := doc["components"].(Object)["schemas"].(Object)
	// Every generated reference must resolve, including recursive includes/typedefs.
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				if _, ok := schemas[strings.TrimPrefix(ref, "#/components/schemas/")]; !ok {
					t.Fatal("dangling reference", ref)
				}
			}
			for _, value := range v {
				walk(value)
			}
		case []any:
			for _, value := range v {
				walk(value)
			}
		}
	}
	var decoded any
	json.Unmarshal(raw, &decoded)
	walk(decoded)
}
func TestGetBodyWarning(t *testing.T) {
	doc, err := Generate("p", "r", map[string]string{"idl/p.thrift": `struct Q {1: required string id(api.body="id")} service S {Q Get(1:Q req)(api.get="/p")}`})
	if err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(Object)["/p"].(Object)["get"].(Object)
	if !strings.Contains(op["description"].(string), "api.query") || op["requestBody"].(Object)["required"] != true {
		t.Fatal(op)
	}
}

func TestLoginMetadata(t *testing.T) {
	doc, err := Generate("p", "revision", map[string]string{"idl/main.thrift": `struct Q {1:string user(api.header="X-User-ID")} service S {Q Get(1:Q req)(api.get="/p",xuandu.Auth="required")}`})
	if err != nil {
		t.Fatal(err)
	}
	op := doc["paths"].(Object)["/p"].(Object)["get"].(Object)
	if op["x-xuandu-auth"] != "required" || op["responses"].(Object)["401"] == nil {
		t.Fatal("missing login policy")
	}
	found := false
	for _, p := range op["parameters"].([]Object) {
		if p["name"] == "X-User-ID" {
			found = true
			if p["required"] != true {
				t.Fatal("identity not required")
			}
		}
	}
	if !found {
		t.Fatal("missing identity header")
	}
}
