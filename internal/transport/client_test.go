package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"testing"

	"github.com/Covalane/turnyard/internal/fault"
)

func TestUnavailableSupervisorKeepsClientRequestID(t *testing.T) {
	_, err := ClientRequest(context.Background(), t.TempDir(), Request{Action: actionPing, RequestID: "req_trace_test"}, false)
	if fault.CodeOf(err) != fault.CodeDaemonUnavailable {
		t.Fatalf("unexpected client error: %v", err)
	}
	var typed *fault.Error
	if !errors.As(err, &typed) || typed.RequestID != "req_trace_test" || fault.Origin(err) == "" {
		t.Fatalf("client error cannot be correlated: %v", err)
	}
}

func TestClientKeepsRawResultForTypedTaskStatus(t *testing.T) {
	home := t.TempDir()
	path := SocketPath(home)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer os.Remove(path)
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			done <- err
			return
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			done <- err
			return
		}
		_, err = conn.Write([]byte(`{"ok":true,"request_id":"` + req.RequestID + `","result":{"task":{"status":"verified"},"active":false}}` + "\n"))
		done <- err
	}()
	response, err := clientRequestResponse(context.Background(), home, Request{Action: actionTaskShow, RequestID: "req_typed"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "req_typed" || string(response.Result) != `{"task":{"status":"verified"},"active":false}` {
		t.Fatalf("client changed response bytes: %+v", response)
	}
	state, err := decodeClientResult[taskWaitResult](response)
	if err != nil {
		t.Fatal(err)
	}
	if finished, err := taskWaitDone(state); err != nil || !finished {
		t.Fatalf("task status not decoded: %v %v", finished, err)
	}
}
