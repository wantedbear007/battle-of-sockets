# ⚡️ WebSocket Benchmark — Node.js vs Go

A simple real-world test comparing **WebSocket performance** between **Node.js (TypeScript)** and **Go**.

Each server implements the same minimal chat app:
- `POST /login` → creates a user + token  
- `GET /channels/:id/history` → recent messages  
- `WS /ws?token=...&channel=...` → chat, typing



## 🚀 Run the Servers

### Node.js
```bash
cd node
pnpm install
pnpm run dev
```


### Node.js
```bash
cd golang
go mod init
go run main.go
