package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ohseeeye/oci/ocimem"
)

func TestGracefulShutdownDrainsRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("active request canceled during graceful shutdown")
		}
		_, _ = io.WriteString(w, "finished")
	})
	done := make(chan error, 1)
	go func() { done <- serve(ctx, listener, handler, 5*time.Second) }()
	client := &http.Client{Timeout: 5 * time.Second}
	response := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer resp.Body.Close()
			var body []byte
			body, err = io.ReadAll(resp.Body)
			if err == nil && string(body) != "finished" {
				err = errors.New("response did not complete")
			}
		}
		response <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server returned before active request finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
	if err := <-response; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownTimeoutClosesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	finished := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(finished)
	})
	done := make(chan error, 1)
	go func() { done <- serve(ctx, listener, handler, 20*time.Millisecond) }()
	client := &http.Client{Timeout: 5 * time.Second}
	response := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if resp != nil {
			resp.Body.Close()
		}
		response <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v; want shutdown deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not enforce shutdown timeout")
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("request connection was not closed")
	}
	if err := <-response; err == nil {
		t.Fatal("client unexpectedly completed its request")
	}
}

func TestRunRejectsInvalidTimeout(t *testing.T) {
	for _, timeout := range []time.Duration{0, -time.Second} {
		if err := Run(context.Background(), "127.0.0.1:0", ocimem.New(), timeout); err == nil {
			t.Fatalf("accepted timeout %v", timeout)
		}
	}
}

func TestRunReportsListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := Run(context.Background(), listener.Addr().String(), ocimem.New(), time.Second); err == nil {
		t.Fatal("expected an error for an occupied address")
	}
}
