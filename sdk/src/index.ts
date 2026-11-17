export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };
export type JSONRecord = { [key: string]: JSONValue };

export interface PMQDocument<T extends JSONRecord = JSONRecord> {
  id: string;
  collection: string;
  data: T;
  createdAt: string;
  updatedAt: string;
}

export interface DocumentPage<T extends JSONRecord = JSONRecord> {
  documents: PMQDocument<T>[];
  limit: number;
  offset: number;
}

export interface ObjectInfo {
  key: string;
  size: number;
  lastModified: string;
  contentType?: string;
  etag?: string;
}

export interface ObjectPage {
  objects: ObjectInfo[];
  limit: number;
  nextCursor: string;
}

export interface ChatMessage {
  role: "system" | "user" | "assistant";
  content: string;
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  temperature?: number;
  top_p?: number;
  max_tokens?: number;
  stop?: string[];
}

export interface ChatCompletion {
  id: string;
  object: "chat.completion";
  created: number;
  model: string;
  choices: Array<{
    index: number;
    message: { role: "assistant"; content: string };
    finish_reason: string;
  }>;
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
}

export interface ChatCompletionChunk {
  id: string;
  object: "chat.completion.chunk";
  created: number;
  model: string;
  choices: Array<{
    index: number;
    delta: { role?: "assistant"; content?: string };
    finish_reason: string | null;
  }>;
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
}

export interface AIUsageEvent {
  type: "usage";
  status: string;
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number };
  estimated_cost_usd?: number;
  cost_basis?: "reported_tokens_and_configured_price" | "conservative_request_estimate";
}

export interface AIChunkEvent {
  type: "chunk";
  chunk: ChatCompletionChunk;
}

export type ChatStreamEvent = AIChunkEvent | AIUsageEvent;

export class PMQError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "PMQError";
    this.status = status;
    this.code = code;
  }
}

export interface RequestOptions {
  signal?: AbortSignal;
}

export interface PMQClientOptions {
  /** Optional origin override, useful for tests. Browser apps should omit it. */
  baseUrl?: string;
  /** Optional Fetch-compatible implementation, useful for tests and runtimes. */
  fetch?: typeof fetch;
}

interface JSONRequestOptions extends RequestOptions {
  method?: string;
  body?: unknown;
  headers?: HeadersInit;
}

/** A dependency-free browser client for the relative, same-origin PMQ APIs. */
export class PMQClient {
  readonly documents: {
    list<T extends JSONRecord = JSONRecord>(collection: string, options?: RequestOptions & { limit?: number; offset?: number }): Promise<DocumentPage<T>>;
    create<T extends JSONRecord = JSONRecord>(collection: string, data: T, options?: RequestOptions): Promise<PMQDocument<T>>;
    get<T extends JSONRecord = JSONRecord>(collection: string, id: string, options?: RequestOptions): Promise<PMQDocument<T>>;
    update<T extends JSONRecord = JSONRecord>(collection: string, id: string, data: T, options?: RequestOptions): Promise<PMQDocument<T>>;
    delete(collection: string, id: string, options?: RequestOptions): Promise<void>;
  };

  readonly storage: {
    list(options?: RequestOptions & { prefix?: string; limit?: number; cursor?: string }): Promise<ObjectPage>;
    upload(key: string, body: BodyInit, options?: RequestOptions & { contentType?: string }): Promise<void>;
    get(key: string, options?: RequestOptions): Promise<Response>;
    head(key: string, options?: RequestOptions): Promise<ObjectInfo>;
    delete(key: string, options?: RequestOptions): Promise<void>;
  };

  readonly ai: {
    complete(request: ChatCompletionRequest, options?: RequestOptions): Promise<ChatCompletion>;
    /** Pulls one SSE chunk at a time. Breaking iteration cancels the response body. */
    stream(request: ChatCompletionRequest, options?: RequestOptions): AsyncIterable<ChatStreamEvent>;
  };

  private readonly baseUrl?: string;
  private readonly fetcher: typeof fetch;

  constructor(options: PMQClientOptions = {}) {
    this.baseUrl = options.baseUrl?.replace(/\/$/, "");
    this.fetcher = options.fetch ?? globalThis.fetch.bind(globalThis);

    this.documents = {
      list: (collection, opts = {}) => {
        const query = new URLSearchParams();
        if (opts.limit !== undefined) query.set("limit", String(opts.limit));
        if (opts.offset !== undefined) query.set("offset", String(opts.offset));
        const suffix = query.size === 0 ? "" : `?${query}`;
        return this.json(`/db/${segment(collection)}${suffix}`, { signal: opts.signal });
      },
      create: (collection, data, opts = {}) => this.json(`/db/${segment(collection)}`, { method: "POST", body: data, signal: opts.signal }),
      get: (collection, id, opts = {}) => this.json(`/db/${segment(collection)}/${segment(id)}`, { signal: opts.signal }),
      update: (collection, id, data, opts = {}) => this.json(`/db/${segment(collection)}/${segment(id)}`, { method: "PUT", body: data, signal: opts.signal }),
      delete: async (collection, id, opts = {}) => {
        await this.request(`/db/${segment(collection)}/${segment(id)}`, { method: "DELETE", signal: opts.signal });
      },
    };

    this.storage = {
      list: (opts = {}) => {
        const query = new URLSearchParams();
        if (opts.prefix !== undefined) query.set("prefix", opts.prefix);
        if (opts.limit !== undefined) query.set("limit", String(opts.limit));
        if (opts.cursor !== undefined) query.set("cursor", opts.cursor);
        const suffix = query.size === 0 ? "" : `?${query}`;
        return this.json(`/storage${suffix}`, { signal: opts.signal });
      },
      upload: async (key, body, opts = {}) => {
        const headers = new Headers();
        if (opts.contentType) headers.set("Content-Type", opts.contentType);
        else if (body instanceof Blob && body.type) headers.set("Content-Type", body.type);
        await this.request(`/storage/${objectPath(key)}`, { method: "PUT", body, headers, signal: opts.signal });
      },
      get: (key, opts = {}) => this.request(`/storage/${objectPath(key)}`, { signal: opts.signal }),
      head: async (key, opts = {}) => {
        const response = await this.request(`/storage/${objectPath(key)}`, { method: "HEAD", signal: opts.signal });
        const size = Number(response.headers.get("Content-Length"));
        return {
          key,
          size: Number.isFinite(size) ? size : 0,
          lastModified: response.headers.get("Last-Modified") ?? "",
          contentType: response.headers.get("Content-Type") ?? undefined,
          etag: response.headers.get("ETag") ?? undefined,
        };
      },
      delete: async (key, opts = {}) => {
        await this.request(`/storage/${objectPath(key)}`, { method: "DELETE", signal: opts.signal });
      },
    };

    this.ai = {
      complete: (request, opts = {}) => this.json("/ai/chat/completions", { method: "POST", body: request, signal: opts.signal }),
      stream: (request, opts = {}) => this.streamChat(request, opts),
    };
  }

  private async json<T>(path: string, options: JSONRequestOptions = {}): Promise<T> {
    const response = await this.request(path, options);
    if (response.status === 204) return undefined as T;
    try {
      return (await response.json()) as T;
    } catch {
      throw new PMQError(response.status, "invalid_response", "PMQ returned an invalid JSON response");
    }
  }

  private async request(path: string, options: JSONRequestOptions = {}): Promise<Response> {
    const headers = new Headers();
    if (options.body !== undefined && !isBodyInit(options.body)) headers.set("Content-Type", "application/json");
    new Headers(options.headers).forEach((value, key) => headers.set(key, value));
    const response = await this.fetcher(this.url(path), {
      method: options.method ?? "GET",
      headers,
      body: options.body === undefined ? undefined : isBodyInit(options.body) ? options.body : JSON.stringify(options.body),
      signal: options.signal,
      credentials: "same-origin",
    });
    if (!response.ok) throw await this.error(response);
    return response;
  }

  private async error(response: Response): Promise<PMQError> {
    try {
      const payload = await response.json() as { error?: { code?: unknown; message?: unknown } };
      if (typeof payload.error?.code === "string" && typeof payload.error.message === "string") {
        return new PMQError(response.status, payload.error.code, payload.error.message);
      }
    } catch {
      // Non-JSON responses still have a useful status and stable error shape.
    }
    return new PMQError(response.status, "http_error", `PMQ request failed with HTTP ${response.status}`);
  }

  private async *streamChat(request: ChatCompletionRequest, options: RequestOptions): AsyncGenerator<ChatStreamEvent> {
    const response = await this.request("/ai/chat/completions", {
      method: "POST",
      body: { ...request, stream: true },
      headers: { Accept: "text/event-stream" },
      signal: options.signal,
    });
    if (!response.headers.get("Content-Type")?.toLowerCase().startsWith("text/event-stream") || !response.body) {
      throw new PMQError(response.status, "invalid_response", "PMQ did not return an AI event stream");
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffered = "";
    let eventName = "message";
    let data: string[] = [];
    let eventBytes = 0;
    let finished = false;
    try {
      while (!finished) {
        const newline = buffered.indexOf("\n");
        if (newline < 0) {
          const { done, value } = await reader.read();
          if (done) {
            buffered += decoder.decode();
            if (buffered.length > 0) buffered += "\n";
            else break;
          } else {
            buffered += decoder.decode(value, { stream: true });
          }
          if (buffered.length > 2 * 1024 * 1024) throw new PMQError(response.status, "stream_event_too_large", "PMQ AI stream event exceeded the client limit");
          continue;
        }

        const line = buffered.slice(0, newline).replace(/\r$/, "");
        buffered = buffered.slice(newline + 1);
        if (line === "") {
          if (data.length === 0) { eventName = "message"; continue; }
          const payload = data.join("\n");
          data = [];
          eventBytes = 0;
          const name = eventName;
          eventName = "message";
          if (name === "error") throw streamError(response.status, payload);
          if (payload === "[DONE]") { finished = true; break; }
          if (name === "pmq-usage") {
            let usage: Omit<AIUsageEvent, "type">;
            try { usage = JSON.parse(payload) as Omit<AIUsageEvent, "type">; }
            catch { throw new PMQError(response.status, "invalid_stream_event", "PMQ returned an invalid AI usage event"); }
            if (typeof usage.status !== "string") throw new PMQError(response.status, "invalid_stream_event", "PMQ returned an unsupported AI usage event");
            yield { type: "usage", ...usage };
            continue;
          }
          // Ignore extension events we do not yet understand; they are not chat chunks.
          if (name !== "message") continue;
          let chunk: ChatCompletionChunk;
          try { chunk = JSON.parse(payload) as ChatCompletionChunk; }
          catch { throw new PMQError(response.status, "invalid_stream_event", "PMQ returned an invalid AI stream event"); }
          if (chunk.object !== "chat.completion.chunk" || !Array.isArray(chunk.choices)) {
            throw new PMQError(response.status, "invalid_stream_event", "PMQ returned an unsupported AI stream event");
          }
          yield { type: "chunk", chunk };
          continue;
        }
        if (line.startsWith(":")) continue;
        const colon = line.indexOf(":");
        const field = colon < 0 ? line : line.slice(0, colon);
        const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "");
        if (field === "event") eventName = value;
        else if (field === "data") {
          eventBytes += value.length + 1;
          if (eventBytes > 2 * 1024 * 1024) throw new PMQError(response.status, "stream_event_too_large", "PMQ AI stream event exceeded the client limit");
          data.push(value);
        }
      }
    } finally {
      if (!finished) await reader.cancel().catch(() => undefined);
      reader.releaseLock();
    }
  }

  private url(path: string): string {
    const relative = `/_pmq${path}`;
    return this.baseUrl ? new URL(relative, this.baseUrl).toString() : relative;
  }
}

function streamError(status: number, payload: string): PMQError {
  try {
    const value = JSON.parse(payload) as { error?: { code?: unknown; message?: unknown } };
    if (typeof value.error?.code === "string" && typeof value.error.message === "string") {
      return new PMQError(status, value.error.code, value.error.message);
    }
  } catch { /* Fall through to a stable stream error. */ }
  return new PMQError(status, "stream_error", "PMQ AI stream failed");
}

function segment(value: string): string {
  return encodeURIComponent(value);
}

function objectPath(key: string): string {
  return key.split("/").map(segment).join("/");
}

function isBodyInit(value: unknown): value is BodyInit {
  return typeof value === "string" || value instanceof Blob || value instanceof ArrayBuffer || ArrayBuffer.isView(value) || value instanceof FormData || value instanceof URLSearchParams || value instanceof ReadableStream;
}

export function createPMQClient(options?: PMQClientOptions): PMQClient {
  return new PMQClient(options);
}
