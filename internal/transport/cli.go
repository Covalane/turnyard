package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Covalane/turnyard/internal/agents/registry"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/engine"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/lifecycle"
	"github.com/Covalane/turnyard/internal/observe"
	sandboxregistry "github.com/Covalane/turnyard/internal/sandbox/registry"
	"github.com/Covalane/turnyard/internal/stateops"
	"github.com/Covalane/turnyard/internal/store"
)

func option(args []string, name cliFlag) (string, error) {
	for i, arg := range args {
		if arg == string(name) {
			if i+1 >= len(args) {
				return "", fault.New(fault.CodeInvalidRequest, "%s needs a value", name)
			}
			return args[i+1], nil
		}
	}
	return "", fault.New(fault.CodeInvalidRequest, "missing %s", name)
}
func optional(args []string, name cliFlag) (string, bool) {
	for i, arg := range args {
		if arg == string(name) && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}
func positional(args []string, index int) (string, error) {
	if len(args) <= index || strings.HasPrefix(args[index], "--") {
		return "", fault.New(fault.CodeInvalidRequest, "missing positional argument")
	}
	return args[index], nil
}
func runCLI(ctx context.Context, args []string) (map[string]any, error) {
	home := DefaultHome()
	if len(args) >= 2 && args[0] == string(cliFlagHome) {
		home = args[1]
		args = args[2:]
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	home = abs
	if len(args) == 0 {
		return nil, fault.New(fault.CodeInvalidRequest, "usage: turnyard [%s DIR] %s|%s|%s|%s|%s|%s",
			cliFlagHome, cliCommandDoctor, cliCommandDaemon, cliCommandSession, cliCommandTask, cliCommandCheckpoint, cliCommandState)
	}
	switch cliCommand(args[0]) {
	case cliCommandState:
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "%s needs %s, %s, or %s",
				cliCommandState, cliVerbBackup, cliVerbVerify, cliVerbRestore)
		}
		switch cliVerb(args[1]) {
		case cliVerbBackup:
			destination, err := option(args[2:], cliFlagOut)
			if err != nil {
				return nil, err
			}
			lock, err := stateops.Acquire(home)
			if err != nil {
				return nil, fault.Wrap(fault.CodeSessionBusy, "backup state", err, "stop supervisor before backup")
			}
			defer lock.Close()
			manifest, err := stateops.Backup(ctx, home, destination)
			if err != nil {
				return nil, err
			}
			return map[string]any{"backup": destination, "format": manifest.Format, "entries": len(manifest.Entries)}, nil
		case cliVerbVerify:
			source, err := option(args[2:], cliFlagFrom)
			if err != nil {
				return nil, err
			}
			manifest, err := stateops.Verify(ctx, source)
			if err != nil {
				return nil, err
			}
			return map[string]any{"backup": source, "format": manifest.Format, "entries": len(manifest.Entries), "source": manifest.Source}, nil
		case cliVerbRestore:
			source, err := option(args[2:], cliFlagFrom)
			if err != nil {
				return nil, err
			}
			destination, err := option(args[2:], cliFlagTo)
			if err != nil {
				return nil, err
			}
			if err := stateops.Restore(ctx, source, destination); err != nil {
				return nil, err
			}
			return map[string]any{"restored": true, "home": destination}, nil
		default:
			return nil, fault.New(fault.CodeInvalidRequest, "unknown %s action", cliCommandState)
		}
	case cliCommandDoctor:
		probes := map[string]any{}
		for _, name := range sandboxregistry.Backends() {
			b, err := sandboxregistry.Backend(name)
			if err != nil {
				return nil, err
			}
			p, err := b.Probe(ctx)
			if err != nil {
				observe.LogFailure(ctx, "sandbox doctor probe failed", err, "backend", name)
				p["error_code"] = fault.CodeOf(err)
			}
			probes[name] = p
		}
		probes["agent_runtimes"] = registry.Runtimes()
		probes["home"] = home
		return probes, nil
	case cliCommandDaemon:
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "%s needs action", cliCommandDaemon)
		}
		switch cliVerb(args[1]) {
		case cliVerbServe:
			value, err := option(args[2:], cliFlagHome)
			if err != nil {
				return nil, err
			}
			return nil, Serve(ctx, value)
		case cliVerbStart:
			if err := StartDaemon(ctx, home); err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionPing}, false)
		case cliVerbStatus:
			return ClientRequest(ctx, home, Request{Action: actionPing}, false)
		case cliVerbStop:
			return ClientRequest(ctx, home, Request{Action: actionDaemonStop}, false)
		default:
			return nil, fault.New(fault.CodeInvalidRequest, "unknown %s action", cliCommandDaemon)
		}
	case cliCommandSession:
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "%s needs action", cliCommandSession)
		}
		switch cliVerb(args[1]) {
		case cliVerbCreate:
			path, err := option(args[2:], cliFlagFile)
			if err != nil {
				return nil, err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			timeout := 0
			if value, ok := optional(args[2:], cliFlagTimeout); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, fault.Wrap(fault.CodeInvalidRequest, "parse session timeout", err, "invalid %s", cliFlagTimeout)
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionCreate, File: path, Timeout: timeout}, true)
		case cliVerbShow:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionShow, SessionID: id}, true)
		case cliVerbComplete:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionComplete, SessionID: id}, true)
		case cliVerbPublish:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			timeout := 600
			if value, ok := optional(args[3:], cliFlagTimeout); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, fault.Wrap(fault.CodeInvalidRequest, "parse publish timeout", err, "invalid %s", cliFlagTimeout)
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionPublish, SessionID: id, Timeout: timeout}, true)
		case cliVerbCancel:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			reason, err := option(args[3:], cliFlagReason)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionCancel, SessionID: id, Reason: reason}, true)
		case cliVerbEvents:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			after := int64(0)
			if value, ok := optional(args[3:], cliFlagAfter); ok {
				after, err = strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, err
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionEvents, SessionID: id, After: after}, true)
		case cliVerbPrune:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			backup, err := option(args[3:], cliFlagBackup)
			if err != nil {
				return nil, err
			}
			lock, err := stateops.Acquire(home)
			if err != nil {
				return nil, fault.Wrap(fault.CodeSessionBusy, "prune session", err, "stop supervisor before pruning")
			}
			defer lock.Close()
			if err := stateops.VerifyCurrent(ctx, home, backup); err != nil {
				return nil, fault.Wrap(fault.CodeInvalidRequest, "verify backup", err, "an unchanged verified backup is required")
			}
			database, err := store.OpenStore(ctx, home)
			if err != nil {
				return nil, err
			}
			defer database.Close()
			artifacts, err := database.PruneTerminalSession(ctx, id)
			if err != nil {
				return nil, err
			}
			if err := os.RemoveAll(artifacts.SessionRoot); err != nil {
				return nil, fault.Wrap(fault.CodeInternalError, "remove archived session", err, "database pruned; remove session files using verified backup")
			}
			for _, path := range artifacts.Checkpoints {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return nil, fault.Wrap(fault.CodeInternalError, "remove checkpoint", err, "database pruned; remove checkpoint files using verified backup")
				}
			}
			return map[string]any{"session_id": id, "pruned": true, "backup": backup}, nil
		}
	case cliCommandTask:
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "%s needs action", cliCommandTask)
		}
		action := cliVerb(args[1])
		switch action {
		case cliVerbAdd:
			sid, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			path, err := option(args[3:], cliFlagFile)
			if err != nil {
				return nil, err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			timeout := 0
			if value, ok := optional(args[3:], cliFlagTimeout); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, fault.Wrap(fault.CodeInvalidRequest, "parse task addition timeout", err, "invalid %s", cliFlagTimeout)
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionTaskAdd, SessionID: sid, File: path, Timeout: timeout}, true)
		case cliVerbRun, cliVerbRetry, cliVerbReply:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			timeout := 900
			if value, ok := optional(args[3:], cliFlagTimeout); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, err
				}
			}
			reply := ""
			if action == cliVerbReply {
				reply, err = option(args[3:], cliFlagText)
				if err != nil {
					return nil, err
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionTaskRun, TaskID: id, Reply: reply, Retry: action == cliVerbRetry, Timeout: timeout}, true)
		case cliVerbShow, cliVerbWait, cliVerbVerify, cliVerbReconcile:
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			if action == cliVerbVerify {
				return ClientRequest(ctx, home, Request{Action: actionTaskVerify, TaskID: id}, true)
			}
			if action == cliVerbReconcile {
				return ClientRequest(ctx, home, Request{Action: actionTaskReconcile, TaskID: id}, true)
			}
			if action == cliVerbShow {
				return ClientRequest(ctx, home, Request{Action: actionTaskShow, TaskID: id}, true)
			}
			for {
				result, err := ClientRequest(ctx, home, Request{Action: actionTaskShow, TaskID: id}, true)
				if err != nil {
					return nil, err
				}
				done, err := taskWaitDone(result)
				if err != nil {
					return nil, err
				}
				if done {
					return result, nil
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
				}
			}
		}
	case cliCommandCheckpoint:
		if len(args) >= 3 && cliVerb(args[1]) == cliVerbRestore {
			return ClientRequest(ctx, home, Request{Action: actionCheckpointRestore, CheckpointID: args[2]}, true)
		}
	}
	return nil, fault.New(fault.CodeInvalidRequest, "unknown command")
}

// taskWaitDone is the only place the dynamic CLI response is interpreted as a
// task state. A malformed response must fail instead of polling forever.
func taskWaitDone(result map[string]any) (bool, error) {
	task, taskOK := result["task"].(map[string]any)
	active, activeOK := result["active"].(bool)
	status, statusOK := task["status"].(string)
	if !taskOK || !activeOK || !statusOK || status == "" {
		return false, fault.New(fault.CodeDaemonUnavailable, "invalid task status response")
	}
	return !active && lifecycle.WaitStops(status), nil
}

func CLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	result, err := runCLI(ctx, args)
	if err != nil {
		detail := map[string]any{"code": contracts.ErrorCode(err), "message": err.Error()}
		var typed *fault.Error
		if errors.As(err, &typed) && typed.RequestID != "" {
			detail["request_id"] = typed.RequestID
		}
		body, encodeErr := json.Marshal(map[string]any{"error": detail})
		if encodeErr != nil {
			observe.LogFailure(ctx, "CLI error encoding failed", fault.Wrap(fault.CodeInternalError, "encode CLI error", encodeErr, "CLI error could not be encoded"))
			return 2
		}
		if _, writeErr := fmt.Fprintln(stderr, string(body)); writeErr != nil {
			observe.LogFailure(ctx, "CLI error output failed", fault.Ensure(writeErr, "write CLI error"))
		}
		return 2
	}
	if len(args) >= 2 && cliCommand(args[0]) == cliCommandDaemon && cliVerb(args[1]) == cliVerbServe {
		return 0
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		if _, writeErr := fmt.Fprintln(stderr, err); writeErr != nil {
			observe.LogFailure(ctx, "CLI encoding error output failed", fault.Ensure(writeErr, "write CLI encoding error"))
		}
		return 2
	}
	if _, err := fmt.Fprintln(stdout, string(body)); err != nil {
		observe.LogFailure(ctx, "CLI result output failed", fault.Ensure(err, "write CLI result"))
		return 2
	}
	if result["status"] == string(engine.SessionPublicationPartial) {
		return 2
	}
	return 0
}
