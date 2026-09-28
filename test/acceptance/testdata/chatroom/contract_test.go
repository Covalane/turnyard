package chatroom_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatroom "example.com/turnyard-chatroom"
)

type message struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
	Text string `json:"text"`
}

func post(t *testing.T, base, path, body string) {
	t.Helper()
	response, err := http.Post(base+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("%s returned %d", path, response.StatusCode)
	}
}
func receive(ctx context.Context, base, user string) (message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/receive?user="+user, nil)
	if err != nil {
		return message{}, err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return message{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return message{}, fmt.Errorf("receive status %d", response.StatusCode)
	}
	var got message
	return got, json.NewDecoder(response.Body).Decode(&got)
}
func TestPublicChat(t *testing.T) {
	server := httptest.NewServer(chatroom.NewHandler())
	defer server.Close()
	post(t, server.URL, "/join", `{"user":"alice"}`)
	post(t, server.URL, "/join", `{"user":"bob"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan struct {
		msg message
		err error
	}, 1)
	go func() {
		msg, err := receive(ctx, server.URL, "bob")
		result <- struct {
			msg message
			err error
		}{msg, err}
	}()
	time.Sleep(80 * time.Millisecond)
	text := fmt.Sprintf("随意聊天-%d", time.Now().UnixNano())
	post(t, server.URL, "/send", fmt.Sprintf(`{"from":"alice","text":%q}`, text))
	got := <-result
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.msg.From != "alice" || got.msg.Text != text {
		t.Fatalf("wrong public message: %+v", got.msg)
	}
	fresh := httptest.NewServer(chatroom.NewHandler())
	defer fresh.Close()
	post(t, fresh.URL, "/join", `{"user":"bob"}`)
	emptyCtx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	old, err := receive(emptyCtx, fresh.URL, "bob")
	if err == nil {
		t.Fatalf("new room replayed history: %+v", old)
	}
}
