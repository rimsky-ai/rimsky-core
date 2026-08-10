// An HTTP endpoint that holds a request open until the run releases it, so a
// dispatch can be provably in flight without any wall-clock guess.
//
//	GET /hold     -> blocks until /release, then answers 200
//	GET /release  -> releases every held request
//	GET /status   -> {"held":N,"released":M}
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
)

type gate struct {
	mu       sync.Mutex
	held     int
	released int
	ch       chan struct{}
}

func main() {
	port := os.Getenv("SLOW_PORT")
	if port == "" {
		port = "8000"
	}
	g := &gate{ch: make(chan struct{})}

	http.HandleFunc("/hold", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.held++
		wait := g.ch
		g.mu.Unlock()
		select {
		case <-wait:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("content-type", "application/json")
		fmt.Fprint(w, `{"ok":true,"source":"slow-server"}`)
	})

	http.HandleFunc("/release", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		close(g.ch)
		g.ch = make(chan struct{})
		g.released += g.held
		g.held = 0
		g.mu.Unlock()
		fmt.Fprint(w, `{"released":true}`)
	})

	http.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		out := map[string]int{"held": g.held, "released": g.released}
		g.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Fprintf(os.Stderr, "slow-server: %v\n", err)
		os.Exit(1)
	}
}
