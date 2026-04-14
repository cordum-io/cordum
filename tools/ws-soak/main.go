// ws-soak: WebSocket soak test for Cordum gateway connection stability.
//
// Connects N concurrent WebSocket clients, holds connections for a configurable
// duration, and reports any unexpected disconnects. Exits 1 on failure.
//
// Usage:
//
//	go run ./tools/ws-soak/ -url wss://localhost:8081/api/v1/stream -duration 10m
//	CORDUM_API_KEY=key go run ./tools/ws-soak/ -clients 20 -duration 2h
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

// ---------- flags ----------

var (
	flagURL           = flag.String("url", "wss://localhost:8081/api/v1/stream", "WebSocket endpoint URL")
	flagAPIKey        = flag.String("api-key", "", "API key (overridden by CORDUM_API_KEY env var)")
	flagClients       = flag.Int("clients", 10, "Number of concurrent WebSocket clients")
	flagDuration      = flag.Duration("duration", 10*time.Minute, "Test duration (Go duration syntax)")
	flagStatusURL     = flag.String("status-url", "https://localhost:8081/api/v1/status", "Status endpoint for cross-check")
	flagTLSSkip       = flag.Bool("tls-skip-verify", true, "Skip TLS certificate verification")
	flagVerbose       = flag.Bool("verbose", false, "Enable per-connection debug logging")
	flagStatusPoll    = flag.Duration("status-poll", 30*time.Second, "Status endpoint poll interval")
	flagReconnect     = flag.Bool("reconnect", true, "Attempt reconnection on unexpected disconnect")
	flagMaxReconnects = flag.Int("max-reconnects", 5, "Maximum reconnection attempts per client")
)

// ---------- counters ----------

var (
	totalMessages    atomic.Int64
	totalDrops       atomic.Int64
	totalReconnects  atomic.Int64
	totalMismatches  atomic.Int64
	clientsConnected atomic.Int64
)

// ---------- client ----------

func runClient(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()

	apiKey := resolveAPIKey()
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: *flagTLSSkip},
		HandshakeTimeout: 10 * time.Second,
	}
	header := http.Header{
		"X-API-Key": {apiKey},
	}

	connectStart := time.Now()
	conn, _, err := dialer.DialContext(ctx, *flagURL, header)
	if err != nil {
		log.Printf("[client-%d] initial connect failed: %v", id, err)
		totalDrops.Add(1)
		return
	}
	clientsConnected.Add(1)
	defer func() {
		clientsConnected.Add(-1)
		_ = conn.Close()
	}()

	if *flagVerbose {
		log.Printf("[client-%d] connected to %s", id, *flagURL)
	}

	// Pong tracking.
	lastPong := time.Now()
	conn.SetPongHandler(func(string) error {
		lastPong = time.Now()
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Read pump goroutine.
	readCtx, readCancel := context.WithCancel(ctx)
	go func() {
		defer readCancel()
		for {
			_, _, readErr := conn.ReadMessage()
			if readErr != nil {
				if readCtx.Err() != nil {
					return // context cancelled, expected
				}
				if *flagVerbose {
					log.Printf("[client-%d] read error: %v", id, readErr)
				}
				return
			}
			totalMessages.Add(1)
		}
	}()

	// Ping ticker.
	pingTicker := time.NewTicker(15 * time.Second)
	defer pingTicker.Stop()

	reconnectAttempts := 0

	for {
		select {
		case <-readCtx.Done():
			if ctx.Err() != nil {
				// Parent context cancelled = graceful shutdown.
				duration := time.Since(connectStart).Round(time.Millisecond)
				if *flagVerbose {
					log.Printf("[client-%d] graceful disconnect after %s", id, duration)
				}
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "soak test complete"))
				return
			}
			// Unexpected disconnect.
			totalDrops.Add(1)
			duration := time.Since(connectStart).Round(time.Millisecond)
			log.Printf("[client-%d] UNEXPECTED DISCONNECT after %s (last_pong=%s ago)",
				id, duration, time.Since(lastPong).Round(time.Millisecond))

			if !*flagReconnect || reconnectAttempts >= *flagMaxReconnects {
				log.Printf("[client-%d] giving up after %d reconnect attempts", id, reconnectAttempts)
				return
			}
			// Attempt reconnection with backoff.
			reconnectAttempts++
			backoff := time.Duration(1<<uint(reconnectAttempts-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			log.Printf("[client-%d] reconnecting in %s (attempt %d/%d)",
				id, backoff, reconnectAttempts, *flagMaxReconnects)

			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}

			newConn, _, reconErr := dialer.DialContext(ctx, *flagURL, header)
			if reconErr != nil {
				log.Printf("[client-%d] reconnect failed: %v", id, reconErr)
				continue
			}
			_ = conn.Close()
			conn = newConn
			totalReconnects.Add(1)
			connectStart = time.Now()
			lastPong = time.Now()
			conn.SetPongHandler(func(string) error {
				lastPong = time.Now()
				_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
				return nil
			})
			_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))

			// Restart read pump.
			readCtx, readCancel = context.WithCancel(ctx)
			go func() {
				defer readCancel()
				for {
					_, _, readErr := conn.ReadMessage()
					if readErr != nil {
						if readCtx.Err() != nil {
							return
						}
						return
					}
					totalMessages.Add(1)
				}
			}()

			if *flagVerbose {
				log.Printf("[client-%d] reconnected successfully", id)
			}

		case <-pingTicker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if pingErr := conn.WriteMessage(websocket.PingMessage, nil); pingErr != nil {
				if *flagVerbose {
					log.Printf("[client-%d] ping write failed: %v", id, pingErr)
				}
			}

		case <-ctx.Done():
			duration := time.Since(connectStart).Round(time.Millisecond)
			if *flagVerbose {
				log.Printf("[client-%d] shutting down after %s", id, duration)
			}
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "soak test complete"))
			return
		}
	}
}

// ---------- status cross-check ----------

func runStatusChecker(ctx context.Context, wg *sync.WaitGroup, expectedClients int) {
	defer wg.Done()

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: *flagTLSSkip},
		},
	}
	apiKey := resolveAPIKey()
	ticker := time.NewTicker(*flagStatusPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, "GET", *flagStatusURL, nil)
			if err != nil {
				continue
			}
			req.Header.Set("X-API-Key", apiKey)
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[status] request failed: %v", err)
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			var status struct {
				ActiveWSClients int `json:"active_ws_clients"`
			}
			if jsonErr := json.Unmarshal(body, &status); jsonErr != nil {
				log.Printf("[status] parse failed: %v", jsonErr)
				continue
			}
			actual := int(clientsConnected.Load())
			if abs(status.ActiveWSClients-actual) > 1 {
				totalMismatches.Add(1)
				log.Printf("[status] MISMATCH: server reports %d ws clients, we have %d connected",
					status.ActiveWSClients, actual)
			} else if *flagVerbose {
				log.Printf("[status] OK: server=%d, local=%d", status.ActiveWSClients, actual)
			}
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---------- main ----------

func main() {
	flag.Parse()

	apiKey := resolveAPIKey()
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "error: API key required — set CORDUM_API_KEY env var or use -api-key flag")
		os.Exit(1)
	}

	numClients := *flagClients
	duration := *flagDuration

	fmt.Println("=== WebSocket Soak Test ===")
	fmt.Printf("URL:       %s\n", *flagURL)
	fmt.Printf("Clients:   %d\n", numClients)
	fmt.Printf("Duration:  %s\n", duration)
	fmt.Printf("TLS Skip:  %v\n", *flagTLSSkip)
	fmt.Println()

	// Context with timeout + signal handling.
	ctx, cancel := context.WithTimeout(context.Background(), duration+10*time.Second)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			log.Printf("received signal %s, shutting down gracefully...", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	testStart := time.Now()

	// Timer for the soak duration.
	timer := time.AfterFunc(duration, func() {
		log.Printf("soak duration (%s) reached, shutting down...", duration)
		cancel()
	})
	defer timer.Stop()

	// Launch clients.
	var clientWg sync.WaitGroup
	for i := 0; i < numClients; i++ {
		clientWg.Add(1)
		go runClient(ctx, i, &clientWg)
		// Stagger connections to avoid thundering herd.
		time.Sleep(100 * time.Millisecond)
	}

	// Launch status checker.
	var statusWg sync.WaitGroup
	statusWg.Add(1)
	go runStatusChecker(ctx, &statusWg, numClients)

	// Wait for all clients to finish.
	clientWg.Wait()
	cancel() // stop status checker
	statusWg.Wait()

	elapsed := time.Since(testStart).Round(time.Second)

	// Print summary.
	drops := totalDrops.Load()
	reconnections := totalReconnects.Load()
	messages := totalMessages.Load()
	mismatches := totalMismatches.Load()

	fmt.Println()
	fmt.Println("=== WebSocket Soak Test Summary ===")
	fmt.Printf("Duration:       %s\n", elapsed)
	fmt.Printf("Clients:        %d\n", numClients)
	fmt.Printf("Messages:       %d\n", messages)
	fmt.Printf("Drops:          %d\n", drops)
	fmt.Printf("Reconnections:  %d\n", reconnections)
	fmt.Printf("Mismatches:     %d\n", mismatches)
	fmt.Println()

	if drops > 0 {
		fmt.Println("RESULT: FAIL — unexpected disconnects detected")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS — all connections held for full duration")
}

func resolveAPIKey() string {
	if envKey := os.Getenv("CORDUM_API_KEY"); envKey != "" {
		return envKey
	}
	return *flagAPIKey
}
