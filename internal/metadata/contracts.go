package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	consul "github.com/hashicorp/consul/api"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
)

const contractChunkSize = 256 << 10

func (s *ConsulStore) contractKey(digest string) (string, error) {
	if !RevisionPattern.MatchString(digest) {
		return "", fmt.Errorf("invalid contract revision")
	}
	return strings.TrimSuffix(s.prefix, "/") + "-contracts/" + digest + "/", nil
}

// Immutable chunks are written before the completion marker. App publication
// happens only after this succeeds; partial uploads cannot become active.
func (s *ConsulStore) PutContract(ctx context.Context, r *archiveidl.Revision) error {
	key, err := s.contractKey(r.Digest)
	if err != nil {
		return err
	}
	if len(r.Archive) == 0 || len(r.Archive) > archiveidl.MaxArchiveBytes || fmt.Sprintf("%x", sha256.Sum256(r.Archive)) != r.Digest {
		return fmt.Errorf("invalid contract archive digest")
	}
	for i, offset := 0, 0; offset < len(r.Archive); i, offset = i+1, offset+contractChunkSize {
		end := min(offset+contractChunkSize, len(r.Archive))
		if err := s.putImmutable(ctx, key+fmt.Sprintf("%06d", i), r.Archive[offset:end]); err != nil {
			return err
		}
	}
	return s.putImmutable(ctx, key+"complete", []byte(strconv.Itoa(len(r.Archive))))
}
func (s *ConsulStore) putImmutable(ctx context.Context, key string, data []byte) error {
	ok, _, err := s.client.KV().CAS(&consul.KVPair{Key: key, Value: data}, (&consul.WriteOptions{}).WithContext(ctx))
	if err != nil {
		return fmt.Errorf("contract persistence failed")
	}
	if ok {
		return nil
	}
	pair, _, err := s.client.KV().Get(key, (&consul.QueryOptions{RequireConsistent: true}).WithContext(ctx))
	if err != nil || pair == nil || !bytes.Equal(pair.Value, data) {
		return fmt.Errorf("persisted contract content conflict")
	}
	return nil
}
func (s *ConsulStore) GetContract(ctx context.Context, digest string) (*archiveidl.Revision, error) {
	key, err := s.contractKey(digest)
	if err != nil {
		return nil, err
	}
	get := func(k string) ([]byte, error) {
		pair, _, err := s.client.KV().Get(k, (&consul.QueryOptions{RequireConsistent: true}).WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("contract storage unavailable")
		}
		if pair == nil {
			return nil, fmt.Errorf("persisted contract missing; import a new ZIP link")
		}
		return pair.Value, nil
	}
	marker, err := get(key + "complete")
	if err != nil {
		return nil, err
	}
	size, err := strconv.Atoi(string(marker))
	if err != nil || size <= 0 || size > archiveidl.MaxArchiveBytes {
		return nil, fmt.Errorf("invalid contract size")
	}
	data := make([]byte, 0, size)
	for i := 0; len(data) < size; i++ {
		part, err := get(key + fmt.Sprintf("%06d", i))
		if err != nil {
			return nil, err
		}
		if len(part) != min(contractChunkSize, size-len(data)) {
			return nil, fmt.Errorf("invalid contract chunk")
		}
		data = append(data, part...)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return nil, fmt.Errorf("persisted contract digest mismatch")
	}
	files, err := archiveidl.Unpack(data)
	if err != nil {
		return nil, err
	}
	return &archiveidl.Revision{Digest: digest, Archive: data, Files: files}, nil
}
