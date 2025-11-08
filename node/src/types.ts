export type User = {
  id: string;
  username: string;
  token: string;
};

export type ClientMeta = {
  userId: string;
  username: string;
  channelId: string;
  ws: WebSocket;
};

export type IncomingMessage =
  | { type: "chat"; content: string }
  | { type: "typing"; isTyping: boolean }
  | { type: "ping" };

export type OutgoingMessage =
  | {
      type: "chat";
      id: string;
      channelId: string;
      userId: string;
      username: string;
      content: string;
      createdAt: string;
    }
  | {
      type: "typing";
      channelId: string;
      userId: string;
      username: string;
      isTyping: boolean;
    }
  | {
      type: "system";
      event: "joined" | "left";
      channelId: string;
      userId: string;
      username: string;
    }
  | { type: "pong" }
  | { type: "error"; message: string };
