/**
 * TypeScript SDK for the LadyM HTTP data-plane (`ladym serve --http`).
 *
 * Zero runtime dependencies (global fetch). The wire format mirrors
 * client/golang and sdk/python; non-2xx responses throw LadymError carrying
 * the HTTP status.
 */

export class LadymError extends Error {
  /** HTTP status of the failed response. */
  readonly status: number;

  constructor(status: number, message: string) {
    super(`ladym server returned http ${status}: ${message}`);
    this.name = "LadymError";
    this.status = status;
  }
}

/** One memory as returned in recall results / memory lists. */
export interface Memory {
  id: string;
  layer: string;
  type: string;
  summary: string;
  content: string;
  source: string;
  tags: string[];
  workspace?: string;
  metadata?: Record<string, unknown>;
  created_at?: number;
  updated_at?: number;
}

/** The /api/remember response. On an attention-gate drop nothing is
 * persisted: dropped is true and reason explains why. */
export interface RememberResult {
  id: string;
  hash: string;
  gated: string;
  reason: string;
  /** True when the server's attention gate dropped the write. */
  dropped: boolean;
}

export interface RecallResult {
  score: number;
  tier: number;
  via: string[];
  memory: Memory;
}

export interface RecallResponse {
  query: string;
  tier_reached: number;
  reflected_sufficient: boolean;
  elapsed_ms: number;
  results: RecallResult[];
}

export interface RecordEventResult {
  id: string;
  /** Wire value is the schema constant, e.g. "L1_episodic". */
  layer: string;
  type: string;
}

export interface ConsolidateResult {
  kept_episodes: number;
  promoted_to_semantic: number;
  skipped_consolidated: number;
  actions: Record<string, number>;
}

export interface Stats {
  total_memories: number;
  by_layer: Record<string, number>;
  by_type: Record<string, number>;
  edges: number;
  code_symbols: number;
  workspaces: string[];
  db_path: string;
  avg_tokens_per_memory: number;
}

/** One page of GET /api/memories; total is the filtered count before
 * pagination. */
export interface MemoryList {
  memories: Memory[];
  total: number;
}

/** A users-table account (the password hash never leaves the server). */
export interface User {
  username: string;
  workspace: string;
  admin: boolean;
  created_at: number;
}

export interface RememberOptions {
  tags?: string[];
  workspace?: string;
  /** An empty source defaults to "http" server-side. */
  source?: string;
}

export interface RecallOptions {
  /** 0 lets the server default (8). */
  topK?: number;
  workspace?: string;
  /** Restrict to code items (server maps it to SearchCode). */
  codeOnly?: boolean;
}

export interface RecordEventOptions {
  observation?: string;
  outcome?: string;
  tags?: string[];
  workspace?: string;
}

export interface ConsolidateOptions {
  workspace?: string;
  /** Unix seconds; bounds the pass, 0 processes the whole pending backlog. */
  since?: number;
}

export interface ListMemoriesOptions {
  workspace?: string;
  layer?: string;
  type?: string;
  /** Zero/omitted falls back to the server default (limit 50, offset 0). */
  limit?: number;
  offset?: number;
}

/** Partial memory update; unset fields are left unchanged. A content change
 * re-embeds server-side. */
export interface MemoryPatch {
  content?: string;
  summary?: string;
  tags?: string[];
}

/** Injectable fetch — pass a stub in tests or an environment-specific
 * implementation. Matches the standard fetch signature. */
export type FetchFn = typeof fetch;

export interface LadymClientOptions {
  username?: string;
  password?: string;
  /** Applied via AbortSignal.timeout; 0 disables. Default 30000. */
  timeoutMs?: number;
  fetch?: FetchFn;
}

function basicAuthHeader(user: string, password: string): string {
  const raw = `${user}:${password}`;
  const encoded =
    typeof Buffer !== "undefined"
      ? Buffer.from(raw, "utf8").toString("base64")
      : btoa(raw);
  return `Basic ${encoded}`;
}

/** Client for one `ladym serve --http` data-plane. With no username, no
 * Authorization header is sent (no-auth deployments); a username with an
 * empty password matches a passwordless server account. */
export class LadymClient {
  private readonly base: string;
  private readonly user?: string;
  private readonly password: string;
  private readonly timeoutMs: number;
  private readonly fetchFn: FetchFn;

  constructor(baseUrl: string, opts: LadymClientOptions = {}) {
    this.base = baseUrl.replace(/\/+$/, "");
    this.user = opts.username;
    this.password = opts.password ?? "";
    this.timeoutMs = opts.timeoutMs ?? 30_000;
    this.fetchFn = opts.fetch ?? fetch;
  }

  private async do(
    method: string,
    path: string,
    query?: Record<string, string>,
    body?: unknown,
  ): Promise<Record<string, unknown>> {
    let url = this.base + path;
    if (query && Object.keys(query).length > 0) {
      url += "?" + new URLSearchParams(query).toString();
    }
    const headers: Record<string, string> = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.user !== undefined) {
      headers["Authorization"] = basicAuthHeader(this.user, this.password);
    }
    const resp = await this.fetchFn(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: this.timeoutMs > 0 ? AbortSignal.timeout(this.timeoutMs) : undefined,
    });
    const text = await resp.text();
    let payload: Record<string, unknown> = {};
    try {
      const parsed: unknown = text.trim() === "" ? {} : JSON.parse(text);
      if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
        payload = parsed as Record<string, unknown>;
      }
    } catch {
      // Non-JSON error page (proxy etc.): keep the trimmed text as the
      // message so LadymError can surface it.
      payload = { error: text.trim() };
    }
    if (resp.status < 200 || resp.status >= 300) {
      const msg = typeof payload.error === "string" && payload.error !== "" ? payload.error : "(empty error body)";
      throw new LadymError(resp.status, msg);
    }
    return payload;
  }

  /** Probe /healthz (auth-exempt); resolves when healthy. */
  async ping(): Promise<void> {
    await this.do("GET", "/healthz");
  }

  /** Verify the client's own credentials; stateless, no session. */
  async login(): Promise<User> {
    const out = await this.do("POST", "/api/login", undefined, {
      username: this.user ?? "",
      password: this.password,
    });
    return out as unknown as User;
  }

  /** Write a semantic fact. */
  async remember(content: string, opts: RememberOptions = {}): Promise<RememberResult> {
    const out = await this.do("POST", "/api/remember", undefined, {
      content,
      source: opts.source ?? "",
      tags: opts.tags ?? null,
      workspace: opts.workspace ?? "",
    });
    const gated = (out.gated as string) ?? "";
    return {
      id: (out.id as string) ?? "",
      hash: (out.hash as string) ?? "",
      gated,
      reason: (out.reason as string) ?? "",
      dropped: gated === "dropped",
    };
  }

  /** Query memories. The HTTP data-plane exposes only query/top_k/workspace/
   * code_only; the MCP-only knobs (layers/types/min_similarity) are not part
   * of this signature by design. */
  async recall(query: string, opts: RecallOptions = {}): Promise<RecallResponse> {
    const out = await this.do("POST", "/api/recall", undefined, {
      query,
      top_k: opts.topK ?? 0,
      workspace: opts.workspace ?? "",
      code_only: opts.codeOnly ?? false,
    });
    const results = ((out.results as unknown[]) ?? []).map((r) => {
      const rr = r as Record<string, unknown>;
      return {
        score: (rr.score as number) ?? 0,
        tier: (rr.tier as number) ?? 0,
        via: (rr.via as string[]) ?? [],
        memory: (rr.memory ?? {}) as Memory,
      };
    });
    return {
      query: (out.query as string) ?? "",
      tier_reached: (out.tier_reached as number) ?? 0,
      reflected_sufficient: (out.reflected_sufficient as boolean) ?? false,
      elapsed_ms: (out.elapsed_ms as number) ?? 0,
      results,
    };
  }

  /** Write an L1 episodic event. */
  async recordEvent(
    agent: string,
    action: string,
    opts: RecordEventOptions = {},
  ): Promise<RecordEventResult> {
    const out = await this.do("POST", "/api/record_event", undefined, {
      agent,
      action,
      observation: opts.observation ?? "",
      outcome: opts.outcome ?? "",
      tags: opts.tags ?? null,
      workspace: opts.workspace ?? "",
    });
    return {
      id: (out.id as string) ?? "",
      layer: (out.layer as string) ?? "",
      type: (out.type as string) ?? "",
    };
  }

  /** Run one System2 consolidation cycle. */
  async consolidate(opts: ConsolidateOptions = {}): Promise<ConsolidateResult> {
    const out = await this.do("POST", "/api/consolidate", undefined, {
      workspace: opts.workspace ?? "",
      since: opts.since ?? 0,
    });
    return {
      kept_episodes: (out.kept_episodes as number) ?? 0,
      promoted_to_semantic: (out.promoted_to_semantic as number) ?? 0,
      skipped_consolidated: (out.skipped_consolidated as number) ?? 0,
      actions: (out.actions as Record<string, number>) ?? {},
    };
  }

  /** Aggregate statistics (workspace-scoped when given / forced). */
  async stats(opts: { workspace?: string } = {}): Promise<Stats> {
    const out = await this.do("POST", "/api/stats", undefined, { workspace: opts.workspace ?? "" });
    return out as unknown as Stats;
  }

  /** Create an associative edge src -[relation]-> dst; returns the edge id. */
  async link(src: string, dst: string, relation = "related_to"): Promise<string> {
    const out = await this.do("POST", "/api/link", undefined, { src, dst, relation });
    return (out.id as string) ?? "";
  }

  /** Delete a memory by id (no-op when missing, MCP semantics). */
  async forget(id: string): Promise<void> {
    await this.do("POST", "/api/forget", undefined, { memory_id: id });
  }

  /** List memories with workspace/layer/type filters. */
  async listMemories(opts: ListMemoriesOptions = {}): Promise<MemoryList> {
    const query: Record<string, string> = {};
    if (opts.workspace) query.workspace = opts.workspace;
    if (opts.layer) query.layer = opts.layer;
    if (opts.type) query.type = opts.type;
    if (opts.limit && opts.limit > 0) query.limit = String(opts.limit);
    if (opts.offset && opts.offset > 0) query.offset = String(opts.offset);
    const out = await this.do("GET", "/api/memories", query);
    return {
      memories: ((out.memories as unknown[]) ?? []).map((m) => m as Memory),
      total: (out.total as number) ?? 0,
    };
  }

  /** Patch content/summary/tags of one memory; returns the updated memory. */
  async updateMemory(id: string, patch: MemoryPatch): Promise<Memory> {
    const body: Record<string, unknown> = {};
    if (patch.content !== undefined) body.content = patch.content;
    if (patch.summary !== undefined) body.summary = patch.summary;
    if (patch.tags !== undefined) body.tags = patch.tags;
    const out = await this.do("PUT", "/api/memories/" + encodeURIComponent(id), undefined, body);
    return (out.memory ?? {}) as Memory;
  }

  /** Delete one memory by id; a missing id throws LadymError(404). */
  async deleteMemory(id: string): Promise<void> {
    await this.do("DELETE", "/api/memories/" + encodeURIComponent(id));
  }
}
