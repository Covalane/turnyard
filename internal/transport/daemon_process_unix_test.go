//go:build unix

package transport

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDaemonSurvivesLauncherProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	root := t.TempDir()
	binary := filepath.Join(root, "turnyard")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/turnyard")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	home := filepath.Join(root, "state")
	log, err := os.Create(filepath.Join(root, "launcher.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	launcher := exec.Command("sh", "-c", `"$1" --home "$2" daemon start; exec sleep 30`, "turnyard-launcher", binary, home)
	launcher.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	launcher.Stdout, launcher.Stderr = log, log
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := ClientRequest(stopCtx, home, Request{Action: actionDaemonStop}, false); err != nil {
			t.Errorf("stop test supervisor: %v", err)
		}
		if launcher.ProcessState == nil {
			_ = syscall.Kill(-launcher.Process.Pid, syscall.SIGKILL)
			_ = launcher.Wait()
		}
	}()
	var response *clientResponse
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		response, err = clientRequestResponse(ctx, home, Request{Action: actionPing}, false)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err != nil {
		t.Fatalf("supervisor did not start: %v", err)
	}
	type daemonStatus struct {
		PID int `json:"pid"`
	}
	status, err := decodeClientResult[daemonStatus](response)
	if err != nil || status.PID <= 0 {
		t.Fatalf("invalid supervisor status: %+v, %v", status, err)
	}
	group, err := syscall.Getpgid(status.PID)
	if err != nil {
		t.Fatal(err)
	}
	if group == launcher.Process.Pid {
		t.Fatal("supervisor still belongs to the launcher process group")
	}
	if err := syscall.Kill(-launcher.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatal(err)
	}
	_ = launcher.Wait()
	response, err = clientRequestResponse(ctx, home, Request{Action: actionPing}, false)
	if err != nil {
		t.Fatalf("supervisor died with launcher: %v", err)
	}
	after, err := decodeClientResult[daemonStatus](response)
	if err != nil || after.PID != status.PID {
		t.Fatalf("supervisor was replaced instead of surviving: %+v, %v", after, err)
	}
}
