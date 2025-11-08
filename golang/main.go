package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	_ "modernc.org/sqlite" 
)



type User struct {
	ID       string
	Username string
	Token    string
}

type ClientMeta struct {
	UserID    string
	Username  string
	ChannelID string
	Conn      *websocket.Conn
	IsAlive   bool
// per conn mutex
	WriteMu sync.Mutex 
}

type IncomingBase struct {
	Type string `json:"type"`
}

type IncomingChat struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type IncomingTyping struct {
	Type     string `json:"type"`
	IsTyping bool   `json:"isTyping"`
}

type IncomingPing struct {
	Type string `json:"type"`
}

type OutgoingChat struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}

type OutgoingTyping struct {
	Type      string `json:"type"`
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	IsTyping  bool   `json:"isTyping"`
}

type OutgoingSystem struct {
	Type      string `json:"type"`
	Event     string `json:"event"` 
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
}

type OutgoingPong struct {
	Type string `json:"type"`
}

type OutgoingError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type HistoryMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channelId"`
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
}


// sqllite setuup
var (
	db                     *sql.DB
	insertUserStmt         *sql.Stmt
	getUserByTokenStmt     *sql.Stmt
	insertMessageStmt      *sql.Stmt
	getMessagesForChanStmt *sql.Stmt
)

func initDB() {
	var err error

	db, err = sql.Open("sqlite", "file:chat.db?_foreign_keys=on")
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// single conn to prevent db busy
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA busy_timeout = 5000;
	`); err != nil {
		log.Fatalf("set pragmas: %v", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL,
		token TEXT NOT NULL UNIQUE
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		channel_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		username TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		log.Fatalf("create tables: %v", err)
	}

	insertUserStmt, err = db.Prepare(`INSERT INTO users (id, username, token) VALUES (?, ?, ?)`)
	if err != nil {
		log.Fatalf("prepare insertUserStmt: %v", err)
	}

	getUserByTokenStmt, err = db.Prepare(`SELECT id, username, token FROM users WHERE token = ?`)
	if err != nil {
		log.Fatalf("prepare getUserByTokenStmt: %v", err)
	}

	insertMessageStmt, err = db.Prepare(`
		INSERT INTO messages (id, channel_id, user_id, username, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		log.Fatalf("prepare insertMessageStmt: %v", err)
	}

	getMessagesForChanStmt, err = db.Prepare(`
		SELECT id, channel_id, user_id, username, content, created_at
		FROM messages
		WHERE channel_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`)
	if err != nil {
		log.Fatalf("prepare getMessagesForChanStmt: %v", err)
	}
}

func createUser(username string) (*User, error) {
	u := &User{
		ID:       uuid.NewString(),
		Username: username,
		Token:    uuid.NewString(),
	}
	if _, err := insertUserStmt.Exec(u.ID, u.Username, u.Token); err != nil {
		return nil, err
	}
	return u, nil
}

func getUserByToken(token string) (*User, error) {
	row := getUserByTokenStmt.QueryRow(token)
	u := &User{}
	if err := row.Scan(&u.ID, &u.Username, &u.Token); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func insertMessage(channelID, userID, username, content, createdAt string) (string, error) {
	id := uuid.NewString()
	if _, err := insertMessageStmt.Exec(id, channelID, userID, username, content, createdAt); err != nil {
		return "", err
	}
	return id, nil
}

func getMessagesForChannel(channelID string, limit int) ([]HistoryMessage, error) {
	rows, err := getMessagesForChanStmt.Query(channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []HistoryMessage
	for rows.Next() {
		var m HistoryMessage
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.UserID, &m.Username, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

// reveerse res
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	return msgs, nil
}



var (
	clients   = make(map[*websocket.Conn]*ClientMeta)
	clientsMu sync.RWMutex

	channelSubs   = make(map[string]map[*websocket.Conn]struct{})
	channelSubsMu sync.RWMutex
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true 
	},
}


// http handler
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	username := strings.TrimSpace(body.Username)
	if len(username) < 2 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "username is required (min 2 chars)",
		})
		return
	}

	user, err := createUser(username)
	if err != nil {
		log.Printf("createUser error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]string{
		"userId":   user.ID,
		"token":    user.Token,
		"username": user.Username,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func historyHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}


	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 || parts[1] != "channels" || parts[3] != "history" {
		http.NotFound(w, r)
		return
	}
	channelID := parts[2]

	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	msgs, err := getMessagesForChannel(channelID, limit)
	if err != nil {
		log.Printf("getMessagesForChannel error: %v", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	resp := struct {
		ChannelID string           `json:"channelId"`
		Messages  []HistoryMessage `json:"messages"`
	}{
		ChannelID: channelID,
		Messages:  msgs,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}



func wsHandler(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	channel := r.URL.Query().Get("channel")

	if token == "" || channel == "" {
		http.Error(w, "Missing token or channel", http.StatusBadRequest)
		return
	}

	user, err := getUserByToken(token)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	channelID := strings.TrimSpace(channel)
	if channelID == "" {
		http.Error(w, "Invalid channel", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}

	meta := &ClientMeta{
		UserID:    user.ID,
		Username:  user.Username,
		ChannelID: channelID,
		Conn:      conn,
		IsAlive:   true,
	}

	// register user
	clientsMu.Lock()
	clients[conn] = meta
	clientsMu.Unlock()

	channelSubsMu.Lock()
	subs := channelSubs[channelID]
	if subs == nil {
		subs = make(map[*websocket.Conn]struct{})
		channelSubs[channelID] = subs
	}
	subs[conn] = struct{}{}
	channelSubsMu.Unlock()

	log.Printf("Client connected: user=%s channel=%s", meta.Username, meta.ChannelID)

	//  mark alive on pong
	conn.SetPongHandler(func(appData string) error {
		clientsMu.Lock()
		if m, ok := clients[conn]; ok {
			m.IsAlive = true
		}
		clientsMu.Unlock()
		return nil
	})

	// to send "joined" message to all
	_ = broadcastToChannel(meta.ChannelID, OutgoingSystem{
		Type:      "system",
		Event:     "joined",
		ChannelID: meta.ChannelID,
		UserID:    meta.UserID,
		Username:  meta.Username,
	}, nil)

	go func() {
		defer func() {
			handleDisconnect(conn)
		}()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("read error: %v", err)
				}
				return
			}
			handleIncomingMessage(conn, data)
		}
	}()
}

func handleIncomingMessage(conn *websocket.Conn, raw []byte) {
	clientsMu.RLock()
	meta, ok := clients[conn]
	clientsMu.RUnlock()
	if !ok {
		_ = conn.Close()
		return
	}

	var base IncomingBase
	if err := json.Unmarshal(raw, &base); err != nil {
		_ = sendJSON(conn, OutgoingError{
			Type:    "error",
			Message: "Invalid JSON",
		})
		return
	}

	switch base.Type {
	case "ping":
		_ = sendJSON(conn, OutgoingPong{Type: "pong"})
	case "typing":
		var msg IncomingTyping
		if err := json.Unmarshal(raw, &msg); err != nil {
			_ = sendJSON(conn, OutgoingError{
				Type:    "error",
				Message: "Invalid typing message",
			})
			return
		}
		out := OutgoingTyping{
			Type:      "typing",
			ChannelID: meta.ChannelID,
			UserID:    meta.UserID,
			Username:  meta.Username,
			IsTyping:  msg.IsTyping,
		}
		_ = broadcastToChannel(meta.ChannelID, out, conn) 
	case "chat":
		var msg IncomingChat
		if err := json.Unmarshal(raw, &msg); err != nil {
			_ = sendJSON(conn, OutgoingError{
				Type:    "error",
				Message: "Invalid chat message",
			})
			return
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			_ = sendJSON(conn, OutgoingError{
				Type:    "error",
				Message: "Empty message",
			})
			return
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		id, err := insertMessage(meta.ChannelID, meta.UserID, meta.Username, content, now)
		if err != nil {
			log.Printf("insertMessage error: %v", err)
			_ = sendJSON(conn, OutgoingError{
				Type:    "error",
				Message: "Failed to persist message",
			})
			return
		}

		out := OutgoingChat{
			Type:      "chat",
			ID:        id,
			ChannelID: meta.ChannelID,
			UserID:    meta.UserID,
			Username:  meta.Username,
			Content:   content,
			CreatedAt: now,
		}
		_ = broadcastToChannel(meta.ChannelID, out, nil)
	default:
		_ = sendJSON(conn, OutgoingError{
			Type:    "error",
			Message: "Unknown message type",
		})
	}
}

func handleDisconnect(conn *websocket.Conn) {
	clientsMu.Lock()
	meta, ok := clients[conn]
	if !ok {
		clientsMu.Unlock()
		return
	}
	delete(clients, conn)
	clientsMu.Unlock()

	channelSubsMu.Lock()
	if subs, ok := channelSubs[meta.ChannelID]; ok {
		delete(subs, conn)
		if len(subs) == 0 {
			delete(channelSubs, meta.ChannelID)
		}
	}
	channelSubsMu.Unlock()

	_ = conn.Close()

	log.Printf("Client disconnected: user=%s channel=%s", meta.Username, meta.ChannelID)


	_ = broadcastToChannel(meta.ChannelID, OutgoingSystem{
		Type:      "system",
		Event:     "left",
		ChannelID: meta.ChannelID,
		UserID:    meta.UserID,
		Username:  meta.Username,
	}, conn)
}


func sendJSON(conn *websocket.Conn, msg interface{}) error {
	clientsMu.RLock()
	meta, ok := clients[conn]
	clientsMu.RUnlock()
	if !ok {
		return fmt.Errorf("conn not found")
	}

	meta.WriteMu.Lock()
	defer meta.WriteMu.Unlock()

	return conn.WriteJSON(msg)
}

func sendPing(conn *websocket.Conn) error {
	clientsMu.RLock()
	meta, ok := clients[conn]
	clientsMu.RUnlock()
	if !ok {
		return fmt.Errorf("conn not found")
	}

	meta.WriteMu.Lock()
	defer meta.WriteMu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	return conn.WriteControl(websocket.PingMessage, []byte{}, deadline)
}

// to write everyone except self
func broadcastToChannel(channelID string, message interface{}, skip *websocket.Conn) error {
	channelSubsMu.RLock()
	subs, ok := channelSubs[channelID]
	if !ok || len(subs) == 0 {
		channelSubsMu.RUnlock()
		return nil
	}
	conns := make([]*websocket.Conn, 0, len(subs))
	for c := range subs {
		if c != skip {
			conns = append(conns, c)
		}
	}
	channelSubsMu.RUnlock()

	for _, c := range conns {
		if err := sendJSON(c, message); err != nil {
			log.Printf("broadcast error to channel=%s: %v", channelID, err)
		}
	}
	return nil
}



func startHeartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	go func() {
		for range ticker.C {
			var toPing []*websocket.Conn
			var toClose []*websocket.Conn

			clientsMu.Lock()
			for c, meta := range clients {
				if !meta.IsAlive {
					toClose = append(toClose, c)
				} else {
					meta.IsAlive = false
					toPing = append(toPing, c)
				}
			}
			clientsMu.Unlock()

			for _, c := range toPing {
				_ = sendPing(c)
			}
			for _, c := range toClose {
				_ = c.Close()
			}
		}
	}()
}


func main() {
	initDB()
	startHeartbeat()

	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/channels/", historyHandler)
	http.HandleFunc("/ws", wsHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3001" 
	}

	addr := ":" + port
	fmt.Printf("server on http://localhost:%s\n", port)
	fmt.Printf("WebSocket endpoint: ws://localhost:%s/ws\n", port)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("error: %v", err)
	}
}
