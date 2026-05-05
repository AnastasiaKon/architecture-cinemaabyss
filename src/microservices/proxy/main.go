package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port                   string
	MonolithURL            string
	MoviesServiceURL       string
	EventsServiceURL       string
	GradualMigration       bool
	MoviesMigrationPercent int
}

func main() {
	rand.Seed(time.Now().UnixNano())

	cfg := loadConfig()

	monolithProxy := mustCreateReverseProxy("monolith", cfg.MonolithURL)
	moviesProxy := mustCreateReverseProxy("movies-service", cfg.MoviesServiceURL)
	eventsProxy := mustCreateReverseProxy("events-service", cfg.EventsServiceURL)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/movies"):
			if shouldRouteToMovies(cfg) {
				log.Printf("proxy: %s %s -> movies-service", r.Method, r.URL.String())
				moviesProxy.ServeHTTP(w, r)
				return
			}

			log.Printf("proxy: %s %s -> monolith", r.Method, r.URL.String())
			monolithProxy.ServeHTTP(w, r)
			return

		case strings.HasPrefix(r.URL.Path, "/api/events"):
			log.Printf("proxy: %s %s -> events-service", r.Method, r.URL.String())
			eventsProxy.ServeHTTP(w, r)
			return

		default:
			log.Printf("proxy: %s %s -> monolith", r.Method, r.URL.String())
			monolithProxy.ServeHTTP(w, r)
			return
		}
	})

	addr := ":" + cfg.Port
	log.Printf("Starting CinemaAbyss proxy on port %s", cfg.Port)
	log.Printf("MONOLITH_URL=%s", cfg.MonolithURL)
	log.Printf("MOVIES_SERVICE_URL=%s", cfg.MoviesServiceURL)
	log.Printf("EVENTS_SERVICE_URL=%s", cfg.EventsServiceURL)
	log.Printf("GRADUAL_MIGRATION=%t", cfg.GradualMigration)
	log.Printf("MOVIES_MIGRATION_PERCENT=%d", cfg.MoviesMigrationPercent)

	log.Fatal(http.ListenAndServe(addr, mux))
}

func loadConfig() Config {
	port := getEnv("PORT", "8000")

	migrationPercent, err := strconv.Atoi(getEnv("MOVIES_MIGRATION_PERCENT", "0"))
	if err != nil {
		migrationPercent = 0
	}
	if migrationPercent < 0 {
		migrationPercent = 0
	}
	if migrationPercent > 100 {
		migrationPercent = 100
	}

	gradualMigration, err := strconv.ParseBool(getEnv("GRADUAL_MIGRATION", "true"))
	if err != nil {
		gradualMigration = true
	}

	return Config{
		Port:                   port,
		MonolithURL:            getEnv("MONOLITH_URL", "http://localhost:8080"),
		MoviesServiceURL:       getEnv("MOVIES_SERVICE_URL", "http://localhost:8081"),
		EventsServiceURL:       getEnv("EVENTS_SERVICE_URL", "http://localhost:8082"),
		GradualMigration:       gradualMigration,
		MoviesMigrationPercent: migrationPercent,
	}
}

func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(map[string]bool{
		"status": true,
	})
}

func shouldRouteToMovies(cfg Config) bool {
	if !cfg.GradualMigration {
		return false
	}

	if cfg.MoviesMigrationPercent <= 0 {
		return false
	}

	if cfg.MoviesMigrationPercent >= 100 {
		return true
	}

	return rand.Intn(100) < cfg.MoviesMigrationPercent
}

func mustCreateReverseProxy(name string, rawTarget string) *httputil.ReverseProxy {
	target, err := url.Parse(rawTarget)
	if err != nil {
		log.Fatalf("invalid target URL for %s: %s: %v", name, rawTarget, err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)

		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		req.Header.Set("X-Forwarded-Host", req.Host)
		req.Header.Set("X-Proxy-Service", "cinemaabyss-proxy")
	}

	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("proxy error for %s %s via %s: %v", r.Method, r.URL.String(), name, err)
		http.Error(w, "upstream service unavailable", http.StatusBadGateway)
	}

	return proxy
}
