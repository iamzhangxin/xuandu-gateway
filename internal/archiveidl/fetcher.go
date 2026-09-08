package archiveidl

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const MaxArchiveBytes = 32 << 20
const MaxExpandedBytes = 64 << 20
const MaxFileBytes = 8 << 20
const MaxFiles = 1024

type Source struct {
	URL            string
	ExpectedDigest string
}
type Revision struct {
	Archive []byte `json:"-"`
	Digest  string
	Files   map[string]string
}
type Fetcher interface {
	Resolve(context.Context, Source) (*Revision, error)
}
type HTTPFetcher struct{ client *http.Client }

func NewFetcher() *HTTPFetcher { return &HTTPFetcher{client: &http.Client{Timeout: 60 * time.Second}} }
func (f *HTTPFetcher) Resolve(ctx context.Context, s Source) (*Revision, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if e != nil {
		return nil, fmt.Errorf("invalid download URL")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, fmt.Errorf("HTTP(S) download required")
	}
	resp, e := f.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("IDL download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("IDL download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxArchiveBytes {
		return nil, fmt.Errorf("ZIP download exceeds size limit")
	}
	data, e := io.ReadAll(io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if e != nil {
		return nil, fmt.Errorf("IDL download interrupted")
	}
	if len(data) > MaxArchiveBytes {
		return nil, fmt.Errorf("ZIP download exceeds size limit")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if s.ExpectedDigest != "" && digest != s.ExpectedDigest {
		return nil, fmt.Errorf("ZIP digest differs from published revision")
	}
	files, e := Unpack(data)
	if e != nil {
		return nil, e
	}
	return &Revision{Digest: digest, Files: files, Archive: data}, nil
}

// Unpack validates every entry before exposing any content. Files stay in memory;
// no untrusted ZIP path is ever written to the filesystem.
func Unpack(data []byte) (map[string]string, error) {
	if len(data) > MaxArchiveBytes || len(data) < 4 || !bytes.Equal(data[:4], []byte{'P', 'K', 3, 4}) {
		return nil, fmt.Errorf("download is not a ZIP archive")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, fmt.Errorf("download is not a valid ZIP archive")
	}
	if len(z.File) > MaxFiles {
		return nil, fmt.Errorf("too many ZIP entries")
	}
	files := map[string]string{}
	seen := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		name := f.Name
		clean := strings.TrimSuffix(name, "/")
		if clean == "" || path.Clean(clean) != clean || strings.ContainsAny(clean, "\\\x00") || path.IsAbs(clean) || (clean != "idl" && !strings.HasPrefix(clean, "idl/")) {
			return nil, fmt.Errorf("ZIP entries must remain under idl/")
		}
		if seen[clean] {
			return nil, fmt.Errorf("duplicate ZIP entry")
		}
		seen[clean] = true
		if f.Mode()&(fs.ModeType&^fs.ModeDir) != 0 {
			return nil, fmt.Errorf("special ZIP entries are forbidden")
		}
		if f.FileInfo().IsDir() {
			if !strings.HasSuffix(name, "/") || f.UncompressedSize64 != 0 {
				return nil, fmt.Errorf("invalid ZIP directory")
			}
			continue
		}
		if !f.Mode().IsRegular() || !strings.HasPrefix(name, "idl/") || !strings.HasSuffix(name, ".thrift") {
			return nil, fmt.Errorf("ZIP files must match idl/*.thrift; special files are forbidden")
		}
		if f.UncompressedSize64 > MaxFileBytes || f.UncompressedSize64 > MaxExpandedBytes-total {
			return nil, fmt.Errorf("expanded ZIP exceeds size limit")
		}
		total += f.UncompressedSize64
		r, e := f.Open()
		if e != nil {
			return nil, fmt.Errorf("cannot read ZIP entry")
		}
		b, e := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
		r.Close()
		if e != nil || len(b) > MaxFileBytes || uint64(len(b)) != f.UncompressedSize64 {
			return nil, fmt.Errorf("corrupt or oversized ZIP entry")
		}
		files[name] = string(b)
	}
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if _, ok := files[parent]; ok {
				return nil, fmt.Errorf("ZIP file and directory path conflict")
			}
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("ZIP contains no idl/*.thrift files")
	}
	return files, nil
}
