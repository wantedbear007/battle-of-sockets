<h1 align="center">🧪 WebSocket Performance Comparison — Node.js vs Go</h1>

<p align="center">
A hands-on benchmark project comparing <b>WebSocket performance</b> between <b>Node.js (TypeScript)</b> and <b>Go</b>.
</p>

---

<h2>📘 Overview</h2>

<p>
Both servers implement the same real-world WebSocket app:
</p>

<ul>
  <li><code>POST /login</code> → returns a user token</li>
  <li><code>GET /channels/:channelId/history</code> → returns recent chat messages</li>
  <li><code>WS /ws?token=...&channel=...</code> → bi-directional chat</li>
  <ul>
    <li>Supports <code>chat</code>, <code>typing</code>, <code>ping/pong</code>, and system join/leave events</li>
    <li>Persists messages to SQLite</li>
  </ul>
</ul>

<p>
A Go load tester simulates thousands of WebSocket clients to compare throughput, latency, and concurrency behavior between Node and Go.
</p>

---

<h2>⚙️ Project Structure</h2>

<pre>
.
├── node/              # Node.js (TypeScript) WebSocket server
│   └── src/server.ts
├── golang/            # Go WebSocket server (same behavior as Node)
│   └── main.go
└── loadtest/          # Go WebSocket load generator
    └── main.go
</pre>

---

<h2>🚀 1. Run the Node.js Server</h2>

<p><b>Requirements:</b> Node ≥ 20, pnpm or npm</p>

<pre><code>cd node
pnpm install
pnpm run dev
</code></pre>

<p>The server starts at:</p>

<pre><code>http://localhost:3000
WebSocket: ws://localhost:3000/ws
</code></pre>

<table>
<thead>
<tr><th>Method</th><th>Path</th><th>Description</th></tr>
</thead>
<tbody>
<tr><td>POST</td><td>/login</td><td>Create a user & return token</td></tr>
<tr><td>GET</td><td>/channels/:id/history?limit=50</td><td>Fetch chat history</td></tr>
<tr><td>WS</td><td>/ws?token=...&channel=...</td><td>Connect to a chat channel</td></tr>
</tbody>
</table>

---

<h2>⚡️ 2. Run the Go Server</h2>

<p><b>Requirements:</b> Go ≥ 1.22</p>

<pre><code>cd golang
go mod tidy
go run main.go
</code></pre>

<p>By default, it listens on:</p>

<pre><code>http://localhost:3001
WebSocket: ws://localhost:3001/ws
</code></pre>

<p>
The Go version mirrors the Node implementation but uses <code>gorilla/websocket</code> and <code>modernc.org/sqlite</code>.
</p>

<ul>
  <li>WAL + busy_timeout SQLite setup</li>
  <li>Per-connection write mutex (prevents concurrent write panic)</li>
  <li>Heartbeat with ping/pong</li>
  <li>Clean broadcast logic</li>
</ul>

---

<h2>🧍‍♂️ 3. Run the Load Test (Go)</h2>

<p>Simulates up to <b>10,000 concurrent WebSocket clients</b>.</p>

<pre><code>cd loadtest
go mod tidy
go run main.go
</code></pre>

<p>The load test:</p>
<ul>
  <li>Creates 10k fake users</li>
  <li>Calls <code>/login</code></li>
  <li>Connects each to <code>/ws</code></li>
  <li>Sends one message each</li>
  <li>Reports total time & per-client average</li>
</ul>

<p>It runs two phases automatically:</p>

<ol>
  <li><b>Concurrent (GOMAXPROCS=1)</b> — concurrency without parallelism</li>
  <li><b>Parallel (GOMAXPROCS=NumCPU)</b> — full multicore parallelism</li>
</ol>

---

<h2>📊 4. What You Can Compare</h2>

<table>
<thead>
<tr><th>Metric</th><th>Node.js</th><th>Go</th></tr>
</thead>
<tbody>
<tr><td>Event Loop Model</td><td>Single-threaded, async</td><td>Multi-threaded, goroutines</td></tr>
<tr><td>Parallelism</td><td>❌ Only via clustering</td><td>✅ Uses all cores</td></tr>
<tr><td>WebSocket Handling</td><td><code>ws</code> (JS event loop)</td><td><code>gorilla/websocket</code> (goroutines)</td></tr>
<tr><td>DB Concurrency</td><td><code>better-sqlite3</code> sync</td><td>SQLite single connection (serialized)</td></tr>
<tr><td>Typical Latency</td><td>Low until CPU saturates</td><td>Consistently low under load</td></tr>
<tr><td>Scalability</td><td>Great for &lt;5k conns</td><td>Scales easily to 10k+</td></tr>
</tbody>
</table>

---

<h2>🧠 Notes & Tips</h2>

<ul>
  <li>SQLite allows only one writer at a time — both servers serialize writes.</li>
  <li>To measure pure WebSocket performance, switch to an in-memory store.</li>
  <li>Increase OS file descriptor limits for 10k+ connections:
    <pre><code>ulimit -n 65535</code></pre>
  </li>
  <li>Monitor CPU and memory:
    <pre><code>top -o cpu</code></pre>
  </li>
  <li>Use all cores in Go:
    <pre><code>GOMAXPROCS=$(nproc)</code></pre>
  </li>
</ul>

---

<h2>📈 Example Output (Go Load Test)</h2>

<pre><code>=== Test 1: Concurrent (GOMAXPROCS=1) ===
Total clients: 10000
Successful: 10000
Total time: 48.2s
Avg per client: 4.82ms

=== Test 2: Parallel (GOMAXPROCS=8) ===
Total clients: 10000
Successful: 10000
Total time: 12.3s
Avg per client: 1.23ms
</code></pre>

---

<h2>💡 Key Takeaways</h2>

<ul>
  <li>With DB contention removed, <b>Go’s parallelism shines</b> at high concurrency.</li>
  <li>Node performs great for smaller scales and rapid development.</li>
  <li>The performance gap widens as client count and message frequency increase.</li>
  <li>Both are excellent choices — but Go’s goroutines and multicore scaling make it ideal for real-time backends.</li>
</ul>

---

<h2>🧰 Stack</h2>

<table>
<thead>
<tr><th>Technology</th><th>Used For</th></tr>
</thead>
<tbody>
<tr><td><b>Node.js + TypeScript</b></td><td>Baseline WebSocket implementation</td></tr>
<tr><td><b>Go</b></td><td>High-performance server & load tester</td></tr>
<tr><td><b>gorilla/websocket</b></td><td>WebSocket handling</td></tr>
<tr><td><b>modernc.org/sqlite</b></td><td>Pure Go SQLite driver</td></tr>
<tr><td><b>better-sqlite3</b></td><td>Node.js SQLite wrapper</td></tr>
<tr><td><b>uuid / github.com/google/uuid</b></td><td>User & message IDs</td></tr>
</tbody>
</table>

---

<h2>🏁 License</h2>

<p>
MIT — free to use, modify, and benchmark.
</p>
