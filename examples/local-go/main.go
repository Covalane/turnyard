// Command local-go writes a valid starting point for a local Go repository.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/contracts"
	git "github.com/go-git/go-git/v5"
)

func main() {
	repoPath := flag.String("repo", "", "local Git repository root")
	output := flag.String("out", "", "directory for the generated JSON files")
	objective := flag.String("objective", "", "task objective")
	commitMessage := flag.String("commit-message", "", "Git commit subject, for example: feat: add health check")
	deliverable := flag.String("deliverable", "", "optional required file, relative to the repository root")
	backend := flag.String("backend", "docker", "sandbox backend")
	image := flag.String("image", "turnyard-agent:dev", "prebuilt agent image")
	runtime := flag.String("runtime", "opencode", "agent runtime")
	provider := flag.String("provider", "ollama-cloud", "model provider")
	model := flag.String("model", "glm-5.3-flash", "model name")
	credential := flag.String("credential", "MODEL_API_KEY", "host credential environment variable")
	flag.Parse()
	if *repoPath == "" || *output == "" || *objective == "" {
		fmt.Fprintln(os.Stderr, "required: --repo, --out, --objective")
		os.Exit(2)
	}
	if err := generate(*repoPath, *output, *objective, *deliverable, *backend, *image, *runtime, *provider, *model, *credential, *commitMessage); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(*output)
}

func generate(repoPath, output, objective, deliverable, backend, image, runtime, provider, model, credential, commitMessage string) error {
	if err := contracts.ValidateCommitMessage(commitMessage); err != nil {
		return err
	}
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return err
	}
	repo, err := git.PlainOpen(repoPath)
	if err != nil {
		return fmt.Errorf("open source Git repository: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return fmt.Errorf("read source HEAD: %w", err)
	}
	tree, err := repo.Worktree()
	if err != nil {
		return err
	}
	status, err := tree.Status()
	if err != nil {
		return err
	}
	if !status.IsClean() {
		return fmt.Errorf("source repository has uncommitted changes; commit them before generating pinned inputs")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(repoPath, output)
	if err != nil {
		return err
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("output directory must be outside the source repository")
	}
	for _, name := range []string{"session.json", "environment.json", "work.json"} {
		if _, err := os.Lstat(filepath.Join(output, name)); err == nil {
			return fmt.Errorf("%s already exists; choose a new output directory", name)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	session := contracts.SessionSpec{
		SchemaVersion: contracts.SessionVersion, IdempotencyKey: contracts.NewID("create"),
		Repositories: []contracts.RepositorySpec{{ID: "app", Type: contracts.RepositorySourceLocalGit, Path: repoPath, Commit: head.Hash().String()}},
		Environment:  "environment.json", PrimaryAgent: "lead",
	}
	env := contracts.EnvironmentSpec{
		SchemaVersion: contracts.EnvironmentVersion,
		Sandbox:       contracts.SandboxSpec{Backend: backend, Image: image, CPUs: 2, MemoryMB: 2048},
		Agents:        []contracts.AgentSpec{{ID: "lead", Runtime: runtime, ModelBinding: "primary"}},
		ModelBindings: []contracts.ModelBinding{{
			ID: "primary", Provider: provider, Model: model, CredentialEnv: credential,
		}},
		Git:    contracts.GitPolicy{LocalCommits: true, RemoteWrites: "none"},
		Checks: []contracts.CheckSpec{{ID: "go-test", Argv: []string{"go", "-C", "/workspace/app", "test", "./..."}, Repositories: []string{"app"}, TimeoutSeconds: 120}},
	}
	work := contracts.WorkSpec{
		SchemaVersion: contracts.WorkVersion, IdempotencyKey: contracts.NewID("work"),
		Objective: objective, CommitMessage: commitMessage, Acceptance: []string{"实现目标并通过 Go 测试"},
		Checks: []string{"go-test"},
	}
	work.Scope.Repositories = []contracts.ScopeRepo{{ID: "app", Mode: contracts.ScopeWrite}}
	if deliverable != "" {
		work.Deliverables = []contracts.DeliverableSpec{{ID: "output", Repository: "app", Path: deliverable, Kind: contracts.DeliverableFile}}
	}
	if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	for name, value := range map[string]any{"session.json": session, "environment.json": env, "work.json": work} {
		body, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(output, name), append(body, '\n'), 0o600); err != nil {
			return err
		}
	}
	return nil
}
