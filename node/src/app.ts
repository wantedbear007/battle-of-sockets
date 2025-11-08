import http from "http";
import express, { Response, Request  } from "express";
import { WebSocketServer, WebSocket } from "ws";
import Database from "better-sqlite3";
import { v4 as uuidv4 } from "uuid";
import url from "url";
import { ClientMeta, IncomingMessage, OutgoingMessage, User } from "./types";



// sql lite setup 
const db = new Database("chat.sqlite");

// Create tables if not exists
db.exec(`
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
`);

const insertUserStmt = db.prepare<{
  id: string;
  username: string;
  token: string;
}>("INSERT INTO users (id, username, token) VALUES (@id, @username, @token)");

const getUserByTokenStmt = db.prepare<string, User>(
  "SELECT id, username, token FROM users WHERE token = ?"
);

const insertMessageStmt = db.prepare<{
  id: string;
  channelId: string;
  userId: string;
  username: string;
  content: string;
  createdAt: string;
}>(
  "INSERT INTO messages (id, channel_id, user_id, username, content, created_at) VALUES (@id, @channelId, @userId, @username, @content, @createdAt)"
);

const getMessagesForChannelStmt = db.prepare<
  { channelId: string; limit: number },
  {
    id: string;
    channel_id: string;
    user_id: string;
    username: string;
    content: string;
    created_at: string;
  }
>(
  "SELECT id, channel_id, user_id, username, content, created_at FROM messages WHERE channel_id = @channelId ORDER BY created_at DESC LIMIT @limit"
);


const app = express();
app.use(express.json());

/**
 * POST /login
 * Body: { "username": "alice" }
 * Returns: { userId, token, username }
 *
 * For demo purposes this is super simple (no passwords).
 */
app.post("/login", (req: Request, res: Response) => {
  const { username } = req.body || {};
  if (!username || typeof username !== "string" || username.trim().length < 2) {
    res.status(400).json({ error: "username is required (min 2 chars)" });
    return;
  }

  const user: User = {
    id: uuidv4(),
    username: username.trim(),
    token: uuidv4(),
  };

  insertUserStmt.run(user);
  res.json({ userId: user.id, token: user.token, username: user.username });
});

/**
 * GET /channels/:channelId/history?limit=50
 * Returns recent messages for a channel.
 */
app.get("/channels/:channelId/history", (req: Request, res: Response) => {
  const channelId = req.params.channelId;
  const limit = Math.min(parseInt(req.query.limit as string) || 50, 200);

  const rows = getMessagesForChannelStmt.all({ channelId, limit });
  const messages = rows
    .map((row) => ({
      id: row.id,
      channelId: row.channel_id,
      userId: row.user_id,
      username: row.username,
      content: row.content,
      createdAt: row.created_at,
    }))
    .reverse(); 

  res.json({ channelId, messages });
});


const server = http.createServer(app);
const wss = new WebSocketServer({ server, path: "/ws" });

// active clients keyed by WebSocket instance
const clients = new Map<WebSocket, ClientMeta>();

// channel -> set of WebSockets
const channelSubscriptions = new Map<string, Set<WebSocket>>();

// Heartbeat
function heartbeat(this: WebSocket) {
  (this as any).isAlive = true;
}

wss.on("connection", (ws: WebSocket, req: Request) => {
  // Mark as alive for heartbeat
  (ws as any).isAlive = true;
  ws.on("pong", heartbeat);

  // Parse token & channel from query params
  const parsedUrl = url.parse(req.url || "", true);
  const { token, channel } = parsedUrl.query;

  if (typeof token !== "string" || typeof channel !== "string") {
    ws.send(
      JSON.stringify({
        type: "error",
        message: "Missing token or channel in query string",
      } satisfies OutgoingMessage)
    );
    ws.close();
    return;
  }

  const user = getUserByTokenStmt.get(token);
  if (!user) {
    ws.send(
      JSON.stringify({
        type: "error",
        message: "Invalid token",
      } satisfies OutgoingMessage)
    );
    ws.close();
    return;
  }

  const channelId = channel.trim();
  if (!channelId) {
    ws.send(
      JSON.stringify({
        type: "error",
        message: "Invalid channel",
      } satisfies OutgoingMessage)
    );
    ws.close();
    return;
  }

  const meta: ClientMeta = {
    userId: user.id,
    username: user.username,
    channelId,
    ws,
  };

  clients.set(ws, meta);

  if (!channelSubscriptions.has(channelId)) {
    channelSubscriptions.set(channelId, new Set());
  }
  channelSubscriptions.get(channelId)!.add(ws);

  console.log(
    `Client connected: user=${meta.username} channel=${meta.channelId}`
  );

  // Notify others in the channel
  broadcastToChannel(meta.channelId, {
    type: "system",
    event: "joined",
    channelId: meta.channelId,
    userId: meta.userId,
    username: meta.username,
  });

  ws.on("message", (raw: string) => {
    handleIncomingMessage(ws, raw.toString());
  });

  ws.on("close", () => {
    handleDisconnect(ws);
  });

  ws.on("error", (err: unknown) => {
    console.error("WebSocket error:", err);
    handleDisconnect(ws);
  });
});

function handleIncomingMessage(ws: WebSocket, raw: string) {
  const meta = clients.get(ws);
  if (!meta) {
    ws.close();
    return;
  }

  let msg: IncomingMessage;
  try {
    msg = JSON.parse(raw);
  } catch (err) {
    ws.send(
      JSON.stringify({
        type: "error",
        message: "Invalid JSON",
      } satisfies OutgoingMessage)
    );
    return;
  }

  switch (msg.type) {
    case "ping": {
      const response: OutgoingMessage = { type: "pong" };
      ws.send(JSON.stringify(response));
      return;
    }

    case "typing": {
      const outgoing: OutgoingMessage = {
        type: "typing",
        channelId: meta.channelId,
        userId: meta.userId,
        username: meta.username,
        isTyping: msg.isTyping,
      };
      broadcastToChannel(meta.channelId, outgoing, ws);
      return;
    }

    case "chat": {
      const content = msg.content?.trim();
      if (!content) {
        ws.send(
          JSON.stringify({
            type: "error",
            message: "Empty message",
          } satisfies OutgoingMessage)
        );
        return;
      }

      const now = new Date().toISOString();
      const id = uuidv4();

      insertMessageStmt.run({
        id,
        channelId: meta.channelId,
        userId: meta.userId,
        username: meta.username,
        content,
        createdAt: now,
      });

      const outgoing: OutgoingMessage = {
        type: "chat",
        id,
        channelId: meta.channelId,
        userId: meta.userId,
        username: meta.username,
        content,
        createdAt: now,
      };
      broadcastToChannel(meta.channelId, outgoing);
      return;
    }

    default: {
      ws.send(
        JSON.stringify({
          type: "error",
          message: "Unknown message type",
        } satisfies OutgoingMessage)
      );
      return;
    }
  }
}

function handleDisconnect(ws: WebSocket) {
  const meta = clients.get(ws);
  if (!meta) return;

  clients.delete(ws);
  const channelSet = channelSubscriptions.get(meta.channelId);
  if (channelSet) {
    channelSet.delete(ws);
    if (channelSet.size === 0) {
      channelSubscriptions.delete(meta.channelId);
    }
  }

  console.log(
    `Client disconnected: user=${meta.username} channel=${meta.channelId}`
  );

  broadcastToChannel(meta.channelId, {
    type: "system",
    event: "left",
    channelId: meta.channelId,
    userId: meta.userId,
    username: meta.username,
  });
}

function broadcastToChannel(
  channelId: string,
  message: OutgoingMessage,
  skip?: WebSocket
) {
  const channelSet = channelSubscriptions.get(channelId);
  if (!channelSet) return;

  const data = JSON.stringify(message);
  for (const clientWs of channelSet) {
    if (clientWs.readyState === WebSocket.OPEN && clientWs !== skip) {
      clientWs.send(data);
    }
  }
}

// Heartbeat: terminate dead connections
const interval = setInterval(() => {
  for (const ws of wss.clients) {
    // @ts-ignore
    if (ws.isAlive === false) {
      return ws.terminate();
    }

    // @ts-ignore
    ws.isAlive = false;
    ws.ping();
  }
}, 30000);

wss.on("close", () => {
  clearInterval(interval);
});

// ------------------ Start server ------------------

const PORT = process.env.PORT || 3000;

server.listen(PORT, () => {
  console.log(`HTTP + WebSocket server listening on http://localhost:${PORT}`);
  console.log(`WebSocket endpoint: ws://localhost:${PORT}/ws`);
});
