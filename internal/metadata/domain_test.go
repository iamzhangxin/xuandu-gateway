package metadata

import (
	"encoding/json"
	consul "github.com/hashicorp/consul/api"
	"strings"
	"testing"
)

func TestDomainNormalization(t *testing.T) {
	for input, want := range map[string]string{"Product.Example.": "product.example", " localhost ": "localhost", "127.0.0.1": "127.0.0.1", "::1": "::1"} {
		got, err := NormalizeDomain(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"", "https://product.example", "product.example:8080", "*.example", "x/y", "x?y", "x#y", "x@y", "x..y", "-x.example", "x_.example", "x\ny"} {
		if _, err := NormalizeDomain(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for input, want := range map[string]string{"Product.Example.:8080": "product.example", "127.0.0.1:8081": "127.0.0.1", "[::1]:8081": "::1", "[::1]": "::1"} {
		got, err := RequestDomain(input)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"x:bad", "x:", "x:99999", "x:0", " x"} {
		if _, err := RequestDomain(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestLegacyDomainRemainsEditable(t *testing.T) {
	a := App{Name: "old", ServiceName: "old", RPCTimeout: "1s", IDL: IDLSource{Type: "zip", ResolvedRevision: strings.Repeat("a", 64)}}
	data, _ := json.Marshal(a)
	decoded, err := decode(&consul.KVPair{Value: data, ModifyIndex: 5})
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Validate() == nil {
		t.Fatal("domain-less application may not publish")
	}
	decoded.Domain = "old.example"
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
}
