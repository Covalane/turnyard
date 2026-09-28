package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
)

var githubSlug = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var githubNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// GitHubCLI verifies a PR through the authenticated official GitHub CLI on
// the supervisor host. It does not grant the sandbox any GitHub credentials.
// Other SCMs can replace it through PullRequestVerifier.
type GitHubCLI struct{}

func (GitHubCLI) ReadPullRequest(ctx context.Context, source contracts.RepositorySpec, rawURL string) (PullRequestSnapshot, error) {
	remote := source.PushURL
	if remote == "" {
		remote = source.URL
	}
	repository, err := githubRepository(remote)
	if err != nil {
		return PullRequestSnapshot{}, err
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return PullRequestSnapshot{}, fmt.Errorf("invalid GitHub pull request URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || !githubNumber.MatchString(parts[3]) || parts[0]+"/"+parts[1] != repository {
		return PullRequestSnapshot{}, fmt.Errorf("pull request does not belong to the configured repository")
	}
	canonical := "https://github.com/" + repository + "/pull/" + parts[3]
	if rawURL != canonical {
		return PullRequestSnapshot{}, fmt.Errorf("pull request URL is not canonical")
	}
	cmd := exec.CommandContext(ctx, "gh", "api", "repos/"+repository+"/pulls/"+parts[3])
	output, err := cmd.Output()
	if err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("GitHub pull request readback failed: %w", err)
	}
	var response struct {
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Base    struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(output, &response); err != nil {
		return PullRequestSnapshot{}, fmt.Errorf("decode GitHub pull request: %w", err)
	}
	return PullRequestSnapshot{URL: response.HTMLURL, Base: response.Base.Ref, Head: response.Head.SHA, Open: response.State == "open"}, nil
}

func githubRepository(remote string) (string, error) {
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = strings.TrimPrefix(remote, "git@github.com:")
	} else {
		u, err := url.Parse(remote)
		if err != nil {
			return "", fmt.Errorf("repository is not a GitHub remote: %w", err)
		}
		sshUser := u.Scheme == "ssh" && u.User != nil && u.User.String() == "git"
		if u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" ||
			(u.Scheme != "https" && u.Scheme != "ssh") ||
			(u.User != nil && !sshUser) {
			return "", fmt.Errorf("repository is not a GitHub remote")
		}
		remote = strings.TrimPrefix(u.Path, "/")
	}
	remote = strings.TrimSuffix(remote, ".git")
	if !githubSlug.MatchString(remote) {
		return "", fmt.Errorf("invalid GitHub repository path")
	}
	return remote, nil
}
