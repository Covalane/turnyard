//go:build private

package chatroom_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	chatroom "example.com/turnyard-chatroom"
)

func TestPrivateChat(t *testing.T) {
	server := httptest.NewServer(chatroom.NewHandler())
	defer server.Close()
	for _, user := range []string{"alice", "bob", "carol"} {
		post(t, server.URL, "/join", `{"user":"`+user+`"}`)
	}
	bobCtx, bobCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer bobCancel()
	carolCtx, carolCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer carolCancel()
	bob := make(chan message, 1)
	bobError := make(chan error, 1)
	go func() { msg, err := receive(bobCtx, server.URL, "bob"); bob <- msg; bobError <- err }()
	carol := make(chan message, 1)
	go func() { msg, _ := receive(carolCtx, server.URL, "carol"); carol <- msg }()
	time.Sleep(80 * time.Millisecond)
	post(t, server.URL, "/private", `{"from":"alice","to":"bob","text":"only bob can read this"}`)
	got := <-bob
	if err := <-bobError; err != nil {
		t.Fatal(err)
	}
	if got.From != "alice" || got.To != "bob" || got.Text != "only bob can read this" {
		t.Fatalf("wrong private message: %+v", got)
	}
	leaked := <-carol
	if leaked.Text != "" {
		t.Fatalf("private message leaked to carol: %+v", leaked)
	}
}
