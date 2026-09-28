package contracts

import "net/url"

// ValidModelEndpoint accepts an explicit HTTPS API base without inline
// credentials or request-specific query parameters.
func ValidModelEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		u.RawQuery == "" && u.Fragment == "" && u.Opaque == ""
}
