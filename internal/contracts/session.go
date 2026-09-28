package contracts

import (
	"os"
	"path/filepath"
	"strings"

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
	}
	if err := uniqueIDs(ids, "artifact connector"); err != nil {
		return session, env, err
	}
	ids = ids[:0]
	models := map[string]bool{}
	agents := map[string]bool{}
	skills := map[string]bool{}
	tools := map[string]bool{}
	for _, m := range env.ModelBindings {
		ids = append(ids, m.ID)
		models[m.ID] = true
		for _, endpoint := range []string{m.Endpoints.OpenAIChat, m.Endpoints.Anthropic, m.Endpoints.Responses} {
			if endpoint != "" && !ValidModelEndpoint(endpoint) {
				return session, env, fault.New(fault.CodeInvalidSpec, "model binding %s has an invalid HTTPS endpoint", m.ID)
			}
		}
	}
	if err := uniqueIDs(ids, "model binding"); err != nil {
		return session, env, err
	}
	if search := env.ToolSearch; search != nil {
		if search.Mode == ToolSearchLLMRerank && !models[search.ModelBinding] {
			return session, env, fault.New(fault.CodeInvalidSpec, "tool search references missing model binding")
		}
		if search.Mode == ToolSearchLexical && search.ModelBinding != "" {
			return session, env, fault.New(fault.CodeInvalidSpec, "lexical tool search does not use a model binding")
		}
	}
	ids = ids[:0]
	for _, a := range env.Agents {
		ids = append(ids, a.ID)
		agents[a.ID] = true
	}
	if err := uniqueIDs(ids, "agent"); err != nil {
		return session, env, err
	}
	if !agents[session.PrimaryAgent] {
		return session, env, fault.New(fault.CodeInvalidSpec, "primaryAgent does not exist")
	}
	for _, a := range env.Agents {
		if !models[a.ModelBinding] {
			return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references missing model", a.ID)
		}
		for _, child := range a.Delegates {
			if child == a.ID || !agents[child] {
				return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references invalid delegate %s", a.ID, child)
			}
		}
	}
	ids = ids[:0]
	repositoryIDs := map[string]bool{}
	for _, repository := range session.Repositories {
		repositoryIDs[repository.ID] = true
	}
	for _, c := range env.Checks {
		ids = append(ids, c.ID)
		for _, repositoryID := range c.Repositories {
			if !repositoryIDs[repositoryID] {
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
		skills[s.ID] = true
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
		tools[t.ID] = true
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
			if !skills[id] {
				return session, env, fault.New(fault.CodeInvalidSpec, "agent %s references missing skill %s", a.ID, id)
			}
		}
		for _, id := range a.Tools {
			if !tools[id] {
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

func validConnectorCommand(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	joined := strings.Join(argv, "\x00")
	return strings.Count(joined, "{uri}") == 1 && strings.Count(joined, "{file}") == 1
}
