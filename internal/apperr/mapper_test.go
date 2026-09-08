package apperr

import (
	"context"
	"testing"

	"github.com/cloudwego/kitex/pkg/kerrors"
)

func TestMap(t *testing.T) {
	for e, want := range map[error]int{context.DeadlineExceeded: 504, kerrors.ErrRPCTimeout: 504, kerrors.ErrNoInstance: 503, kerrors.ErrLoadbalance: 503, kerrors.ErrGetConnection: 502, kerrors.ErrPayloadValidation: 400, kerrors.ErrInternalException: 500} {
		got, _, _ := Map(e)
		if got != want {
			t.Fatalf("%v: %d", e, got)
		}
	}
}
