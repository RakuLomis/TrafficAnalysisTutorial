package transport

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var ErrOriginNotAllowed = errors.New("origin not allowed")

func CheckOrigin(r *http.Request) error {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" || r.Host == "" {
		return ErrOriginNotAllowed
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return ErrOriginNotAllowed
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrOriginNotAllowed
	}
	if !strings.EqualFold(parsed.Host, r.Host) {
		return ErrOriginNotAllowed
	}
	return nil
}
