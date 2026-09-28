package contracts

import (
	"net/url"
	"path"
	"strings"
)

// ValidConnectorPrefix requires a directory-like URI under one exact
// authority. Ambiguous encoded paths are rejected rather than interpreted by
// each CLI differently.
func ValidConnectorPrefix(raw string) bool {
	u, ok := parseConnectorURI(raw)
	if !ok || !strings.HasSuffix(u.Path, "/") {
		return false
	}
	if u.Path == "/" {
		return true
	}
	return path.Clean(u.Path)+"/" == u.Path
}

// ValidConnectorURI enforces the trusted prefix on path-segment boundaries.
// It is used both for Work validation and immediately before connector exec.
func ValidConnectorURI(spec ArtifactConnectorSpec, raw string) bool {
	if !ValidConnectorPrefix(spec.URIPrefix) {
		return false
	}
	prefix, _ := url.Parse(spec.URIPrefix)
	u, ok := parseConnectorURI(raw)
	if !ok || u.Scheme != prefix.Scheme || u.Host != prefix.Host ||
		!strings.HasPrefix(u.Path, prefix.Path) || len(u.Path) <= len(prefix.Path) ||
		strings.HasSuffix(u.Path, "/") {
		return false
	}
	return path.Clean(u.Path) == u.Path
}

func parseConnectorURI(raw string) (*url.URL, bool) {
	if strings.ContainsAny(raw, "%\\?#\x00\r\n ") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.RawQuery != "" || u.Fragment != "" ||
		!strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "//") {
		return nil, false
	}
	return u, true
}
