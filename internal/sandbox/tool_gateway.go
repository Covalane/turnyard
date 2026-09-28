package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
)

type toolGatewayLease struct {
	binary, name, network, address string
	ownsNetwork                    bool
	logPath                        string
	secrets                        map[string]string
}

var gatewayInvocationID = regexp.MustCompile(`^inv_[a-f0-9]{16}$`)

func (b OCIBackend) startToolGateway(ctx context.Context, input SandboxRun, model *modelGatewayLease) (*toolGatewayLease, error) {
	if b.Name() != BackendDocker || input.ToolGateway == nil {
		return nil, fault.New(fault.CodeCapabilityMissing, "tool gateway sidecar requires Docker")
	}
	if !strings.HasPrefix(input.Name, "ty-") || len(input.Name) > 80 {
		return nil, fault.New(fault.CodeInvalidSpec, "invalid tool gateway invocation name")
	}
	relative, err := filepath.Rel(input.State, input.ToolGateway.ConfigPath)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		return nil, fault.New(fault.CodeInvalidSpec, "tool gateway configuration is outside session state")
	}
	if info, err := os.Lstat(input.ToolGateway.ConfigPath); err != nil || !info.Mode().IsRegular() {
		return nil, fault.New(fault.CodeInvalidSpec, "tool gateway configuration is unavailable")
	}
	if info, err := os.Lstat(input.ToolGateway.CatalogDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fault.New(fault.CodeInvalidSpec, "tool catalog directory is unavailable")
	}
	lease := &toolGatewayLease{binary: b.dialect.Binary(), name: "ty-tool-gw-" + input.Name,
		logPath: input.LogPath + ".gateway.log", secrets: input.ToolGateway.Credentials}
	if err := os.MkdirAll(filepath.Dir(lease.logPath), 0o700); err != nil {
		return nil, fault.Wrap(fault.CodeSandboxUnavailable, "prepare tool gateway log", err, "cannot prepare tool gateway log directory")
	}
	ready := false
	if model != nil {
		lease.network = model.network
	} else {
		lease.network = "ty-tool-net-" + input.Name
		lease.ownsNetwork = true
		args := []string{"network", "create"}
		if input.Sandbox.Network == contracts.SandboxNetworkModelOnly {
			args = append(args, "--internal")
		}
		if err := runGatewayCLI(ctx, lease.binary, append(args, lease.network)...); err != nil {
			return nil, err
		}
	}
	defer func() {
		if !ready {
			if err := lease.close(context.WithoutCancel(ctx)); err != nil {
				observe.LogFailure(ctx, "failed tool gateway setup cleanup", err, "container", lease.name, "network", lease.network)
			}
		}
	}()
	toolHome := filepath.Join(filepath.Dir(input.State), "tool-home")
	if err := os.MkdirAll(toolHome, 0o700); err != nil {
		return nil, fault.Wrap(fault.CodeSandboxUnavailable, "prepare tool home", err, "cannot prepare tool home")
	}
	args := []string{"run", "-d", "--name", lease.name, "--network", lease.network,
		"--network-alias", "turnyard-tools", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--read-only", "--tmpfs", "/tmp", "--tmpfs", "/state:rw,exec,mode=1777", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/state/home", toolHome),
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/state/tool-gateway,readonly", filepath.Join(input.State, "tool-gateway")),
		"--mount", fmt.Sprintf("type=bind,source=%s,target=/turnyard-catalog", input.ToolGateway.CatalogDir)}
	for _, name := range []string{"tools", "tool-config"} {
		source := filepath.Join(input.State, name)
		if info, err := os.Lstat(source); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/state/%s,readonly", source, name))
		} else if err != nil && !os.IsNotExist(err) {
			return nil, fault.Wrap(fault.CodeInvalidSpec, "inspect tool state", err, "cannot inspect tool state")
		} else if err == nil {
			return nil, fault.New(fault.CodeInvalidSpec, "tool state path is not a directory")
		}
	}
	if input.ControlDir != "" {
		if info, err := os.Lstat(input.ControlDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fault.New(fault.CodeInvalidSpec, "delegation control directory is unavailable")
		}
		if !gatewayInvocationID.MatchString(input.ToolGateway.InvocationID) {
			return nil, fault.New(fault.CodeInvalidSpec, "delegation invocation ID is invalid")
		}
		requestDir := filepath.Join(input.State, "delegation-requests", input.ToolGateway.InvocationID)
		if info, err := os.Lstat(requestDir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fault.New(fault.CodeInvalidSpec, "delegation request directory is unavailable")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/turnyard-control,readonly", input.ControlDir),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/state/delegation-requests/%s", requestDir, input.ToolGateway.InvocationID),
			"--env", "TURNYARD_DELEGATION_INVOCATION")
	}
	if input.InputDir != "" {
		if info, err := os.Stat(input.InputDir); err != nil || !info.IsDir() {
			return nil, fault.New(fault.CodeInvalidSpec, "staged input directory is unavailable")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/.turnyard-input,readonly", input.InputDir))
	}
	if input.ArtifactDir != "" {
		if err := os.MkdirAll(input.ArtifactDir, 0o700); err != nil {
			return nil, fault.Wrap(fault.CodeSandboxUnavailable, "prepare tool output mount", err, "cannot prepare tool output directory")
		}
		args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/.turnyard-output", input.ArtifactDir))
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
				return nil, fault.New(fault.CodeInvalidRepository, "%s has no Git metadata directory", item.ID)
			}
			args = append(args, "--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/%s/.git,readonly", gitPath, item.ID))
		}
	}
	for _, name := range sortedKeys(input.ToolGateway.Credentials) {
		if !validCredentialName(name) {
			return nil, fault.New(fault.CodeInvalidSpec, "invalid tool gateway credential name")
		}
		args = append(args, "--env", name)
	}
	args = append(args, "--env", "HOME=/state/home", "--env", "PWD=/workspace", "--workdir", "/workspace")
	if input.Sandbox.Network == contracts.SandboxNetworkModelOnly {
		args = append(args, "--entrypoint", "/bin/sh", input.Sandbox.Image, "-c",
			`while [ ! -f /tmp/turnyard-start ]; do sleep 0.1; done; read -r bind < /tmp/turnyard-start; exec /usr/local/bin/turnyard-tool-gateway serve --config "$1" --address "$bind:8081"`,
			"sh", filepath.Join("/state", relative))
	} else {
		args = append(args, "--entrypoint", "/usr/local/bin/turnyard-tool-gateway", input.Sandbox.Image,
			"serve", "--config", filepath.Join("/state", relative))
	}
	cmd := exec.CommandContext(ctx, lease.binary, args...)
	cmd.Env = os.Environ()
	for name, value := range input.ToolGateway.Credentials {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	if input.ControlDir != "" {
		cmd.Env = append(cmd.Env, "TURNYARD_DELEGATION_INVOCATION="+input.ToolGateway.InvocationID)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fault.Wrap(fault.CodeSandboxUnavailable, "tool gateway", err, "start tool gateway: %s", strings.TrimSpace(string(output)))
	}
	if input.Sandbox.Network == contracts.SandboxNetworkModelOnly {
		if err := runGatewayCLI(ctx, lease.binary, "network", "connect", "bridge", lease.name); err != nil {
			return nil, err
		}
		lease.address, err = gatewayNetworkAddress(ctx, lease.binary, lease.name, lease.network)
		if err != nil {
			return nil, err
		}
		release := exec.CommandContext(ctx, lease.binary, "exec", lease.name, "/bin/sh", "-c",
			`printf '%s\n' "$1" > /tmp/turnyard-start.tmp && mv /tmp/turnyard-start.tmp /tmp/turnyard-start`, "sh", lease.address)
		if output, err := release.CombinedOutput(); err != nil {
			return nil, fault.Wrap(fault.CodeSandboxUnavailable, "release tool gateway startup", err, "cannot release tool gateway startup: %s", strings.TrimSpace(string(output)))
		}
	}
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		probe := exec.CommandContext(probeCtx, lease.binary, "exec", lease.name, "/usr/local/bin/turnyard-tool-gateway", "probe", "--address", "turnyard-tools:8081")
		if probe.Run() == nil {
			cancel()
			if input.Sandbox.Isolation == contracts.SandboxIsolationGVisor && lease.address == "" {
				lease.address, err = gatewayNetworkAddress(ctx, lease.binary, lease.name, lease.network)
				if err != nil {
					return nil, err
				}
			}
			ready = true
			return lease, nil
		}
		cancel()
		status, inspectErr := exec.CommandContext(ctx, lease.binary, "inspect", "--format", "{{.State.Running}}", lease.name).Output()
		if inspectErr == nil && strings.TrimSpace(string(status)) == "false" {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	output, _ := exec.CommandContext(ctx, lease.binary, "logs", "--tail", "40", lease.name).CombinedOutput()
	detail := redactGatewaySecrets(strings.TrimSpace(string(output)), lease.secrets)
	if len(detail) > 2048 {
		detail = detail[len(detail)-2048:]
	}
	return nil, fault.New(fault.CodeSandboxUnavailable, "tool gateway did not become ready: %s", detail)
}

func (l *toolGatewayLease) close(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if l.logPath != "" {
		if output, err := exec.CommandContext(call, l.binary, "logs", "--tail", "1000", l.name).CombinedOutput(); err == nil {
			body := redactGatewaySecrets(string(output), l.secrets)
			if err := os.WriteFile(l.logPath, []byte(body), 0o600); err != nil {
				observe.LogFailure(ctx, "persist tool gateway log failed", err, "path", l.logPath)
			}
		}
	}
	containerErr := exec.CommandContext(call, l.binary, "rm", "-f", l.name).Run()
	if containerErr != nil {
		output, inspectErr := exec.CommandContext(call, l.binary, "inspect", l.name).CombinedOutput()
		if inspectErr == nil || !gatewayResourceMissing(output, l.name) {
			return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove tool gateway", errors.Join(containerErr, inspectErr), "tool gateway container %s cleanup unconfirmed", l.name)
		}
	}
	if l.ownsNetwork {
		networkErr := exec.CommandContext(call, l.binary, "network", "rm", l.network).Run()
		if networkErr != nil {
			output, inspectErr := exec.CommandContext(call, l.binary, "network", "inspect", l.network).CombinedOutput()
			if inspectErr == nil || !gatewayResourceMissing(output, l.network) {
				return fault.Wrap(fault.CodeSandboxCleanupUnknown, "remove tool gateway network", errors.Join(networkErr, inspectErr), "tool gateway network %s cleanup unconfirmed", l.network)
			}
		}
	}
	return nil
}

func redactGatewaySecrets(body string, secrets map[string]string) string {
	for _, value := range secrets {
		if value != "" {
			body = strings.ReplaceAll(body, value, "[REDACTED]")
		}
	}
	return body
}

func toolGatewayRunOptions(spec contracts.SandboxSpec, lease *toolGatewayLease) ([]string, error) {
	options := []string{"--network", lease.network}
	if spec.Isolation == contracts.SandboxIsolationGVisor {
		if net.ParseIP(lease.address) == nil {
			return nil, fault.New(fault.CodeSandboxUnavailable, "gVisor tool gateway has no internal address")
		}
		options = append(options, "--add-host", "turnyard-tools:"+lease.address)
	}
	return options, nil
}
