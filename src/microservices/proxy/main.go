package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"math/rand"
)

// Короткий JSON для ошибок апстрима
type errResp struct {
	Error string `json:"error"`
}

func main() {
	port := env("PORT", "8000")

	monolith := mustURL(env("MONOLITH_URL", "http://monolith:8080"))
	movies := mustURL(env("MOVIES_SERVICE_URL", "http://movies-service:8081"))
	events := mustURL(env("EVENTS_SERVICE_URL", "http://events-service:8082"))

	gradual := strings.ToLower(env("GRADUAL_MIGRATION", "true")) == "true"
	percent := clampPercent(env("MOVIES_MIGRATION_PERCENT", "0"))

	// Seed for per-request canary routing (needed for MOVIES_MIGRATION_PERCENT)
	rand.Seed(time.Now().UnixNano())

	monolithProxy := newProxy(monolith)
	moviesProxy := newProxy(movies)
	eventsProxy := newProxy(events)

	mux := http.NewServeMux()

	// /health — локальный (тестам достаточно 200)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Users всегда в монолит
	mux.Handle("/api/users", monolithProxy)
	mux.Handle("/api/users/", monolithProxy)

	// Events можно проксировать (в k8s чаще роутим ingress-ом напрямую)
	mux.Handle("/api/events", eventsProxy)
	mux.Handle("/api/events/", eventsProxy)

	// Movies — Strangler Fig
	mux.HandleFunc("/api/movies", func(w http.ResponseWriter, r *http.Request) {
		chooseMovies(r, gradual, percent, monolithProxy, moviesProxy).ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/movies/", func(w http.ResponseWriter, r *http.Request) {
		// Чтобы health всегда проходил через movies-service
		if r.URL.Path == "/api/movies/health" {
			moviesProxy.ServeHTTP(w, r)
			return
		}
		chooseMovies(r, gradual, percent, monolithProxy, moviesProxy).ServeHTTP(w, r)
	})

	// Остальные /api/* — в монолит (payments/subscriptions и т.п.)
	mux.Handle("/api/", monolithProxy)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           logMW(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("proxy-service :%s gradual=%v percent=%d", port, gradual, percent)
	log.Fatal(srv.ListenAndServe())
}

func chooseMovies(r *http.Request, gradual bool, percent int, monolith, movies http.Handler) http.Handler {
	if !gradual || percent <= 0 {
		return monolith
	}
	if percent >= 100 {
		return movies
	}

	// 1) Если есть id в query — детерминированно (удобно для воспроизводимости)
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		b := bucket100("id=" + id)
		if b < percent {
			return movies
		}
		return monolith
	}

	// 2) Иначе — распределяем per-request (реально даёт 50/50 даже при одинаковом client IP)
	if rand.Intn(100) < percent {
		return movies
	}
	return monolith
}

func newProxy(target *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(target)

	transport := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
	}
	p.Transport = transport

	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("upstream error %s %s -> %v", r.Method, r.URL.Path, err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(errResp{Error: "upstream unavailable"})
	}

	return p
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func bucket100(s string) int {
	sum := sha256.Sum256([]byte(s))
	v := int(sum[0])<<8 | int(sum[1])
	return v % 100
}

func env(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func clampPercent(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	if n < 0 {
		return 0
	}
	if n > 100 {
		return 100
	}
	return n
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
