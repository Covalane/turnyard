package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

// buildCommand applies the common mount and credential policy before any OCI
// process starts. Dialects supply only backend-specific run options.
func (b OCIBackend) buildCommand(input SandboxRun, extraOptions []string) (*exec.Cmd, int, int, error) {
	if err := os.MkdirAll(input.State, 0o700); err != nil {
		return nil, 0, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(input.LogPath), 0o700); err != nil {
		return nil, 0, 0, err
	}
	cpus, memory := EffectiveResources(input.Sandbox)
	args := []string{"run", "--rm", "--name", input.Name, "--cpus", fmt.Sprint(cpus),
		"--memory", fmt.Sprintf("%dm", memory), "--read-only", "--tmpfs", "/tmp"}
	// Bind mounts are owned by the supervisor account. Match that identity in
	// OCI runtimes so private state stays writable without broad host ACLs.
	args = append(args, b.dialect.UserOptions(os.Getuid(), os.Getgid())...)
	args = append(args, extraOptions...)
	args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/state", input.State))
	if input.CatalogDir != "" {
		if info, err := os.Lstat(input.CatalogDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, 0, fault.New(fault.CodeInvalidSpec, "tool catalog directory is unavailable")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/turnyard-catalog", input.CatalogDir))
	}
	if input.ArtifactDir != "" {
		if err := os.MkdirAll(input.ArtifactDir, 0o700); err != nil {
			return nil, 0, 0, err
		}
		mount := fmt.Sprintf("type=bind,source=%s,target=/workspace/.turnyard-output", input.ArtifactDir)
		if input.ArtifactReadOnly {
			mount += ",readonly"
		}
		args = append(args, "--mount", mount)
	}
	if input.InputDir != "" {
		info, err := os.Stat(input.InputDir)
		if err != nil || !info.IsDir() {
			return nil, 0, 0, fault.New(fault.CodeInvalidSpec, "staged input directory is unavailable")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/.turnyard-input,readonly", input.InputDir))
	}
	if input.ControlDir != "" {
		info, err := os.Lstat(input.ControlDir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, 0, 0, fault.New(fault.CodeInvalidSpec, "delegation control directory is unavailable")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/turnyard-control,readonly", input.ControlDir))
	}
	for _, item := range input.Scope {
		repoPath := filepath.Join(input.Workspace, item.ID)
		mount := fmt.Sprintf("type=bind,source=%s,target=/workspace/%s", repoPath, item.ID)
		if item.Mode == contracts.ScopeRead {
			mount += ",readonly"
		}
		args = append(args, "--mount", mount)
		if item.Mode == contracts.ScopeWrite {
			gitPath := filepath.Join(repoPath, ".git")
			if info, err := os.Stat(gitPath); err != nil || !info.IsDir() {
				return nil, 0, 0, fault.New(fault.CodeInvalidRepository, "%s has no Git metadata directory", item.ID)
			}
			args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/%s/.git,readonly", gitPath, item.ID))
		}
	}
	for _, name := range sortedKeys(input.Credentials) {
		if !validCredentialName(name) {
			return nil, 0, 0, fault.New(fault.CodeInvalidSpec, "invalid credential environment variable")
		}
		args = append(args, "--env", name)
	}
	for _, name := range sortedKeys(input.Environment) {
		if !validCredentialName(name) {
			return nil, 0, 0, fault.New(fault.CodeInvalidSpec, "invalid environment variable")
		}
		args = append(args, "--env", name)
	}
	args = append(args, "--env", "PWD=/workspace", "--env", "HOME=/state/home")
	if input.EntryPoint != "" {
		args = append(args, "--entrypoint", input.EntryPoint)
	}
	args = append(args, "--workdir", "/workspace", input.Sandbox.Image)
	args = append(args, input.Command...)
	cmd := exec.Command(b.dialect.Binary(), args...)
	cmd.Env = os.Environ()
	for _, name := range sortedKeys(input.Credentials) {
		cmd.Env = append(cmd.Env, name+"="+input.Credentials[name])
	}
	for _, name := range sortedKeys(input.Environment) {
		cmd.Env = append(cmd.Env, name+"="+input.Environment[name])
	}
	return cmd, cpus, memory, nil
}
