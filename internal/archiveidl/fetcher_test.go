package archiveidl

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func archive(t *testing.T, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, n := range names {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		f.Write([]byte("struct Q {}"))
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestDownload(t *testing.T) {
	data := archive(t, "idl/a.thrift", "idl/model/b.thrift")
	status := 200
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write(data) }))
	defer ts.Close()
	f := NewFetcher()
	r, e := f.Resolve(context.Background(), Source{URL: ts.URL})
	if e != nil || len(r.Files) != 2 || len(r.Digest) != 64 {
		t.Fatal(r, e)
	}
	if _, e = f.Resolve(context.Background(), Source{URL: ts.URL, ExpectedDigest: r.Digest}); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Resolve(context.Background(), Source{URL: ts.URL, ExpectedDigest: strings.Repeat("0", 64)}); e == nil {
		t.Fatal("digest mismatch accepted")
	}
	status = 404
	if _, e = f.Resolve(context.Background(), Source{URL: ts.URL}); e == nil {
		t.Fatal("failed download accepted")
	}
	status = 200
	data = []byte("<html>login</html>")
	if _, e = f.Resolve(context.Background(), Source{URL: ts.URL}); e == nil {
		t.Fatal("non ZIP accepted")
	}
	ts.Close()
	if _, e = f.Resolve(context.Background(), Source{URL: ts.URL}); e == nil {
		t.Fatal("connection failure accepted")
	}
}
func TestArchiveRejectsUnexpectedEntries(t *testing.T) {
	for _, names := range [][]string{{}, {"main.thrift"}, {"repo/idl/a.thrift"}, {"idl/a.thrift", "README.md"}, {"idl/../secret.thrift"}, {"idl/../../secret.thrift"}, {"idl/a.txt"}, {"idl/a.thrift", "idl/a.thrift"}, {"/idl/a.thrift"}, {`idl\a.thrift`}, {"idl/a.thrift/child.thrift", "idl/a.thrift"}} {
		data := archive(t, names...)
		_, e := Unpack(data)
		if e == nil {
			t.Errorf("accepted %v", names)
		}
	}
}
func TestArchiveSymlinkAndCorruption(t *testing.T) {
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	header := &zip.FileHeader{Name: "idl/a.thrift"}
	header.SetMode(os.ModeSymlink | 0777)
	f, _ := w.CreateHeader(header)
	f.Write([]byte("outside"))
	w.Close()
	if _, e := Unpack(b.Bytes()); e == nil {
		t.Fatal("symlink accepted")
	}
	data := archive(t, "idl/a.thrift")
	if _, e := Unpack(data[:len(data)/2]); e == nil {
		t.Fatal("truncated ZIP accepted")
	}
}
