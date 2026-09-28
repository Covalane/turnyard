package gitstate

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

// AuthForURL uses the host's existing Git credential helper or SSH agent.
// Tokens never enter Session JSON, the sandbox, or operational logs.
func AuthForURL(ctx context.Context, raw string) (transport.AuthMethod, error) {
	if err := contracts.ValidateGitURL(raw); err != nil {
		return nil, err
	}
	parsed, _ := url.Parse(raw)
	switch parsed.Scheme {
	case "file":
		return nil, nil
	case "ssh":
		user := "git"
		if parsed.User != nil {
			user = parsed.User.Username()
		}
		auth, err := gitssh.NewSSHAgentAuth(user)
		if err != nil {
			return nil, fault.Wrap(fault.CodeAuthUnavailable, "Git SSH agent", err, "SSH agent is unavailable")
		}
		return auth, nil
	case "https":
		command := exec.CommandContext(ctx, "git", "credential", "fill")
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		command.Stdin = strings.NewReader("protocol=https\nhost=" + parsed.Host + "\npath=" + strings.TrimPrefix(parsed.Path, "/") + "\n\n")
		output, err := command.Output()
		if err != nil {
			// Anonymous HTTPS remains valid for public repositories.
			return nil, nil
		}
		values := map[string]string{}
		for _, line := range strings.Split(string(output), "\n") {
			key, value, found := strings.Cut(line, "=")
			if found {
				values[key] = value
			}
		}
		if values["password"] == "" {
			return nil, nil
		}
		user := values["username"]
		if user == "" {
			user = "git"
		}
		return &githttp.BasicAuth{Username: user, Password: values["password"]}, nil
	}
	return nil, fault.New(fault.CodeInvalidSpec, "unsupported Git transport")
}

func remoteErrorCode(err error) fault.Code {
	if errors.Is(err, transport.ErrAuthenticationRequired) || errors.Is(err, transport.ErrAuthorizationFailed) {
		return fault.CodeAuthUnavailable
	}
	return fault.CodeGitError
}
