package events

import (
	"bufio"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublishReachesSubscribers(t *testing.T) {
	h := New()
	ch, unsub := h.Subscribe()
	defer unsub()
	h.Publish(Player, "")
	select {
	case ev := <-ch:
		if ev.Type != Player {
			t.Fatalf("got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no event")
	}
	unsub()
	if h.Clients() != 0 {
		t.Fatal("client still registered")
	}
	// A second unsubscribe is harmless.
	unsub()
}

func TestSlowClientIsDropped(t *testing.T) {
	h := New()
	ch, _ := h.Subscribe()
	for i := 0; i < 40; i++ {
		h.Publish(Library, "x")
	}
	if h.Clients() != 0 {
		t.Fatal("slow client should be dropped")
	}
	// The channel is closed, so a receive does not block.
	for range ch {
	}
}

func TestServeHTTPStreams(t *testing.T) {
	h := New()
	ts := httptest.NewServer(h)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	// Wait for the subscription, then publish.
	deadline := time.Now().Add(2 * time.Second)
	for h.Clients() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	h.Publish(Queue, "")
	r := bufio.NewReader(resp.Body)
	var lines []string
	for len(lines) < 3 {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	if lines[0] != "retry: 3000" || lines[2] != `data: {"type":"queue"}` {
		t.Fatalf("got %q", lines)
	}
}
