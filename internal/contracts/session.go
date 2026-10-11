package contracts

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/collections"
	"github.com/Covalane/turnyard/internal/fault"
)

func LoadSession(path string) (SessionSpec, EnvironmentSpec, error) {
	var session SessionSpec
	absolute, err := filepath.Abs(path)
	if err != nil {
		return session, EnvironmentSpec{}, err
	}
	if err := ReadJSON(absolute, "session", &session); err != nil {
		return session, EnvironmentSpec{}, err
	}
	ids := []string{}
	for _, repo := range session.Repositories {
		ids = append(ids, repo.ID)
	}
	if err := uniqueIDs(ids, "repository"); err != nil {
		return session, EnvironmentSpec{}, err
	}
	envPath, err := resolvePath(filepath.Dir(absolute), session.Environment)
	if err != nil {
		return session, EnvironmentSpec{}, err
	}
	var env EnvironmentSpec
	if err := ReadJSON(envPath, "environment", &env); err != nil {
		return session, env, err
	}
	// Git is an explicit delivery capability for repository sessions. A task
	// that only produces files or text does not need Git policy at all.
	if len(session.Repositories) > 0 && (!env.Git.LocalCommits || env.Git.RemoteWrites == "") {
		return session, env, fault.New(fault.CodeInvalidSpec, "repository sessions require an explicit Git policy")
	}
	if len(session.Repositories) == 0 && env.Git.RemoteWrites == GitRemoteWritesPush {
		return session, env, fault.New(fault.CodeInvalidSpec, "remote Git writes require a repository")
	}
	ids = ids[:0]
	for _, connector := range env.ArtifactConnectors {
		ids = append(ids, connector.ID)
		if !ValidConnectorPrefix(connector.URIPrefix) ||
			!validConnectorCommand(connector.GetArgv) || len(connector.PutArgv) > 0 && !validConnectorCommand(connector.PutArgv) {
			return session, env, fault.New(fault.CodeInvalidSpec, "artifact connector %s has invalid commands or URI prefix", connector.ID)
		}
		if err := ValidateConnectorDelivery(connector); err != nil {
			return session, env, err
		}
	}
	if err := uniqueIDs(ids, "artifact connector"); err != nil {
		return session, env, err
	}
	ids = ids[:0]
	agents := collections.Set[string]{}
	skills := collections.Set[string]{}
	tools := collections.Set[string]{}
	if err := ValidateModelReferences(env); err != nil {
		return session, env, err
	}
	ids = ids[:0]
	for _, a := range env.Agents {
		ids = append(ids, a.ID)
		agents.Add(a.ID)
	}
	if err := uniqueIDs(ids, "agent"); err != nil {
		return session, env, err
	}
	if !agents.Has(session.PrimaryAgent) {
		return session, env, fault.New(fault.CodeInvalidSpec, "primaryAgent does not exist")
	}
	for _, a := range env.Agents {
		for _, child := range a.Delegates {
			if child == a.ID || !agents.Has(child) {
				return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references invalid delegate %s", a.ID, child)
			}
		}
	}
	ids = ids[:0]
	repositoryIDs := collections.Set[string]{}
	for _, repository := range session.Repositories {
		repositoryIDs.Add(repository.ID)
	}
	for _, c := range env.Checks {
		ids = append(ids, c.ID)
		for _, repositoryID := range c.Repositories {
			if !repositoryIDs.Has(repositoryID) {
				return session, env, fault.New(fault.CodeInvalidSpec, "check %s references missing repository %s", c.ID, repositoryID)
			}
		}
	}
	if err := uniqueIDs(ids, "check"); err != nil {
		return session, env, err
	}
	ids = ids[:0]
	for i := range env.Skills {
		s := &env.Skills[i]
		ids = append(ids, s.ID)
		skills.Add(s.ID)
		s.Path, err = resolvePath(filepath.Dir(envPath), s.Path)
		if err != nil {
			return session, env, err
		}
		body, err := os.ReadFile(filepath.Join(s.Path, "SKILL.md"))
		if err != nil {
			return session, env, fault.New(fault.CodeInvalidSpec, "skill %s has no SKILL.md", s.ID)
		}
		if !strings.HasPrefix(string(body), "---\n") || !strings.Contains(string(body)[4:], "\n---") || !strings.Contains(string(body), "name: "+s.ID) || !strings.Contains(string(body), "description:") {
			return session, env, fault.New(fault.CodeInvalidSpec, "skill %s has invalid frontmatter", s.ID)
		}
		s.SourceDigest, err = bundleDigest(s.Path)
		if err != nil {
			return session, env, err
		}
	}
	if err := uniqueIDs(ids, "skill"); err != nil {
		return session, env, err
	}
	ids = ids[:0]
	for i := range env.Tools {
		t := &env.Tools[i]
		if t.ID == DelegationToolID || t.ID == HumanInputToolID {
			return session, env, fault.New(fault.CodeInvalidSpec, "tool ID %s is reserved", DelegationToolID)
		}
		ids = append(ids, t.ID)
		tools.Add(t.ID)
		if t.Kind != ToolKindMCP && t.Kind != ToolKindExecutable {
			return session, env, fault.New(fault.CodeInvalidSpec, "tool %s has unsupported kind %s", t.ID, t.Kind)
		}
		if t.Kind == ToolKindExecutable && t.Description == "" {
			return session, env, fault.New(fault.CodeInvalidSpec, "executable tool %s requires a description", t.ID)
		}
		if len(t.Argv) == 0 || t.Argv[0] == "" {
			return session, env, fault.New(fault.CodeInvalidSpec, "tool %s requires a command", t.ID)
		}
		if t.Path == "" {
			continue
		}
		t.Path, err = resolvePath(filepath.Dir(envPath), t.Path)
		if err != nil {
			return session, env, err
		}
		info, err := os.Stat(t.Path)
		if err != nil || !info.IsDir() {
			return session, env, fault.New(fault.CodeInvalidSpec, "tool %s path must be a directory", t.ID)
		}
		t.SourceDigest, err = bundleDigest(t.Path)
		if err != nil {
			return session, env, err
		}
	}
	if err := uniqueIDs(ids, "tool"); err != nil {
		return session, env, err
	}
	for _, a := range env.Agents {
		for _, id := range a.Skills {
			if !skills.Has(id) {
				return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references missing skill %s", a.ID, id)
			}
		}
		for _, id := range a.Tools {
			if !tools.Has(id) {
				return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references missing tool %s", a.ID, id)
			}
		}
	}
	for i := range session.Repositories {
		repository := &session.Repositories[i]
		switch repository.Type {
		case RepositorySourceLocalGit:
			if repository.URL != "" {
				return session, env, fault.New(fault.CodeInvalidSpec, "local repository %s cannot have a source URL", repository.ID)
			}
			repository.Path, err = resolvePath(filepath.Dir(absolute), repository.Path)
			if err != nil {
				return session, env, err
			}
		case RepositorySourceRemoteGit:
			if repository.Path != "" {
				return session, env, fault.New(fault.CodeInvalidSpec, "remote repository %s cannot have a local source path", repository.ID)
			}
			if err := ValidateGitURL(repository.URL); err != nil {
				return session, env, err
			}
		}
		if repository.PushURL != "" {
			if err := ValidateGitURL(repository.PushURL); err != nil {
				return session, env, err
			}
		}
	}
	return session, env, nil
}

func ValidateConnectorDelivery(spec ArtifactConnectorSpec) error {
	seen := collections.Set[int]{}
	for _, code := range spec.NotFoundExitCodes {
		if code < 1 || code > 255 || !seen.Add(code) {
			return fault.New(fault.CodeInvalidSpec, "connector %s has invalid not-found exit codes", spec.ID)
		}
	}
	if len(spec.PutArgv) > 0 && (spec.PutMode != ConnectorPutCreateOnly || len(spec.NotFoundExitCodes) == 0) {
		return fault.New(fault.CodeInvalidSpec, "connector %s delivery requires create-only writes and explicit not-found exit codes", spec.ID)
	}
	if len(spec.PutArgv) == 0 && spec.PutMode != "" {
		return fault.New(fault.CodeInvalidSpec, "connector %s declares a write mode without a write command", spec.ID)
	}
	return nil
}

// ValidateModelReferences also validates environments derived in memory, such
// as managed child sessions, before they are persisted or used by a driver.
func ValidateModelReferences(env EnvironmentSpec) error {
	models := collections.Set[string]{}
	for _, binding := range env.ModelBindings {
		if !models.Add(binding.ID) {
			return fault.New(fault.CodeInvalidSpec, "duplicate model binding %s", binding.ID)
		}
		for _, endpoint := range []string{binding.Endpoints.OpenAIChat, binding.Endpoints.Anthropic, binding.Endpoints.Responses} {
			if endpoint != "" && !ValidModelEndpoint(endpoint) {
				return fault.New(fault.CodeInvalidSpec, "model binding %s has an invalid HTTPS endpoint", binding.ID)
			}
		}
	}
	for _, agent := range env.Agents {
		if !models.Has(agent.ModelBinding) {
			return fault.New(fault.CodeInvalidSpec, "agent %s references missing model", agent.ID)
		}
	}
	if search := env.ToolSearch; search != nil {
		switch search.Mode {
		case ToolSearchLLMRerank:
			if !models.Has(search.ModelBinding) {
				return fault.New(fault.CodeInvalidSpec, "tool search references missing model binding")
			}
		case ToolSearchLexical:
			if search.ModelBinding != "" {
				return fault.New(fault.CodeInvalidSpec, "lexical tool search does not use a model binding")
			}
		default:
			return fault.New(fault.CodeInvalidSpec, "unknown tool search mode %s", search.Mode)
		}
	}
	return nil
}

func validConnectorCommand(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	joined := strings.Join(argv, "\x00")
	return strings.Count(joined, "{uri}") == 1 && strings.Count(joined, "{file}") == 1
}
