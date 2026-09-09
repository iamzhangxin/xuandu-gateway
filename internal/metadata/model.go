package metadata

import (
	"context"
	"errors"
	"fmt"
	"github.com/iamzhangxin/xuandu-gateway/internal/archiveidl"
	"net/url"

	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("app not found")
var ErrConflict = errors.New("metadata conflict")
var NamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var RevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type IDLSource struct {
	Type             string `json:"type"`
	URL              string `json:"-"`
	ResolvedRevision string `json:"resolvedRevision"`
}
type App struct {
	CatalogIndex uint64    `json:"-"`
	Name         string    `json:"name"`
	Domain       string    `json:"domain"`
	ServiceName  string    `json:"serviceName"`
	Enabled      bool      `json:"enabled"`
	RPCTimeout   string    `json:"rpcTimeout"`
	IDL          IDLSource `json:"idl"`
	ModifyIndex  uint64    `json:"-"`
}

func (a *App) Validate() error {
	if _, e := NormalizeDomain(a.Domain); e != nil {
		return e
	}
	return a.validateStored()
}

// Existing catalogs without domains remain editable, but cannot build a runtime.
func (a *App) validateStored() error {
	if a.Domain != "" {
		if _, e := NormalizeDomain(a.Domain); e != nil {
			return e
		}
	}

	if !NamePattern.MatchString(a.Name) || strings.TrimSpace(a.ServiceName) == "" {
		return fmt.Errorf("invalid application or service name")
	}
	if d, e := time.ParseDuration(a.RPCTimeout); e != nil || d <= 0 {
		return fmt.Errorf("rpcTimeout must be a positive duration")
	}
	if a.IDL.Type != "zip" {
		return fmt.Errorf("IDL source must be zip")
	}
	if a.IDL.URL != "" {
		return ValidateDownloadURL(a.IDL.URL)
	}
	if !RevisionPattern.MatchString(a.IDL.ResolvedRevision) {
		return fmt.Errorf("persisted contract revision required; import a ZIP link")
	}

	return nil
}

func ValidateDownloadURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("IDL URL must be an HTTP(S) download link without userinfo or fragment")
	}
	return nil
}

type Store interface {
	PutContract(context.Context, *archiveidl.Revision) error
	GetContract(context.Context, string) (*archiveidl.Revision, error)

	Get(context.Context, string) (*App, error)
	List(context.Context) ([]*App, error)
	Create(context.Context, *App) error
	CompareAndSwap(context.Context, *App, uint64) error
	Delete(context.Context, string, uint64) error
	Watch(context.Context, func([]*App)) error
}

// CatalogStore prevents concurrent writes to different apps from publishing
// application changes based on different replicas' stale snapshots.
type CatalogStore interface {
	Catalog(context.Context) ([]*App, uint64, error)
}
