package idl

import (
	"testing"
)

func TestExtractRoutes(t *testing.T) {
	for _, tc := range []struct {
		body string
		n    int
		bad  bool
	}{{`struct Q {1:string id} service S {Q Get(1:Q req)(api.get="/p/:id") Q Post(1:Q req)(api.post="/p") void Private()}`, 2, false}, {`service S {void A()(api.get="/p") void B()(api.get="/p")}`, 0, true}, {`service S {void A()(api.get="p")}`, 0, true}, {`service S {void A()(api.patch="/p")}`, 0, true}, {`service S {void A()(api.get="/healthz")}`, 0, true}} {
		b := &Bundle{"main.thrift", map[string]string{"main.thrift": tc.body}}
		r, e := ExtractRoutes(b)
		if (e != nil) != tc.bad || (!tc.bad && len(r) != tc.n) {
			t.Fatalf("%s: %v %v", tc.body, r, e)
		}
	}
}

func TestExposedScalarRejected(t *testing.T) {
	_, e := ExtractRoutes(&Bundle{MainPath: "main.thrift", Files: map[string]string{"main.thrift": `struct R {1:string value} service S {R Get(1:string id)(api.get="/p")}`}})
	if e == nil {
		t.Fatal("scalar HTTP argument accepted")
	}
}
func TestIncludeStructAndAlias(t *testing.T) {
	files := map[string]string{"idl/common.thrift": `struct Request {1:string name} typedef Request Alias`, "idl/main.thrift": `include "common.thrift" service S {common.Request Get(1:common.Alias req)(api.get="/p")}`}
	_, routes, e := LoadArchive(files)
	if e != nil || len(routes) != 1 {
		t.Fatal(routes, e)
	}
}

func TestLoginAnnotation(t *testing.T) {
	for _, tc := range []struct {
		annotation    string
		required, bad bool
	}{
		{``, false, false}, {`,xuandu.Auth="required"`, true, false}, {`,xuandu.Auth='required'`, true, false}, {`,xuandu.Auth="optional"`, false, false},
		{`,xuandu.Auth="true"`, false, true}, {`,xuandu.Auth=""`, false, true}, {`,xuandu.Auth="required",xuandu.Auth="optional"`, false, true},
	} {
		_, routes, err := LoadArchive(map[string]string{"idl/main.thrift": `struct Q{} service S {Q Get(1:Q req)(api.get="/p"` + tc.annotation + `)}`})
		if (err != nil) != tc.bad {
			t.Fatalf("%s: %v", tc.annotation, err)
		}
		if err == nil && routes[0].RequireLogin != tc.required {
			t.Fatal("login requirement lost")
		}
	}
}
