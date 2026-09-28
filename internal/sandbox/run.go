package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

const (
	defaultSandboxTimeout = 10 * time.Minute
	defaultSandboxCPUs    = 2
	defaultSandboxMemory  = 2048
)

// EffectiveResources is shared by OCI run arguments and supervisor admission.
func EffectiveResources(spec contracts.SandboxSpec) (cpus, memoryMB int) {
	cpus, memoryMB = spec.CPUs, spec.MemoryMB
	if cpus == 0 {
		cpus = defaultSandboxCPUs
	}
	if memoryMB == 0 {
		memoryMB = defaultSandboxMemory
	}
	return cpus, memoryMB
}

func (b OCIBackend) Run(ctx context.Context, input SandboxRun) (result SandboxResult, runErr error) {
	result = SandboxResult{ExitCode: -1, Backend: b.Name()}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := b.ValidateSpec(ctx, input.Sandbox); err != nil {
		return result, err
	}
	if input.ModelGateway != nil {
		if input.Sandbox.Network != contracts.SandboxNetworkModelOnly {
			return result, fault.New(fault.CodeInvalidSpec, "model gateway requires model-only network")
		}
		for name, value := range input.Credentials {
			if value != ModelCredentialPlaceholder {
				return result, fault.New(fault.CodeInvalidSpec, "model-only container received non-placeholder credential %s", name)
			}
		}
	}
	if input.ToolGateway != nil && b.Name() != BackendDocker {
		return result, fault.New(fault.CodeCapabilityMissing, "tool gateway sidecar requires Docker")
	}
	probe, err := b.Probe(ctx)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if probe["available"] != true {
		return result, fault.New(fault.CodeSandboxUnavailable, "%s is unavailable", b.Name())
	}
	actual, err := b.ImageIdentity(ctx, input.Sandbox.Image)
	if err != nil {
		return result, err
	}
	if input.Sandbox.ImageDigest != "" {
		if actual != input.Sandbox.ImageDigest {
			return result, fault.New(fault.CodeEnvironmentDrift, "OCI image tag no longer resolves to locked digest")
		}
	}
	if input.Sandbox.Network == contracts.SandboxNetworkModelOnly && input.ModelGateway == nil {
		// Inventory and checks never need the model provider. They run offline.
		input.Credentials = placeholderCredentials(input.Credentials)
	}
	networkMode := input.Sandbox.Network
	if input.ModelGateway != nil || input.ToolGateway != nil {
		networkMode = contracts.SandboxNetworkDefault
	}
	extraOptions, err := b.dialect.RunOptions(networkMode)
	if err != nil {
		return result, err
	}
	isolationOptions, err := b.dialect.IsolationOptions(ctx, input.Sandbox.Isolation)
	if err != nil {
		return result, err
	}
	extraOptions = append(extraOptions, isolationOptions...)
	var modelLease *modelGatewayLease
	if input.ModelGateway != nil {
		modelLease, err = b.startModelGateway(ctx, input)
		if err != nil {
			return result, err
		}
		defer func() {
			if err := modelLease.close(context.WithoutCancel(ctx)); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}()
		gatewayOptions, err := gatewayRunOptions(input.Sandbox, modelLease)
		if err != nil {
			return result, err
		}
		extraOptions = append(extraOptions, gatewayOptions...)
	}
	if input.ToolGateway != nil {
		toolLease, err := b.startToolGateway(ctx, input, modelLease)
		if err != nil {
			return result, err
		}
		defer func() {
			if err := toolLease.close(context.WithoutCancel(ctx)); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}()
		toolOptions, err := toolGatewayRunOptions(input.Sandbox, toolLease)
		if err != nil {
			return result, err
		}
		if modelLease != nil {
			// Both gateways use the same private network; keep only one --network.
			toolOptions = toolOptions[2:]
		}
		extraOptions = append(extraOptions, toolOptions...)
	}
	if input.Timeout == 0 {
		input.Timeout = defaultSandboxTimeout
	}
	cmd, cpus, memory, err := b.buildCommand(input, extraOptions)
	if err != nil {
		return result, err
	}
	var stdout, stderr cappedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	if err := cmd.Start(); err != nil {
		return result, fault.Wrap(fault.CodeSandboxUnavailable, "", err, "start %s", b.Name())
	}
	operationalLog.InfoContext(ctx, "sandbox started", "backend", b.Name(), "isolation", input.Sandbox.Isolation, "container", input.Name,
		"image", input.Sandbox.Image, "cpu", cpus, "memory_mb", memory, "timeout_seconds", input.Timeout.Seconds())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(input.Timeout)
	defer timer.Stop()
	var runError error
	var cleanupError error
	interrupted := false
	select {
	case err = <-done:
		if err == nil {
			result.ExitCode = 0
		} else if exit, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exit.ExitCode()
		} else {
			runError = err
		}
	case <-timer.C:
		interrupted = true
		result.TimedOut = true
		cleanupError = b.cleanup(context.WithoutCancel(ctx), input.Name)
		exited := waitProcess(done, 10*time.Second)
		if !exited {
			_ = cmd.Process.Kill()
			exited = waitProcess(done, 10*time.Second)
		}
		if !exited {
			return result, fault.New(fault.CodeSandboxCleanupUnknown, "container %s process did not exit after timeout", input.Name)
		}
	case <-ctx.Done():
		interrupted = true
		result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
		runError = ctx.Err()
		cleanupError = b.cleanup(context.WithoutCancel(ctx), input.Name)
		exited := waitProcess(done, 10*time.Second)
		if !exited {
			_ = cmd.Process.Kill()
			exited = waitProcess(done, 10*time.Second)
		}
		if !exited {
			return result, fault.New(fault.CodeSandboxCleanupUnknown, "container %s process did not exit after cancellation", input.Name)
		}
	}
	cleanupError = errors.Join(cleanupError, b.cleanup(context.WithoutCancel(ctx), input.Name))
	var interruptionCleanupErr error
	if interrupted {
		// The CLI can exit before Docker finishes a pending create/start. Keep
		// checking for a short quiet period and remove any late container.
		interruptionCleanupErr = b.confirmInterruptedCleanup(ctx, input.Name)
	}
	// A failed cleanup is an uncertain execution state even when the container
	// command already returned a nonzero exit code.
	cleanupUnknown, inspectError := b.containerPresent(context.WithoutCancel(ctx), input.Name)
	result.Output = stdout.String()
	result.Stderr = stderr.String()
	for _, credential := range input.Credentials {
		if credential != "" {
			result.Output = strings.ReplaceAll(result.Output, credential, "[REDACTED]")
			result.Stderr = strings.ReplaceAll(result.Stderr, credential, "[REDACTED]")
		}
	}
	if input.ToolGateway != nil {
		for _, credential := range input.ToolGateway.Credentials {
			if credential != "" {
				result.Output = strings.ReplaceAll(result.Output, credential, "[REDACTED]")
				result.Stderr = strings.ReplaceAll(result.Stderr, credential, "[REDACTED]")
			}
		}
	}
	logged := result.Output
	if result.Stderr != "" {
		logged += "\n[container stderr]\n" + result.Stderr
	}
	if len(logged) > maxCapturedStreamBytes {
		logged = logged[:maxCapturedStreamBytes] + "\n[TURNYARD_LOG_TRUNCATED]"
	}
	if err := os.WriteFile(input.LogPath, []byte(logged), 0o600); err != nil {
		return result, err
	}
	operationalLog.InfoContext(ctx, "sandbox finished", "backend", b.Name(), "isolation", input.Sandbox.Isolation, "container", input.Name,
		"exit_code", result.ExitCode, "timed_out", result.TimedOut,
		"output_truncated", stdout.truncated || stderr.truncated, "duration_ms", time.Since(started).Milliseconds(),
		"log_path", input.LogPath)
	if inspectError != nil {
		return result, fault.Wrap(fault.CodeSandboxCleanupUnknown, "confirm container cleanup", errors.Join(cleanupError, inspectError), "cannot confirm container %s is absent", input.Name)
	}
	if interruptionCleanupErr != nil {
		return result, interruptionCleanupErr
	}
	if cleanupUnknown && cleanupError != nil {
		return result, fault.At(cleanupError, "container remained after execution")
	}
	if cleanupUnknown {
		return result, fault.New(fault.CodeSandboxCleanupUnknown, "container %s remained after execution", input.Name)
	}
	if cleanupError != nil {
		operationalLog.WarnContext(ctx, "container removal retried and absence confirmed", "container", input.Name, "origin", fault.Origin(cleanupError))
	}
	if runError != nil {
		return result, runError
	}
	if stdout.truncated || stderr.truncated {
		return result, fault.New(fault.CodeAgentOutputTruncated, "sandbox output exceeded capture limit; inspect invocation log")
	}
	return result, nil
}

func waitProcess(done <-chan error, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func gatewayRunOptions(spec contracts.SandboxSpec, lease *modelGatewayLease) ([]string, error) {
	options := []string{"--network", lease.network}
	if spec.Isolation == contracts.SandboxIsolationGVisor {
		// runsc cannot resolve Docker's embedded DNS on a user-defined bridge.
		if lease.address == "" {
			return nil, fault.New(fault.CodeSandboxUnavailable, "gVisor model gateway has no internal address")
		}
		options = append(options, "--add-host", "turnyard-model:"+lease.address)
	}
	return options, nil
}
