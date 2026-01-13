package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

type healthResp struct {
	Status bool `json:"status"`
}
type okResp struct {
	Status string `json:"status"`
}

func main() {
	port := env("PORT", "8082")
	brokers := strings.Split(env("KAFKA_BROKERS", "kafka:9092"), ",")

	tMovie := env("KAFKA_TOPIC_MOVIE", "movie-events")
	tUser := env("KAFKA_TOPIC_USER", "user-events")
	tPay := env("KAFKA_TOPIC_PAYMENT", "payment-events")

	wMovie := writer(brokers, tMovie)
	wUser := writer(brokers, tUser)
	wPay := writer(brokers, tPay)
	defer func() { _ = wMovie.Close(); _ = wUser.Close(); _ = wPay.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	startConsumer(ctx, brokers, tMovie, "events-movie")
	startConsumer(ctx, brokers, tUser, "events-user")
	startConsumer(ctx, brokers, tPay, "events-payment")

	mux := http.NewServeMux()

	mux.HandleFunc("/api/events/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(healthResp{Status: true})
	})

	mux.HandleFunc("/api/events/movie", func(w http.ResponseWriter, r *http.Request) {
		handleEvent(w, r, wMovie, "movie")
	})
	mux.HandleFunc("/api/events/user", func(w http.ResponseWriter, r *http.Request) {
		handleEvent(w, r, wUser, "user")
	})
	mux.HandleFunc("/api/events/payment", func(w http.ResponseWriter, r *http.Request) {
		handleEvent(w, r, wPay, "payment")
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           logMW(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		log.Println("shutdown")
		cancel()
		_ = srv.Shutdown(context.Background())
	}()

	log.Printf("events-service :%s brokers=%v", port, brokers)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func handleEvent(w http.ResponseWriter, r *http.Request, wr *kafka.Writer, typ string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_ = r.Body.Close()

	// envelope — чтобы в логах было понятно что пришло
	msg := map[string]any{
		"type":      typ,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"payload":   json.RawMessage(body),
	}
	b, _ := json.Marshal(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := wr.WriteMessages(ctx, kafka.Message{
		Key:   []byte(typ),
		Value: b,
		Time:  time.Now(),
	}); err != nil {
		http.Error(w, "kafka unavailable", http.StatusServiceUnavailable)
		return
	}

	log.Printf("produced %s: %s", typ, string(b))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(okResp{Status: "success"})
}

func startConsumer(ctx context.Context, brokers []string, topic, group string) {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   topic,
		GroupID: group,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	go func() {
		defer func() { _ = r.Close() }()
		log.Printf("consumer started topic=%s group=%s", topic, group)

		for {
			m, err := r.ReadMessage(ctx)
			if err != nil {
				if ctx.Err() != nil {
					log.Printf("consumer stopped topic=%s", topic)
					return
				}
				log.Printf("consumer error topic=%s err=%v", topic, err)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			log.Printf("consumed topic=%s offset=%d key=%s value=%s", topic, m.Offset, string(m.Key), string(m.Value))
		}
	}()
}

func writer(brokers []string, topic string) *kafka.Writer {
	return &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		AllowAutoTopicCreation: true,
		Balancer:               &kafka.LeastBytes{},
		WriteTimeout:            3 * time.Second,
		ReadTimeout:             3 * time.Second,
	}
}

func env(k, def string) string {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def
	}
	return v
}

func logMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}
