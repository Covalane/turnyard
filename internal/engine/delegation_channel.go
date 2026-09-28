package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
)

// startDelegationServer exposes only parent-scoped operations through a
// read-only response mount. Requests arrive through a separate writable
// directory so the channel works with Docker Desktop's file sharing too.
func (s *Service) startDelegationServer(parent turnExecution) (string, func(), error) {
	if len(parent.agent.Delegates) == 0 {
		return "", func() {}, nil
	}
	dir := filepath.Join(filepath.Dir(parent.workspace), "control", parent.invocationID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return "", nil, err
	}
	responseDir := filepath.Join(dir, "responses")
	if err := os.MkdirAll(responseDir, 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Chmod(responseDir, 0o755); err != nil {
		return "", nil, err
	}
	requestsParent, root, err := openDelegationRequestRoot(parent.state, parent.invocationID)
	if err != nil {
		return "", nil, errors.Join(err, os.RemoveAll(dir))
	}
	server := &delegationServer{service: s, parent: parent, controlDir: dir,
		requestRoot: root, stop: make(chan struct{}), done: make(chan struct{}),
		handoffs: map[string]delegationHandoff{}}
	go server.serve()
	return dir, func() {
		close(server.stop)
		<-server.done
		if err := errors.Join(server.requestRoot.Close(), requestsParent.RemoveAll(parent.invocationID),
			requestsParent.Close(), os.RemoveAll(dir)); err != nil {
			ctx := observe.WithIDs(context.Background(), observe.IDs{TaskID: parent.task.ID, InvocationID: parent.invocationID})
			observe.LogFailure(ctx, "delegation control cleanup failed", fault.Ensure(err, "close delegation control"))
		}
	}, nil
}

// The agent owns /state, so every request-directory operation is anchored to
// that tree. Open directory descriptors also keep cleanup inside the original
// parent if an agent later replaces its pathname with a symlink.
func openDelegationRequestRoot(state, invocationID string) (*os.Root, *os.Root, error) {
	if !regexp.MustCompile(`^inv_[a-f0-9]{16}$`).MatchString(invocationID) {
		return nil, nil, fault.New(fault.CodeInvalidRequest, "invalid delegation invocation ID")
	}
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fault.New(fault.CodeInvalidSpec, "agent state directory is unavailable")
	}
	stateRoot, err := os.OpenRoot(state)
	if err != nil {
		return nil, nil, err
	}
	defer stateRoot.Close()
	const parent = "delegation-requests"
	if err := stateRoot.Mkdir(parent, 0o755); err != nil && !os.IsExist(err) {
		return nil, nil, err
	}
	parentInfo, err := stateRoot.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fault.New(fault.CodeInvalidSpec, "delegation request parent is not a directory")
	}
	parentRoot, err := stateRoot.OpenRoot(parent)
	if err != nil {
		return nil, nil, err
	}
	opened, err := parentRoot.Stat(".")
	if err != nil || !os.SameFile(parentInfo, opened) {
		return nil, nil, discardDelegationRoot(fault.New(fault.CodeInvalidSpec, "delegation request parent changed"), parentRoot, nil, invocationID, false)
	}
	if err := chmodRootDirectory(parentRoot, 0o755); err != nil {
		return nil, nil, discardDelegationRoot(err, parentRoot, nil, invocationID, false)
	}
	if err := parentRoot.Mkdir(invocationID, 0o777); err != nil {
		return nil, nil, discardDelegationRoot(err, parentRoot, nil, invocationID, false)
	}
	requestInfo, err := parentRoot.Lstat(invocationID)
	if err != nil || !requestInfo.IsDir() || requestInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, discardDelegationRoot(fault.New(fault.CodeInvalidSpec, "delegation request directory changed"), parentRoot, nil, invocationID, true)
	}
	requestRoot, err := parentRoot.OpenRoot(invocationID)
	if err != nil {
		return nil, nil, discardDelegationRoot(err, parentRoot, nil, invocationID, true)
	}
	opened, err = requestRoot.Stat(".")
	if err != nil || !os.SameFile(requestInfo, opened) {
		return nil, nil, discardDelegationRoot(fault.New(fault.CodeInvalidSpec, "delegation request directory changed"), parentRoot, requestRoot, invocationID, true)
	}
	if err := chmodRootDirectory(requestRoot, 0o777); err != nil {
		return nil, nil, discardDelegationRoot(err, parentRoot, requestRoot, invocationID, true)
	}
	return parentRoot, requestRoot, nil
}

func discardDelegationRoot(cause error, parentRoot, requestRoot *os.Root, invocationID string, remove bool) error {
	var cleanup []error
	if requestRoot != nil {
		cleanup = append(cleanup, requestRoot.Close())
	}
	if remove {
		cleanup = append(cleanup, parentRoot.RemoveAll(invocationID))
	}
	cleanup = append(cleanup, parentRoot.Close())
	if err := errors.Join(cleanup...); err != nil {
		return fault.At(errors.Join(cause, err), "discard delegation request root")
	}
	return cause
}

func chmodRootDirectory(root *os.Root, mode fs.FileMode) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Chmod(mode)
}

func (d *delegationServer) serve() {
	defer close(d.done)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	ctx := observe.WithIDs(context.Background(), observe.IDs{TaskID: d.parent.task.ID, InvocationID: d.parent.invocationID})
	readFailureLogged := false
	for {
		if err := d.processRequests(); err != nil {
			if !readFailureLogged {
				observe.LogFailure(ctx, "delegation request scan failed", fault.Ensure(err, "scan delegation requests"))
				readFailureLogged = true
			}
		} else {
			readFailureLogged = false
		}
		select {
		case <-d.stop:
			return
		case <-ticker.C:
		}
	}
}

func (d *delegationServer) processRequests() error {
	dir, err := d.requestRoot.Open(".")
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(256)
	closeErr := dir.Close()
	if err != nil && err != io.EOF {
		return errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	processed := 0
	for _, item := range entries {
		if !delegationFilePattern.MatchString(item.Name()) {
			continue
		}
		d.processRequest(item.Name())
		processed++
		if processed >= 64 {
			break
		}
	}
	return nil
}

func (d *delegationServer) processRequest(name string) {
	response := delegationResponse{}
	logCtx := observe.WithIDs(context.Background(), observe.IDs{TaskID: d.parent.task.ID, InvocationID: d.parent.invocationID})
	defer func() {
		if err := d.writeResponse(name, response); err != nil {
			observe.LogFailure(logCtx, "delegation response delivery failed", err)
		}
		if err := d.requestRoot.Remove(name); err != nil {
			observe.LogFailure(logCtx, "delegation request removal failed", fault.Ensure(err, "remove delegation request"))
		}
	}()
	file, err := d.requestRoot.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		response.Error = "delegation request unavailable or unsafe"
		return
	}
	info, err := file.Stat()
	if err != nil {
		observe.LogFailure(logCtx, "delegation request inspection failed", fault.Ensure(errors.Join(err, file.Close()), "inspect delegation request"))
		response.Error = "invalid delegation request file"
		return
	}
	if !info.Mode().IsRegular() || info.Size() > maxDelegationRequest {
		if err := file.Close(); err != nil {
			observe.LogFailure(logCtx, "delegation request close failed", fault.Ensure(err, "close invalid delegation request"))
		}
		response.Error = "invalid delegation request file"
		return
	}
	body, err := io.ReadAll(io.LimitReader(file, maxDelegationRequest+1))
	if err := errors.Join(err, file.Close()); err != nil {
		observe.LogFailure(logCtx, "delegation request read failed", fault.Ensure(err, "read delegation request"))
		response.Error = "delegation request unavailable"
		return
	}
	if len(body) > maxDelegationRequest {
		response.Error = "delegation request exceeds limit"
		return
	}
	var request delegationRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		response.Error = "invalid delegation request"
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		response.Error = "delegation request has trailing data"
		return
	}
	ctx, cancel := context.WithTimeout(logCtx, 110*time.Second)
	defer cancel()
	result, err := d.dispatch(ctx, request)
	if err != nil {
		observe.LogFailure(ctx, "delegation request failed", fault.Ensure(err, "dispatch delegation request"))
		response.Error = err.Error()
		return
	}
	response.OK, response.Result = true, result
}

func (d *delegationServer) writeResponse(name string, response delegationResponse) (writeErr error) {
	body, err := json.Marshal(response)
	if err != nil {
		return fault.Wrap(fault.CodeInternalError, "encode delegation response", err, "delegation response could not be encoded")
	}
	if len(body) > maxDelegationRequest {
		body = []byte(`{"ok":false,"error":"delegation response exceeds size limit"}`)
	}
	root := filepath.Join(d.controlDir, "responses")
	tmp, err := os.CreateTemp(root, ".response-")
	if err != nil {
		return fault.Wrap(fault.CodeInternalError, "create delegation response", err, "delegation response file unavailable")
	}
	defer func() {
		if err := os.Remove(tmp.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeErr = fault.At(errors.Join(writeErr, err), "remove delegation response staging file")
		}
	}()
	if _, err := tmp.Write(body); err != nil {
		return fault.Wrap(fault.CodeInternalError, "write delegation response", errors.Join(err, tmp.Close()), "delegation response could not be written")
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fault.Wrap(fault.CodeInternalError, "chmod delegation response", errors.Join(err, tmp.Close()), "delegation response permissions could not be set")
	}
	if err := tmp.Close(); err != nil {
		return fault.Wrap(fault.CodeInternalError, "close delegation response", err, "delegation response could not be closed")
	}
	if err := os.Rename(tmp.Name(), filepath.Join(root, name)); err != nil {
		return fault.Wrap(fault.CodeInternalError, "publish delegation response", err, "delegation response could not be published")
	}
	return nil
}
