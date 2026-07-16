package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"log-server/internal/models"
)

// Broker is a concurrency-safe pub/sub hub for Server-Sent Events.
type Broker struct {
	mu        sync.RWMutex
	clients   map[chan []byte]struct{}
	register  chan chan []byte
	unreg     chan chan []byte
	broadcast chan []byte
	done      chan struct{}
}

// NewBroker creates and starts an SSE broker.
func NewBroker() *Broker {
	b := &Broker{
		clients:   make(map[chan []byte]struct{}),
		register:  make(chan chan []byte, 16),
		unreg:     make(chan chan []byte, 16),
		broadcast: make(chan []byte, 256),
		done:      make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *Broker) run() {
	for {
		select {
		case c := <-b.register:
			b.mu.Lock()
			b.clients[c] = struct{}{}
			n := len(b.clients)
			b.mu.Unlock()
			slog.Debug("sse client connected", "total", n)

		case c := <-b.unreg:
			b.mu.Lock()
			if _, ok := b.clients[c]; ok {
				delete(b.clients, c)
				close(c)
			}
			n := len(b.clients)
			b.mu.Unlock()
			slog.Debug("sse client disconnected", "total", n)

		case msg := <-b.broadcast:
			b.mu.RLock()
			for c := range b.clients {
				select {
				case c <- msg:
				default:
					slog.Debug("sse client slow, dropping message")
				}
			}
			b.mu.RUnlock()

		case <-b.done:
			b.mu.Lock()
			for c := range b.clients {
				close(c)
			}
			b.clients = make(map[chan []byte]struct{})
			b.mu.Unlock()
			return
		}
	}
}

// Publish broadcasts an event to all connected clients.
func (b *Broker) Publish(event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse marshal failed", "err", err)
		return
	}
	msg := fmt.Appendf(nil, "event: %s\ndata: %s\n\n", event, data)
	select {
	case b.broadcast <- msg:
	default:
		slog.Warn("sse broadcast channel full, dropping event")
	}
}

// PublishEvent is a convenience for models.SSEEvent.
func (b *Broker) PublishEvent(e models.SSEEvent) {
	b.Publish(e.Type, e.Data)
}

// Stop gracefully shuts down the broker.
func (b *Broker) Stop() {
	close(b.done)
}

// ClientCount returns the number of currently connected clients.
func (b *Broker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

// ServeHTTP handles GET /api/events for legacy SSE support.
// New clients should use polling via /api/healthz or /api/logs.
func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := make(chan []byte, 64)
	b.register <- client

	ctx := r.Context()
	defer func() {
		b.unreg <- client
	}()

	// Send hello immediately after registration
	hello, _ := json.Marshal(map[string]any{"time": time.Now().UTC().Unix(), "clients": b.ClientCount()})
	fmt.Fprintf(w, "event: hello\ndata: %s\n\n", hello)
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-client:
			if !ok {
				return
			}
			if _, err := w.Write(msg); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
