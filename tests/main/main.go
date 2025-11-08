package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"github.com/gorilla/websocket"
)



const (
	// server url 
	baseURL      = "http://localhost:3001" 
	// total clients 
	totalClients = 10000       
	// login requests          
	maxInFlight  = 2000    
	channelName  = "general"              
)

type loginResponse struct {
	UserID   string `json:"userId"`
	Token    string `json:"token"`
	Username string `json:"username"`
}

func main() {
	fmt.Printf("Server:       %s\n", baseURL)
	fmt.Printf("Clients:      %d\n", totalClients)
	fmt.Printf("MaxInFlight:  %d\n", maxInFlight)
	fmt.Printf("Channel:      %s\n\n", channelName)

	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}

	// only Concurrent
	fmt.Println("Test 1: Concurrent")
	runtime.GOMAXPROCS(1)
	runLoad(u)

	// Concurrent + parallel
	fmt.Println("\nTest 2: Concurrent + parallel")
	runtime.GOMAXPROCS(runtime.NumCPU())
	runLoad(u)
}


// core load testing 
func runLoad(base *url.URL) {
	start := time.Now()

	var success int64
	var failed int64

	var wg sync.WaitGroup
	wg.Add(totalClients)

	// Semaphore to avoid too many simultaneous dials/logins
	sem := make(chan struct{}, maxInFlight)

	for i := 0; i < totalClients; i++ {
		i := i
		go func() {
			defer wg.Done()
			sem <- struct{}{}         // acquire
			defer func() { <-sem }() // release

			if err := connectOneClient(i, base); err != nil {
				atomic.AddInt64(&failed, 1)
				fmt.Fprintf(os.Stderr, "client %d error: %v\n", i, err)
				return
			}
			atomic.AddInt64(&success, 1)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Println("----- Result -----")
	fmt.Printf("Total clients:        %d\n", totalClients)
	fmt.Printf("Successful:           %d\n", success)
	fmt.Printf("Failed:               %d\n", failed)
	fmt.Printf("Total time:           %s\n", elapsed)
	fmt.Printf("Avg per client:       %s\n", time.Duration(int64(elapsed)/int64(totalClients)))
}


// connectOneClient 
// POST req /login
// WS connect
// send one chat message
// close
func connectOneClient(idx int, base *url.URL) error {
	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	username := fmt.Sprintf("user-%d", idx)

	// Login
	lr, err := doLogin(httpClient, base, username)
	if err != nil {
		return fmt.Errorf("login failed for %s: %w", username, err)
	}

	// WebSocket connect
	wsURL := makeWSURL(base, lr.Token, channelName)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial failed for %s: %w", username, err)
	}
	defer conn.Close()

	// Send a simple chat message
	msg := map[string]any{
		"type":    "chat",
		"content": fmt.Sprintf("hello from %s", username),
	}

	if err := conn.WriteJSON(msg); err != nil {
		return fmt.Errorf("write chat failed for %s: %w", username, err)
	}

	return nil
}


func doLogin(client *http.Client, base *url.URL, username string) (loginResponse, error) {
	loginURL := base.ResolveReference(&url.URL{Path: "/login"})

	payload := map[string]string{"username": username}
	buf, err := json.Marshal(payload)
	if err != nil {
		return loginResponse{}, fmt.Errorf("marshal login body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, loginURL.String(), bytes.NewReader(buf))
	if err != nil {
		return loginResponse{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return loginResponse{}, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return loginResponse{}, fmt.Errorf("login status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var lr loginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return loginResponse{}, fmt.Errorf("decode login response: %w", err)
	}
	return lr, nil
}

func makeWSURL(base *url.URL, token, channel string) string {
	wsScheme := "ws"
	if base.Scheme == "https" {
		wsScheme = "wss"
	}
	wsURL := &url.URL{
		Scheme: wsScheme,
		Host:   base.Host,
		Path:   "/ws",
	}
	q := wsURL.Query()
	q.Set("token", token)
	q.Set("channel", channel)
	wsURL.RawQuery = q.Encode()
	return wsURL.String()
}
