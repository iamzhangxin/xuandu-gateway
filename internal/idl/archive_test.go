package idl

import (
	"testing"
)

func TestArchiveLoadsAllIDLs(t *testing.T) {
	files := map[string]string{"idl/common.thrift": `struct Q {1:string name}`, "idl/a.thrift": `include "common.thrift" service A {common.Q Get(1:common.Q req)(api.get="/a")}`, "idl/b.thrift": `include "common.thrift" service B {common.Q Get(1:common.Q req)(api.get="/b")}`}
	bundles, routes, e := LoadArchive(files)
	if e != nil || len(bundles) != 2 || len(routes) != 2 {
		t.Fatal(bundles, routes, e)
	}
	files["idl/unused.thrift"] = "bad !"
	if _, _, e = LoadArchive(files); e == nil {
		t.Fatal("invalid unused IDL accepted")
	}
	delete(files, "idl/unused.thrift")
	delete(files, "idl/common.thrift")
	if _, _, e = LoadArchive(files); e == nil {
		t.Fatal("missing include accepted")
	}
}
func TestArchiveIncludeEscape(t *testing.T) {
	for _, include := range []string{"../../outside.thrift", "/idl/common.thrift"} {
		files := map[string]string{"idl/a.thrift": "include \"" + include + "\"\nstruct Q {}"}
		if _, _, e := LoadArchive(files); e == nil {
			t.Fatal("include escape accepted")
		}
	}
}
