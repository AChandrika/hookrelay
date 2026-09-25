// mockreceiver is a fake webhook receiver for local testing. Each path fails
// (or succeeds) in a specific way, so you can watch the worker handle it.
//
//	/ok                 always 200
//	/status/{code}      always returns that code (429 and 503 add Retry-After: 10)
//	/flaky?fail=N       fails the first N attempts of each webhook-id, then 200
//	/slow?ms=N          waits N milliseconds, then 200 (test timeouts and crashes)
//	/verify             checks the signature using the secret set via PUT /_secret
//	PUT /_secret        body = the endpoint's whsec_ secret
package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"hookrelay/internal/signing"
)

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}

	var mu sync.Mutex
	seen := map[string]int{} // webhook-id -> times received
	secret := ""

	// record logs the request and returns how many times this webhook-id has
	// arrived. Seeing the same id twice is at-least-once delivery in action.
	record := func(r *http.Request) int {
		id := r.Header.Get("webhook-id")
		mu.Lock()
		seen[id]++
		n := seen[id]
		mu.Unlock()
		log.Printf("%-18s webhook-id=%s delivery #%d", r.URL.Path, id, n)
		return n
	}

	mux := http.NewServeMux()

	mux.HandleFunc("PUT /_secret", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		secret = strings.TrimSpace(string(b))
		mu.Unlock()
		log.Printf("signing secret updated")
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /ok", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /status/{code}", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		code, err := strconv.Atoi(r.PathValue("code"))
		if err != nil || code < 100 || code > 599 {
			code = http.StatusInternalServerError
		}
		if code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable {
			w.Header().Set("Retry-After", "10")
		}
		http.Error(w, http.StatusText(code), code)
	})

	mux.HandleFunc("POST /flaky", func(w http.ResponseWriter, r *http.Request) {
		n := record(r)
		fail, _ := strconv.Atoi(r.URL.Query().Get("fail"))
		if n <= fail {
			http.Error(w, "flaky failure", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /slow", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		ms, _ := strconv.Atoi(r.URL.Query().Get("ms"))
		time.Sleep(time.Duration(ms) * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("POST /verify", func(w http.ResponseWriter, r *http.Request) {
		record(r)
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		s := secret
		mu.Unlock()
		if s == "" {
			http.Error(w, "no secret set; PUT /_secret first", http.StatusUnauthorized)
			return
		}
		if err := signing.Verify(s, r.Header, body, time.Now()); err != nil {
			log.Printf("  signature INVALID: %v", err)
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		log.Printf("  signature valid")
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("mock receiver listening on http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
