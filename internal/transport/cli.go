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
	"github.com/Covalane/turnyard/internal/sandbox"
	"github.com/Covalane/turnyard/internal/stateops"
	"github.com/Covalane/turnyard/internal/store"
)

func option(args []string, name string) (string, error) {
	for i, arg := range args {
		if arg == name {
			if i+1 >= len(args) {
				return "", fault.New(fault.CodeInvalidRequest, "%s needs a value", name)
			}
			return args[i+1], nil
		}
	}
	return "", fault.New(fault.CodeInvalidRequest, "missing %s", name)
}
func optional(args []string, name string) (string, bool) {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
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
	if len(args) >= 2 && args[0] == "--home" {
		home = args[1]
		args = args[2:]
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	home = abs
	if len(args) == 0 {
		return nil, fault.New(fault.CodeInvalidRequest, "usage: turnyard [--home DIR] doctor|daemon|session|task|checkpoint|state")
	}
	switch args[0] {
	case "state":
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "state needs backup, verify, or restore")
		}
		switch args[1] {
		case "backup":
			destination, err := option(args[2:], "--out")
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
		case "verify":
			source, err := option(args[2:], "--from")
			if err != nil {
				return nil, err
			}
			manifest, err := stateops.Verify(ctx, source)
			if err != nil {
				return nil, err
			}
			return map[string]any{"backup": source, "format": manifest.Format, "entries": len(manifest.Entries), "source": manifest.Source}, nil
		case "restore":
			source, err := option(args[2:], "--from")
			if err != nil {
				return nil, err
			}
			destination, err := option(args[2:], "--to")
			if err != nil {
				return nil, err
			}
			if err := stateops.Restore(ctx, source, destination); err != nil {
				return nil, err
			}
			return map[string]any{"restored": true, "home": destination}, nil
		default:
			return nil, fault.New(fault.CodeInvalidRequest, "unknown state action")
		}
	case "doctor":
		probes := map[string]any{}
		for _, name := range sandbox.BuiltinBackends() {
			b, err := sandbox.Backend(name)
			if err != nil {
				return nil, err
			}
			p, err := b.Probe(ctx)
			if err != nil {
				observe.LogFailure(ctx, "sandbox doctor probe failed", err, "backend", name)
				p["errorCode"] = fault.CodeOf(err)
			}
			probes[name] = p
		}
		credentials := map[string]bool{}
		for _, name := range []string{"OLLAMA_API_KEY", "OLLAMA_2_API_KEY", "DEEPSEEK_API_KEY", "OPENAI_API_KEY"} {
			credentials[name] = os.Getenv(name) != ""
		}
		probes["credentialEnvironmentPresent"] = credentials
		probes["agentRuntimes"] = registry.Runtimes()
		probes["home"] = home
		return probes, nil
	case "daemon":
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "daemon needs action")
		}
		switch args[1] {
		case "serve":
			value, err := option(args[2:], "--home")
			if err != nil {
				return nil, err
			}
			return nil, Serve(ctx, value)
		case "start":
			if err := StartDaemon(ctx, home); err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionPing}, false)
		case "status":
			return ClientRequest(ctx, home, Request{Action: actionPing}, false)
		case "stop":
			return ClientRequest(ctx, home, Request{Action: actionDaemonStop}, false)
		default:
			return nil, fault.New(fault.CodeInvalidRequest, "unknown daemon action")
		}
	case "session":
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "session needs action")
		}
		switch args[1] {
		case "create":
			path, err := option(args[2:], "--file")
			if err != nil {
				return nil, err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			timeout := 0
			if value, ok := optional(args[2:], "--timeout"); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, fault.Wrap(fault.CodeInvalidRequest, "parse session timeout", err, "invalid --timeout")
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionCreate, File: path, Timeout: timeout}, true)
		case "show":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionShow, SessionID: id}, true)
		case "complete":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionComplete, SessionID: id}, true)
		case "publish":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			timeout := 600
			if value, ok := optional(args[3:], "--timeout"); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, fault.Wrap(fault.CodeInvalidRequest, "parse publish timeout", err, "invalid --timeout")
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionPublish, SessionID: id, Timeout: timeout}, true)
		case "cancel":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			reason, err := option(args[3:], "--reason")
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionSessionCancel, SessionID: id, Reason: reason}, true)
		case "events":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			after := int64(0)
			if value, ok := optional(args[3:], "--after"); ok {
				after, err = strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, err
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionEvents, SessionID: id, After: after}, true)
		case "prune":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			backup, err := option(args[3:], "--backup")
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
			return map[string]any{"sessionId": id, "pruned": true, "backup": backup}, nil
		}
	case "task":
		if len(args) < 2 {
			return nil, fault.New(fault.CodeInvalidRequest, "task needs action")
		}
		action := args[1]
		switch action {
		case "add":
			sid, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			path, err := option(args[3:], "--file")
			if err != nil {
				return nil, err
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return nil, err
			}
			return ClientRequest(ctx, home, Request{Action: actionTaskAdd, SessionID: sid, File: path}, true)
		case "run", "retry", "reply":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			timeout := 900
			if value, ok := optional(args[3:], "--timeout"); ok {
				timeout, err = strconv.Atoi(value)
				if err != nil {
					return nil, err
				}
			}
			reply := ""
			if action == "reply" {
				reply, err = option(args[3:], "--text")
				if err != nil {
					return nil, err
				}
			}
			return ClientRequest(ctx, home, Request{Action: actionTaskRun, TaskID: id, Reply: reply, Retry: action == "retry", Timeout: timeout}, true)
		case "show", "wait", "verify", "reconcile":
			id, err := positional(args, 2)
			if err != nil {
				return nil, err
			}
			if action == "verify" {
				return ClientRequest(ctx, home, Request{Action: actionTaskVerify, TaskID: id}, true)
			}
			if action == "reconcile" {
				return ClientRequest(ctx, home, Request{Action: actionTaskReconcile, TaskID: id}, true)
			}
			if action == "show" {
				return ClientRequest(ctx, home, Request{Action: actionTaskShow, TaskID: id}, true)
			}
			for {
				result, err := ClientRequest(ctx, home, Request{Action: actionTaskShow, TaskID: id}, true)
				if err != nil {
					return nil, err
				}
				task, ok := result["task"].(map[string]any)
				if ok && result["active"] == false {
					status, _ := task["status"].(string)
					if lifecycle.WaitStops(status) {
						return result, nil
					}
				}
				time.Sleep(time.Second)
			}
		}
	case "checkpoint":
		if len(args) >= 3 && args[1] == "restore" {
			return ClientRequest(ctx, home, Request{Action: actionCheckpointRestore, CheckpointID: args[2]}, true)
		}
	}
	return nil, fault.New(fault.CodeInvalidRequest, "unknown command")
}
func CLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	result, err := runCLI(ctx, args)
	if err != nil {
		detail := map[string]any{"code": contracts.ErrorCode(err), "message": err.Error()}
		var typed *fault.Error
		if errors.As(err, &typed) && typed.RequestID != "" {
			detail["requestId"] = typed.RequestID
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
	if len(args) >= 2 && args[0] == "daemon" && args[1] == "serve" {
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
