package contracts

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/fault"
)

// ValidateGitURL keeps credentials out of JSON and accepts only transports
// supported by the host-side go-git adapter.
func ValidateGitURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fault.New(fault.CodeInvalidSpec, "Git URL must not contain credentials, query, or fragment")
	}
	if parsed.User != nil {
		_, hasPassword := parsed.User.Password()
		if parsed.Scheme != "ssh" || hasPassword || parsed.User.Username() == "" {
			return fault.New(fault.CodeInvalidSpec, "Git URL must not contain credentials")
		}
	}
	switch parsed.Scheme {
	case "https", "ssh":
		if parsed.Hostname() == "" || strings.Trim(parsed.Path, "/") == "" {
			return fault.New(fault.CodeInvalidSpec, "Git URL needs a host and repository path")
		}
	case "file":
		if parsed.Host != "" || !filepath.IsAbs(parsed.Path) {
			return fault.New(fault.CodeInvalidSpec, "file Git URL needs an absolute local path")
		}
	default:
		return fault.New(fault.CodeInvalidSpec, "Git URL must use https, ssh, or file")
	}
	return nil
}
