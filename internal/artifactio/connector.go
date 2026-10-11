// Package artifactio executes trusted byte-transfer connectors. Task input
// selects a configured connector and URI; it never supplies a command line.
package artifactio

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
)

type Connector struct {
	Spec contracts.ArtifactConnectorSpec
}

// ErrNotFound is returned only for the command's explicitly configured missing
// object exit codes. Transport, authorization and timeout failures retain their
// original cause and must never authorize a write.
var ErrNotFound = errors.New("artifact object not found")

func Find(env contracts.EnvironmentSpec, id string) (Connector, error) {
	for _, spec := range env.ArtifactConnectors {
		if spec.ID == id {
			return Connector{Spec: spec}, nil
		}
	}
	return Connector{}, fault.New(fault.CodeInvalidSpec, "unknown artifact connector %s", id)
}

func (c Connector) validURI(uri string) bool {
	return contracts.ValidConnectorURI(c.Spec, uri)
}

// Get writes to a fresh temporary file and only exposes complete downloads.
// maxBytes is enforced during transfer as well as before the rename.
func (c Connector) Get(ctx context.Context, uri, destination string, maxBytes int64) (getErr error) {
	if !c.validURI(uri) {
		return fault.New(fault.CodeInvalidSpec, "connector URI is outside its allowed prefix")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".turnyard-fetch-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	if err := file.Close(); err != nil {
		return fault.Wrap(fault.CodeDeliverableUnverified, "close connector staging file", errors.Join(err, os.Remove(temporary)), "connector download staging failed")
	}
	if err := os.Remove(temporary); err != nil {
		return fault.Wrap(fault.CodeDeliverableUnverified, "prepare connector staging path", err, "connector download staging failed")
	}
	defer func() {
		if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
			getErr = fault.At(errors.Join(getErr, err), "remove connector staging file")
		}
	}()
	if maxBytes <= 0 {
		return fault.New(fault.CodeInvalidSpec, "connector download requires a positive size limit")
	}
	if err := c.run(ctx, c.Spec.GetArgv, uri, temporary, maxBytes); err != nil {
		return err
	}
	info, err := os.Lstat(temporary)
	if err != nil {
		return fault.Wrap(fault.CodeDeliverableUnverified, "stat connector output", err, "connector output cannot be inspected")
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return fault.New(fault.CodeDeliverableUnverified, "connector did not return a regular file within the size limit")
	}
	return os.Rename(temporary, destination)
}

func (c Connector) Put(ctx context.Context, source, uri string) error {
	if !c.validURI(uri) || len(c.Spec.PutArgv) == 0 {
		return fault.New(fault.CodeInvalidSpec, "connector cannot publish to this URI")
	}
	if err := contracts.ValidateConnectorDelivery(c.Spec); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fault.Wrap(fault.CodeDeliverableInvalid, "stat connector source", err, "published source cannot be inspected")
	}
	if !info.Mode().IsRegular() {
		return fault.New(fault.CodeDeliverableInvalid, "published source is not a regular file")
	}
	return c.run(ctx, c.Spec.PutArgv, uri, source, 0)
}

func (c Connector) run(ctx context.Context, argv []string, uri, file string, maxBytes int64) error {
	if len(argv) == 0 || argv[0] == "" {
		return fault.New(fault.CodeInvalidSpec, "connector command is missing")
	}
	timeout := c.Spec.TimeoutSeconds
	if timeout == 0 {
		timeout = 300
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	args := make([]string, len(argv))
	for i, item := range argv {
		args[i] = strings.ReplaceAll(strings.ReplaceAll(item, "{uri}", uri), "{file}", file)
	}
	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for _, name := range c.Spec.PassEnv {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	if maxBytes > 0 {
		exceeded := make(chan struct{}, 1)
		stopped := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopped:
					return
				case <-ticker.C:
					info, err := os.Lstat(file)
					if err == nil && (!info.Mode().IsRegular() || info.Size() > maxBytes) {
						exceeded <- struct{}{}
						cancel()
						return
					}
				}
			}
		}()
		err := cmd.Run()
		close(stopped)
		<-done
		select {
		case <-exceeded:
			return fault.New(fault.CodeDeliverableUnverified, "%s download exceeds size limit", c.Spec.ID)
		default:
		}
		return c.operationError(runCtx, err, true)
	}
	// CLI stderr may contain signed URLs or credentials, so never persist it.
	return c.operationError(runCtx, cmd.Run(), false)
}

func (c Connector) operationError(ctx context.Context, err error, read bool) error {
	if cause := ctx.Err(); cause != nil {
		return fault.Wrap(fault.CodeDeliverableUnverified, "artifact connector", cause, "%s operation interrupted", c.Spec.ID)
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if read && errors.As(err, &exit) && slices.Contains(c.Spec.NotFoundExitCodes, exit.ExitCode()) {
		return fault.Wrap(fault.CodeNotFound, "artifact connector", errors.Join(ErrNotFound, err), "%s object is absent", c.Spec.ID)
	}
	return fault.Wrap(fault.CodeDeliverableUnverified, "artifact connector", err, "%s operation failed", c.Spec.ID)
}
