package metadata

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

var ErrInvalidDomain = errors.New("domain must be a hostname or IP without scheme, port or path")
var ErrDomainConflict = errors.New("domain already belongs to another application")

// NormalizeDomain accepts a DNS name or IP, without scheme, port or path.
func NormalizeDomain(value string) (string, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if host == "" || len(host) > 253 {
		return "", ErrInvalidDomain
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidDomain
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return "", ErrInvalidDomain
			}
		}
	}
	return host, nil
}

// RequestDomain uses the actual HTTP authority; forwarded headers are not trusted.
func RequestDomain(authority string) (string, error) {
	if strings.TrimSpace(authority) != authority {
		return "", fmt.Errorf("invalid host")
	}
	if host, port, err := net.SplitHostPort(authority); err == nil {
		if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("invalid host port")
		}
		authority = host
	} else if strings.HasPrefix(authority, "[") && strings.HasSuffix(authority, "]") {
		authority = authority[1 : len(authority)-1]
	}
	return NormalizeDomain(authority)
}
