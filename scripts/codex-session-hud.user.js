// ==UserScript==
// @name         xLyra Codex Session HUD
// @namespace    xlyra
// @version      0.2.0
// @description  在 Codex 输入框上方展示实时 Token、本轮调用次数、会话费用，并在 xLyra 请求完成后回填服务端数据；右上角提供今日统计。
// @match        app://-/*
// @match        https://chatgpt.com/*
// @match        https://chat.openai.com/*
// @run-at       document-start
// ==/UserScript==

(() => {
  "use strict";

  const VERSION = "0.2.0";
  const ROOT_ID = "xlyra-codex-session-hud";
  const STYLE_ID = "xlyra-codex-session-hud-style";
  const HEADER_ID = "xlyra-codex-session-hud-header";
  const CODEX_PLUS_MENU_ID = "codex-plus-menu";
  const STORAGE_KEY = "__xlyraCodexSessionHudV1";
  const MAX_STORED_REQUEST_IDS = 200;
  const SESSION_BINDING_TTL_MS = 7 * 24 * 60 * 60 * 1000;
  const RENDER_THROTTLE_MS = 80;
  const TURN_IDLE_GRACE_MS = 2200;
  const XLYRA_PROBE_TIMEOUT_MS = 900;
  const XLYRA_FINAL_RETRY_DELAYS_MS = [0, 250, 750, 1500];
  const TODAY_STATS_TTL_MS = 15 * 1000;
  const SESSION_SYNC_INTERVAL_MS = 500;
  const XLYRA_SYSTEM_VERSION_PATH = "/api/v1/system/version";
  const XLYRA_PORTAL_SETTINGS_PATH = "/v1/portal/settings";
  const XLYRA_PORTAL_REQUESTS_PATH = "/v1/portal/requests";

  if (window.__xlyraCodexSessionHud?.destroy) {
    try {
      window.__xlyraCodexSessionHud.destroy();
    } catch {
      // A stale instance must not prevent the new instance from loading.
    }
  }
  if (window.__xlyraCodexSessionHudVersion === VERSION) return;
  window.__xlyraCodexSessionHudVersion = VERSION;

  const state = {
    started: false,
    root: null,
    headerShell: null,
    headerButton: null,
    headerMenu: null,
    headerAnchor: null,
    headerOutsideHandler: null,
    headerKeyHandler: null,
    renderTimer: 0,
    activeSessionKey: "",
    activeSessionObservedAt: 0,
    syntheticSessionKey: `new:${Date.now().toString(36)}`,
    uiSessionKey: "",
    uiSessionObservedAt: 0,
    domObserver: null,
    sessionSyncTimer: 0,
    navigationHandlers: [],
    sessions: new Map(),
    originStates: new Map(),
    todayStats: new Map(),
    todayStatsInFlight: new Map(),
    storedBindings: loadStoredBindings(),
    originalFetch: null,
    originalXHR: null,
    originalWebSocket: null,
    messageHandler: null,
    responseContexts: new Set(),
  };

  function text(value, maxLength = 240) {
    const result = String(value == null ? "" : value).trim();
    return result.length > maxLength ? result.slice(0, maxLength) : result;
  }

  function count(value) {
    if (value == null || value === "" || typeof value === "boolean") return 0;
    const result = Number(value);
    return Number.isFinite(result) && result >= 0 ? Math.max(0, Math.round(result)) : 0;
  }

  function nonNegativeNumber(value) {
    if (value == null || value === "" || typeof value === "boolean") return null;
    const result = Number(value);
    return Number.isFinite(result) && result >= 0 ? result : null;
  }

  function firstCount(...values) {
    for (const value of values) {
      const result = count(value);
      if (result > 0) return result;
    }
    return 0;
  }

  function emptyUsage() {
    return { input: 0, output: 0, cached: 0, cacheWrite: 0, total: 0, exact: false };
  }

  function usageHasFields(value) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    return [
      "input_tokens",
      "inputTokens",
      "prompt_tokens",
      "promptTokens",
      "output_tokens",
      "outputTokens",
      "completion_tokens",
      "completionTokens",
      "total_tokens",
      "totalTokens",
      "cached_tokens",
      "cachedTokens",
      "cached_input_tokens",
      "cachedInputTokens",
      "cache_read_input_tokens",
      "cacheReadInputTokens",
      "cache_write_tokens",
      "cacheWriteTokens",
      "cache_creation_input_tokens",
      "cacheCreationInputTokens",
    ].some((key) => Object.prototype.hasOwnProperty.call(value, key));
  }

  function normalizeUsage(raw) {
    const value = raw && typeof raw === "object" ? raw : {};
    const rawInput = firstCount(value.input_tokens, value.inputTokens);
    const promptInput = firstCount(value.prompt_tokens, value.promptTokens, value.input, value.inputTotalTokens, value.input_total_tokens);
    const output = firstCount(
      value.output_tokens,
      value.outputTokens,
      value.output,
      value.completion_tokens,
      value.completionTokens,
    );
    const cached = firstCount(
      value.cached_tokens,
      value.cachedTokens,
      value.cached_input_tokens,
      value.cachedInputTokens,
      value.cache_read_input_tokens,
      value.cacheReadInputTokens,
      value.cachedReadTokens,
      value.cached_read_tokens,
    );
    const cacheWrite = firstCount(
      value.cache_write_tokens,
      value.cacheWriteTokens,
      value.cache_write_input_tokens,
      value.cacheWriteInputTokens,
      value.cache_creation_input_tokens,
      value.cacheCreationInputTokens,
      value.cacheCreationTokens,
    );
    const explicitTotal = firstCount(value.total_tokens, value.totalTokens, value.total, value.requestTotalTokens);
    const fromTotal = explicitTotal > output ? explicitTotal - output : 0;
    const inputBase = Math.max(rawInput, promptInput, fromTotal);
    const separateCache = cached > 0 || cacheWrite > 0;
    const cacheBase = rawInput || promptInput || fromTotal;
    const input = separateCache ? Math.max(inputBase, cacheBase + cached + cacheWrite) : inputBase;
    const total = explicitTotal || input + output;
    const exact = input > 0 || output > 0 || cached > 0 || cacheWrite > 0 || explicitTotal > 0;
    return { input, output, cached, cacheWrite, total, exact };
  }

  function addUsage(left, right) {
    const a = left || emptyUsage();
    const b = right || emptyUsage();
    return {
      input: count(a.input) + count(b.input),
      output: count(a.output) + count(b.output),
      cached: count(a.cached) + count(b.cached),
      cacheWrite: count(a.cacheWrite) + count(b.cacheWrite),
      total: count(a.total) + count(b.total),
      exact: Boolean(a.exact || b.exact),
    };
  }

  function usageKey(value) {
    const usage = normalizeUsage(value);
    return [usage.input, usage.output, usage.cached, usage.cacheWrite, usage.total].join(":");
  }

  function usageValue(value) {
    const usage = normalizeUsage(value);
    return count(usage.total || usage.input + usage.output);
  }

  function extractSseFragments(value) {
    return String(value || "")
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trim())
      .filter((line) => line && line !== "[DONE]");
  }

  function parseJson(value) {
    if (value && typeof value === "object") return value;
    if (typeof value !== "string" || !value.trim()) return null;
    try {
      return JSON.parse(value);
    } catch {
      return null;
    }
  }

  function isTokenCountPayload(value, depth = 0) {
    if (!value || depth > 3) return false;
    if (typeof value === "string") {
      const parsed = parseJson(value);
      return parsed ? isTokenCountPayload(parsed, depth + 1) : false;
    }
    if (typeof value !== "object") return false;
    const candidate = value?.payload?.type === "token_count" ? value.payload : value;
    return candidate?.type === "token_count" && candidate.info && typeof candidate.info === "object";
  }

  function tokenCountUsage(value) {
    const candidate = value?.payload?.type === "token_count" ? value.payload : value;
    const info = candidate?.info && typeof candidate.info === "object" ? candidate.info : {};
    const last =
      info.last_token_usage ||
      info.lastTokenUsage ||
      info.usage ||
      info.total_token_usage ||
      info.totalTokenUsage ||
      null;
    const usage = normalizeUsage(last);
    return usage.exact ? usage : null;
  }

  function collectUsages(value, depth = 0, output = [], seen = new WeakSet()) {
    if (value == null || depth > 8) return output;
    if (typeof value === "string") {
      const parsed = parseJson(value);
      if (parsed) collectUsages(parsed, depth + 1, output, seen);
      else {
        for (const fragment of extractSseFragments(value)) {
          const parsedFragment = parseJson(fragment);
          if (parsedFragment) collectUsages(parsedFragment, depth + 1, output, seen);
        }
      }
      return output;
    }
    if (Array.isArray(value)) {
      value.forEach((item) => collectUsages(item, depth + 1, output, seen));
      return output;
    }
    if (typeof value !== "object" || seen.has(value)) return output;
    seen.add(value);

    if (isTokenCountPayload(value)) {
      const usage = tokenCountUsage(value);
      if (usage) output.push(usage);
      return output;
    }
    if (usageHasFields(value)) {
      const usage = normalizeUsage(value);
      if (usage.exact) output.push(usage);
      return output;
    }
    for (const key of [
      "usage",
      "last",
      "last_usage",
      "lastUsage",
      "response",
      "payload",
      "info",
      "data",
      "body",
      "bodyJsonString",
      "body_json_string",
      "message",
      "result",
      "event",
      "params",
      "tokenUsage",
      "token_usage",
      "response_metadata",
    ]) {
      if (Object.prototype.hasOwnProperty.call(value, key)) collectUsages(value[key], depth + 1, output, seen);
    }
    return output;
  }

  function isCompletePayload(value, depth = 0, seen = new WeakSet()) {
    if (!value || depth > 8) return false;
    if (typeof value === "string") {
      const parsed = parseJson(value);
      if (parsed) return isCompletePayload(parsed, depth + 1, seen);
      return extractSseFragments(value).some((fragment) => isCompletePayload(parseJson(fragment), depth + 1, seen));
    }
    if (Array.isArray(value)) return value.some((item) => isCompletePayload(item, depth + 1, seen));
    if (typeof value !== "object" || seen.has(value)) return false;
    seen.add(value);
    const type = text(value.type || value.event || value.kind || value.method, 120).toLowerCase().replace(/[\s-]+/g, "_");
    if (["task_complete", "task_completed", "turn_completed", "response.completed", "response_complete", "response_done"].includes(type)) return true;
    if (type === "turn/completed" || value.method === "turn/completed") return true;
    for (const key of ["payload", "data", "body", "message", "result", "params", "response", "event"]) {
      if (Object.prototype.hasOwnProperty.call(value, key) && isCompletePayload(value[key], depth + 1, seen)) return true;
    }
    return false;
  }

  function sessionIdentity(value, depth = 0, seen = new WeakSet()) {
    if (!value || depth > 6) return {};
    if (typeof value === "string") {
      const parsed = parseJson(value);
      if (parsed) return sessionIdentity(parsed, depth + 1, seen);
      return sessionIdentityFromUrl(value);
    }
    if (Array.isArray(value) || typeof value !== "object" || seen.has(value)) return {};
    seen.add(value);
    const result = {
      thread: text(value.threadId || value.thread_id || value.thread?.id || value.thread?.threadId, 240),
      conversation: text(value.conversationId || value.conversation_id || value.conversation?.id || value.chatId || value.chat_id, 240),
      session: text(value.sessionId || value.session_id || value.runtimeSessionId || value.runtime_session_id || value.session?.id, 240),
      turn: text(value.turnId || value.turn_id || value.turn?.id, 240),
      request: text(value.requestId || value.request_id || value.request?.requestId || value.request?.request_id, 240),
      stream: text(value.streamId || value.stream_id || value.stream?.id || value.response?.streamId, 240),
    };
    for (const key of ["url", "href", "request", "params", "data", "payload", "message", "thread", "conversation", "session", "turn", "stream", "response", "result"]) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) continue;
      const nested = sessionIdentity(value[key], depth + 1, seen);
      for (const field of Object.keys(result)) result[field] ||= nested[field] || "";
    }
    return result;
  }

  function sessionIdentityFromUrl(value) {
    const result = { thread: "", conversation: "", session: "", turn: "", request: "", stream: "" };
    const raw = text(value, 1000);
    if (!raw) return result;
    try {
      const url = new URL(raw, window.location.href || "app://-/");
      for (const key of ["threadId", "thread_id"]) result.thread ||= text(url.searchParams.get(key), 240);
      for (const key of ["conversationId", "conversation_id", "chatId", "chat_id"]) result.conversation ||= text(url.searchParams.get(key), 240);
      for (const key of ["sessionId", "session_id"]) result.session ||= text(url.searchParams.get(key), 240);
      const path = decodeURIComponent(url.pathname || "");
      const match = path.match(/\/(thread|threads|conversation|conversations|chat|chats|session|sessions|c)\/([^/?#]+)/i);
      if (match?.[2]) {
        if (/^thread|^c$/i.test(match[1])) result.thread ||= text(match[2], 240);
        else if (/^conversation|^chat/i.test(match[1])) result.conversation ||= text(match[2], 240);
        else result.session ||= text(match[2], 240);
      }
    } catch {
      // URL parsing is best effort; the payload may still carry an identity.
    }
    return result;
  }

  function sessionKeyFromIdentity(identity) {
    const value = identity || {};
    const kind = value.thread ? "thread" : value.conversation ? "conversation" : value.session ? "session" : "";
    const id = value.thread || value.conversation || value.session;
    return id ? `${kind}:${id}` : "";
  }

  function normalizeUiSessionKey(value, kind = "thread") {
    const raw = text(value, 240);
    if (!raw) return "";
    if (/^(thread|conversation|session|new|page):/i.test(raw)) return raw;
    return `${kind}:${raw}`;
  }

  function sidebarThreadKeyFromNode(node) {
    const target = node?.closest?.(
      "[data-app-action-sidebar-thread-id],[data-thread-id],[data-conversation-id],[data-session-id]",
    ) || node;
    const threadId =
      target?.getAttribute?.("data-app-action-sidebar-thread-id") ||
      target?.getAttribute?.("data-thread-id");
    if (threadId) return normalizeUiSessionKey(threadId, "thread");
    const conversationId = target?.getAttribute?.("data-conversation-id");
    if (conversationId) return normalizeUiSessionKey(conversationId, "conversation");
    const sessionId = target?.getAttribute?.("data-session-id");
    return normalizeUiSessionKey(sessionId, "session");
  }

  function activeSidebarThreadKey(doc = document) {
    const selectors = [
      "[data-app-action-sidebar-thread-active='true'][data-app-action-sidebar-thread-id]",
      "[aria-current='page'][data-app-action-sidebar-thread-id]",
      "[aria-selected='true'][data-app-action-sidebar-thread-id]",
      "[data-state='active'][data-app-action-sidebar-thread-id]",
      "[data-active='true'][data-app-action-sidebar-thread-id]",
      "[data-selected='true'][data-app-action-sidebar-thread-id]",
      "[aria-current='page'][data-thread-id]",
      "[aria-selected='true'][data-thread-id]",
    ];
    for (const selector of selectors) {
      const key = sidebarThreadKeyFromNode(doc?.querySelector?.(selector));
      if (key) return key;
    }
    return "";
  }

  function isNewConversationTarget(target) {
    const node = target?.closest?.("a,button,[role='button']") || target;
    if (!node) return false;
    const value = text(`${node.getAttribute?.("aria-label") || ""} ${node.textContent || ""}`, 180).toLowerCase();
    return /new\s+(chat|conversation)|new conversation|新建|新对话|开始新/.test(value);
  }

  function currentUiSessionKey() {
    const fromSidebar = activeSidebarThreadKey();
    const fromUrl = sessionKeyFromIdentity(sessionIdentityFromUrl(window.location.href || ""));
    if (fromUrl && state.uiSessionKey && fromUrl !== state.uiSessionKey) return fromUrl;
    if (state.uiSessionKey && Date.now() - state.uiSessionObservedAt < 3000 && state.uiSessionKey !== fromSidebar) return state.uiSessionKey;
    if (fromSidebar) return fromSidebar;
    if (fromUrl) return fromUrl;
    return state.uiSessionKey || "";
  }

  function activateSession(key) {
    const normalizedKey = text(key, 300) || state.syntheticSessionKey;
    if (state.activeSessionKey === normalizedKey) return normalizedKey;
    state.activeSessionKey = normalizedKey;
    state.activeSessionObservedAt = Date.now();
    const session = getSession(normalizedKey);
    if (!session.activeOrigin) {
      const xlyraOrigins = Array.from(state.originStates.values()).filter((item) => item?.kind === "xlyra");
      if (xlyraOrigins.length === 1) session.activeOrigin = xlyraOrigins[0].origin;
    }
    scheduleActiveSessionHydration(session);
    scheduleRender(0);
    return normalizedKey;
  }

  function locationSessionKey() {
    const identity = sessionIdentityFromUrl(window.location.href || "");
    const key = sessionKeyFromIdentity(identity);
    if (key) return key;
    try {
      const url = new URL(window.location.href || "app://-/");
      return `page:${url.origin}${url.pathname}`;
    } catch {
      return "page:codex";
    }
  }

  function activeSessionKey() {
    const uiKey = currentUiSessionKey();
    if (uiKey) return activateSession(uiKey);
    if (state.activeSessionKey) return state.activeSessionKey;
    return activateSession(locationSessionKey() || state.syntheticSessionKey);
  }

  function observeSession(value) {
    const key = sessionKeyFromIdentity(sessionIdentity(value));
    if (!key) return "";
    getSession(key);
    const uiKey = currentUiSessionKey();
    if (!uiKey || uiKey === key || !state.activeSessionKey) activateSession(key);
    scheduleRender();
    return key;
  }

  function syncActiveSession() {
    const uiKey = currentUiSessionKey();
    const candidate = uiKey || state.activeSessionKey || locationSessionKey() || state.syntheticSessionKey;
    if (candidate && candidate !== state.activeSessionKey) activateSession(candidate);
    return state.activeSessionKey || candidate;
  }

  function installDomObservers() {
    if (state.domObserver || !document.documentElement) return;
    const ownNode = (node) => Boolean(node && (state.root?.contains?.(node) || state.headerShell?.contains?.(node)));
    state.domObserver = typeof MutationObserver === "function" ? new MutationObserver((records) => {
      const relevant = records.some((record) => {
        if (ownNode(record.target)) return false;
        if (record.type === "attributes") {
          return /^(aria-current|aria-selected|data-state|data-active|data-selected|data-app-action-sidebar-thread-active|data-app-action-sidebar-thread-id|data-thread-id|data-conversation-id|data-session-id|href)$/i.test(record.attributeName || "");
        }
        return true;
      });
      if (!relevant) return;
      syncActiveSession();
      scheduleRender(0);
    }) : null;
    state.domObserver?.observe(document.documentElement, { subtree: true, childList: true, attributes: true });

    const clickHandler = (event) => {
      if (state.headerShell?.contains?.(event.target)) return;
      const sidebarKey = sidebarThreadKeyFromNode(event.target);
      if (sidebarKey) {
        state.uiSessionKey = sidebarKey;
        state.uiSessionObservedAt = Date.now();
        activateSession(sidebarKey);
      } else if (isNewConversationTarget(event.target)) {
        state.syntheticSessionKey = newId("new");
        state.uiSessionKey = state.syntheticSessionKey;
        state.uiSessionObservedAt = Date.now();
        activateSession(state.syntheticSessionKey);
      }
    };
    document.addEventListener("click", clickHandler, true);
    state.navigationHandlers.push({ target: document, type: "click", handler: clickHandler, capture: true });
    for (const type of ["popstate", "hashchange"]) {
      const handler = () => {
        syncActiveSession();
        scheduleRender(0);
      };
      window.addEventListener(type, handler);
      state.navigationHandlers.push({ target: window, type, handler });
    }
    state.sessionSyncTimer = window.setInterval(() => {
      const before = state.activeSessionKey;
      syncActiveSession();
      if (before !== state.activeSessionKey) scheduleRender(0);
    }, SESSION_SYNC_INTERVAL_MS);
  }

  function removeDomObservers() {
    state.domObserver?.disconnect?.();
    state.domObserver = null;
    if (state.sessionSyncTimer) window.clearInterval(state.sessionSyncTimer);
    state.sessionSyncTimer = 0;
    for (const item of state.navigationHandlers.splice(0)) item.target?.removeEventListener?.(item.type, item.handler, item.capture);
  }

  function newId(prefix) {
    try {
      if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return `${prefix}-${crypto.randomUUID()}`;
    } catch {
      // Fall through to a local collision-resistant identifier.
    }
    return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}-${Math.random().toString(36).slice(2)}`;
  }

  function loadStoredBindings() {
    try {
      const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) || "null");
      const now = Date.now();
      const result = new Map();
      for (const item of Array.isArray(raw?.sessions) ? raw.sessions : []) {
        const key = text(item?.key, 300);
        const updatedAt = count(item?.updatedAt);
        const ids = Array.from(new Set((Array.isArray(item?.requestIds) ? item.requestIds : []).map((id) => text(id, 240)).filter(Boolean))).slice(-MAX_STORED_REQUEST_IDS);
        if (!key || !ids.length || !updatedAt || now - updatedAt > SESSION_BINDING_TTL_MS) continue;
        result.set(key, { key, requestIds: ids, updatedAt });
      }
      return result;
    } catch {
      return new Map();
    }
  }

  function saveStoredBindings() {
    try {
      const now = Date.now();
      const sessions = Array.from(state.storedBindings.values())
        .filter((item) => item?.key && item?.requestIds?.length && now - count(item.updatedAt) <= SESSION_BINDING_TTL_MS)
        .slice(-100)
        .map((item) => ({ key: item.key, requestIds: item.requestIds.slice(-MAX_STORED_REQUEST_IDS), updatedAt: item.updatedAt }));
      localStorage.setItem(STORAGE_KEY, JSON.stringify({ version: 1, sessions }));
    } catch {
      // Privacy mode or a full storage quota must not affect the HUD.
    }
  }

  function rememberRequestId(sessionKey, requestId) {
    const key = text(sessionKey, 300);
    const id = text(requestId, 240);
    if (!key || !id) return;
    const current = state.storedBindings.get(key) || { key, requestIds: [], updatedAt: 0 };
    current.requestIds = Array.from(new Set([...current.requestIds, id])).slice(-MAX_STORED_REQUEST_IDS);
    current.updatedAt = Date.now();
    state.storedBindings.set(key, current);
    saveStoredBindings();
  }

  function getSession(key = activeSessionKey()) {
    const normalizedKey = text(key, 300) || state.syntheticSessionKey;
    let session = state.sessions.get(normalizedKey);
    if (session) return session;
    const stored = state.storedBindings.get(normalizedKey);
    session = {
      key: normalizedKey,
      current: null,
      lastTurn: null,
      history: [],
      finalRecords: new Map(),
      requestIds: new Set(stored?.requestIds || []),
      hydratedOrigins: new Set(),
      hydratingOrigins: new Set(),
      activeOrigin: "",
    };
    state.sessions.set(normalizedKey, session);
    return session;
  }

  function scheduleActiveSessionHydration(session) {
    if (!session) return;
    const origin = text(session.activeOrigin, 400) || text(session.lastTurn?.xlyraOrigin, 400);
    const info = origin ? originState(origin) : null;
    const auth = info?.authName && info?.authValue ? { name: info.authName, value: info.authValue } : null;
    if (info?.kind === "xlyra" && auth) void hydrateSessionFromXlyra(session, origin, auth, info);
  }

  function isLikelyModelUrl(url) {
    const value = String(url || "");
    if (!value) return false;
    let target = value;
    try {
      const parsed = new URL(value, window.location.href || "app://-/");
      target = parsed.pathname || value;
    } catch {
      // A relative or custom-scheme URL can still be matched by its raw path.
    }
    if (/\/v1\/portal\/|\/api\/v1\/system\/version|\/models(?:[/?#]|$)|\/usage(?:[/?#]|$)|\/profile/i.test(target)) return false;
    return /\/(responses|chat\/completions|messages|conversation|conversations|threads?)(?:[/?#]|$)/i.test(target);
  }

  function requestUrl(input) {
    if (typeof input === "string") return input;
    return text(input?.url || input, 2000);
  }

  function requestMethod(input, init) {
    return String(init?.method || input?.method || "GET").toUpperCase();
  }

  function headerValue(headers, name) {
    const wanted = String(name || "").toLowerCase();
    if (!wanted || !headers) return "";
    try {
      if (typeof headers.get === "function") return text(headers.get(name) || headers.get(wanted), 1200);
      if (Array.isArray(headers)) {
        for (const pair of headers) {
          if (Array.isArray(pair) && String(pair[0] || "").toLowerCase() === wanted) return text(pair[1], 1200);
        }
      }
      for (const [key, value] of Object.entries(headers)) {
        if (String(key).toLowerCase() === wanted) return text(Array.isArray(value) ? value[0] : value, 1200);
      }
    } catch {
      // Header inspection is best effort.
    }
    return "";
  }

  function requestAuthHeader(input, init) {
    const authorization = headerValue(init?.headers, "Authorization") || headerValue(input?.headers, "Authorization");
    if (authorization) return { name: "Authorization", value: authorization };
    const apiKey = headerValue(init?.headers, "X-API-Key") || headerValue(input?.headers, "X-API-Key");
    if (apiKey) return { name: "X-API-Key", value: apiKey };
    return null;
  }

  function requestBody(input, init) {
    if (init && Object.prototype.hasOwnProperty.call(init, "body")) return init.body;
    return null;
  }

  function bodyModel(value, depth = 0, seen = new WeakSet()) {
    if (!value || depth > 3) return "";
    if (typeof value === "string") {
      const parsed = parseJson(value);
      return parsed ? bodyModel(parsed, depth + 1, seen) : "";
    }
    if (typeof value !== "object" || seen.has(value)) return "";
    seen.add(value);
    for (const key of ["model", "model_name", "modelName", "requested_model", "requestedModel"]) {
      const candidate = text(value[key], 120);
      if (candidate) return candidate;
    }
    for (const key of ["request", "params", "data", "payload"]) {
      const candidate = bodyModel(value[key], depth + 1, seen);
      if (candidate) return candidate;
    }
    return "";
  }

  function bodyLooksLikeModelRequest(value, depth = 0, seen = new WeakSet()) {
    if (!value || depth > 3) return false;
    if (typeof value === "string") {
      const parsed = parseJson(value);
      return parsed ? bodyLooksLikeModelRequest(parsed, depth + 1, seen) : false;
    }
    if (typeof value !== "object" || seen.has(value)) return false;
    seen.add(value);
    if (["model", "messages", "input", "prompt", "stream", "tools", "conversation_id", "thread_id"].some((key) => Object.prototype.hasOwnProperty.call(value, key))) return true;
    return ["request", "params", "data", "payload"].some((key) => bodyLooksLikeModelRequest(value[key], depth + 1, seen));
  }

  function originForUrl(url) {
    try {
      const parsed = new URL(String(url || ""), window.location.href || "app://-/");
      return /^https?:$/i.test(parsed.protocol) ? parsed.origin : "";
    } catch {
      return "";
    }
  }

  function originState(origin) {
    if (!origin) return { origin: "", kind: "other", xlyra: false, portal: null };
    const existing = state.originStates.get(origin);
    if (existing) return existing;
    const created = { origin, kind: "unknown", xlyra: false, portal: null, portalChecked: false, promise: null, error: "" };
    state.originStates.set(origin, created);
    return created;
  }

  function rememberXlyraAuth(origin, auth) {
    const item = originState(origin);
    if (item.kind !== "xlyra" || !auth?.name || !auth?.value) return;
    item.authName = text(auth.name, 40);
    item.authValue = text(auth.value, 1200);
  }

  async function probeJson(url, auth = null) {
    if (typeof state.originalFetch !== "function") return null;
    const controller = typeof AbortController === "function" ? new AbortController() : null;
    const timer = controller ? window.setTimeout(() => controller.abort(), XLYRA_PROBE_TIMEOUT_MS) : 0;
    try {
      const response = await state.originalFetch(url, {
        method: "GET",
        cache: "no-store",
        credentials: "omit",
        headers: { Accept: "application/json" },
        ...(auth?.name && auth?.value ? { headers: { Accept: "application/json", [auth.name]: auth.value } } : {}),
        ...(controller ? { signal: controller.signal } : {}),
      });
      if (!response?.ok) return null;
      const payload = await response.json();
      return payload && typeof payload === "object" ? payload : null;
    } catch {
      return null;
    } finally {
      if (timer) window.clearTimeout(timer);
    }
  }

  function looksLikeXlyraVersion(payload) {
    return Boolean(payload && typeof payload.version === "string" && (Object.prototype.hasOwnProperty.call(payload, "commit") || Object.prototype.hasOwnProperty.call(payload, "build_time")));
  }

  function looksLikeXlyraSettings(payload) {
    return Boolean(
      payload &&
        typeof payload === "object" &&
        Object.prototype.hasOwnProperty.call(payload, "enabled") &&
        (Object.prototype.hasOwnProperty.call(payload, "show_requests") || Object.prototype.hasOwnProperty.call(payload, "dimensions")),
    );
  }

  function normalizePortalSettings(payload) {
    const dimensions = payload?.dimensions && typeof payload.dimensions === "object" ? payload.dimensions : {};
    const enabled = payload?.enabled === true;
    const showRequests = payload?.show_requests !== false;
    const tokens = dimensions.tokens !== false;
    const cost = dimensions.cost !== false;
    return { enabled, showRequests, tokens, cost, readable: enabled && showRequests && (tokens || cost) };
  }

  function defaultXlyraPortalSettings() {
    return { enabled: true, showRequests: true, tokens: true, cost: true, readable: true };
  }

  function unavailablePortalSettings() {
    return { enabled: false, showRequests: false, tokens: false, cost: false, readable: false };
  }

  async function detectOrigin(origin, auth = null) {
    const item = originState(origin);
    if (!origin || item.kind === "other") return item;
    if (item.kind === "xlyra" && item.portal && item.portalChecked) return item;
    if (item.promise) return item.promise;
    item.promise = (async () => {
      const [versionPayload, settingsPayload] = await Promise.all([
        probeJson(`${origin}${XLYRA_SYSTEM_VERSION_PATH}`, auth),
        probeJson(`${origin}${XLYRA_PORTAL_SETTINGS_PATH}`, auth),
      ]);
      if (looksLikeXlyraVersion(versionPayload) || looksLikeXlyraSettings(settingsPayload)) {
        item.kind = "xlyra";
        item.xlyra = true;
        item.portal = looksLikeXlyraSettings(settingsPayload) ? normalizePortalSettings(settingsPayload) : unavailablePortalSettings();
        item.portalChecked = true;
        rememberXlyraAuth(origin, auth);
      } else if (item.kind !== "xlyra") {
        item.kind = "other";
        item.xlyra = false;
        item.error = "upstream_is_not_xlyra";
      } else {
        item.portal ||= unavailablePortalSettings();
        item.portalChecked = true;
      }
      scheduleRender();
      return item;
    })().finally(() => {
      item.promise = null;
    });
    return item.promise;
  }

  function markOriginFromResponse(url, response, context = null) {
    const origin = originForUrl(url);
    if (!origin || !response?.headers) return;
    const routeSite = headerValue(response.headers, "X-Xlyra-Route-Site");
    if (!routeSite) return;
    const item = originState(origin);
    item.kind = "xlyra";
    item.xlyra = true;
    item.portal ||= unavailablePortalSettings();
    item.error = "";
    if (context) {
      context.originInfo = item;
      rememberXlyraAuth(origin, { name: context.authName, value: context.authValue });
      bindXlyraContext(context, headerValue(response.headers, "X-Request-ID"));
    }
    scheduleRender();
  }

  function requestWithXlyraId(input, init, requestId) {
    const sourceHeaders = init?.headers || input?.headers || {};
    let headers;
    try {
      if (typeof Headers === "function") {
        headers = new Headers(sourceHeaders);
        headers.set("X-Request-ID", requestId);
      } else if (Array.isArray(sourceHeaders)) {
        headers = sourceHeaders.filter((pair) => String(pair?.[0] || "").toLowerCase() !== "x-request-id");
        headers.push(["X-Request-ID", requestId]);
      } else {
        headers = { ...sourceHeaders, "X-Request-ID": requestId };
      }
    } catch {
      headers = { "X-Request-ID": requestId };
    }
    return [input, { ...(init || {}), headers }];
  }

  function requestIdFrom(input, init) {
    return headerValue(init?.headers, "X-Request-ID") || headerValue(input?.headers, "X-Request-ID");
  }

  function makeTurn(session, model = "") {
    return {
      id: newId("turn"),
      sessionKey: session.key,
      model: text(model, 120),
      startedAt: Date.now(),
      lastActivityAt: Date.now(),
      status: "running",
      completeObserved: false,
      finalizeTimer: 0,
      pendingRequests: 0,
      calls: new Map(),
      liveUsage: emptyUsage(),
      finalUsage: null,
      finalCost: null,
      finalCurrency: "",
      finalReady: false,
      finalRecords: new Map(),
      origin: "",
      xlyraOrigin: "",
      xlyraRequestIds: new Set(),
    };
  }

  function currentTurnForContext(context) {
    return context?.turn || getSession(context?.sessionKey || activeSessionKey()).current || null;
  }

  function addCallToTurn(turn, context = {}) {
    if (!turn) return null;
    const key = text(context.callKey || context.requestId, 300) || newId("call");
    let call = turn.calls.get(key);
    if (!call) {
      call = { key, requestId: text(context.requestId, 240), startedAt: Date.now(), ended: false, usage: emptyUsage() };
      turn.calls.set(key, call);
      turn.pendingRequests += 1;
    }
    return call;
  }

  function bindXlyraContext(context, requestId = "") {
    if (!context?.turn || context.originInfo?.kind !== "xlyra") return;
    const turn = context.turn;
    const session = getSession(context.sessionKey);
    context.requestId ||= text(requestId, 240);
    turn.xlyraOrigin = context.origin;
    if (!context.requestId) return;
    turn.xlyraRequestIds.add(context.requestId);
    session.requestIds.add(context.requestId);
    rememberRequestId(session.key, context.requestId);
  }

  function setRequestContextRequestID(context, requestId) {
    const normalizedID = text(requestId, 240);
    if (!context || !normalizedID) return;
    const turn = context.turn;
    const currentKey = context.callKey;
    const call = turn?.calls.get(currentKey);
    if (call && currentKey !== normalizedID) {
      turn.calls.delete(currentKey);
      call.key = normalizedID;
      call.requestId = normalizedID;
      turn.calls.set(normalizedID, call);
    }
    context.requestId = normalizedID;
    context.callKey = normalizedID;
  }

  function startTurnForRequest(sessionKey, model, context) {
    const session = getSession(sessionKey);
    const now = Date.now();
    let turn = session.current;
    if (turn && turn.status === "running" && (turn.pendingRequests > 0 || now - turn.lastActivityAt <= TURN_IDLE_GRACE_MS * 2)) {
      if (turn.finalizeTimer) window.clearTimeout(turn.finalizeTimer);
    } else {
      if (turn) finishTurn(session, turn);
      turn = makeTurn(session, model);
      session.current = turn;
    }
    turn.lastActivityAt = now;
    if (model) turn.model = model;
    if (context.origin) {
      turn.origin = context.origin;
      session.activeOrigin = context.origin;
    }
    context.turn = turn;
    const call = addCallToTurn(turn, context);
    context.callKey = call.key;
    bindXlyraContext(context);
    scheduleRender();
    return turn;
  }

  function findLatestActiveCall(turn) {
    if (!turn) return null;
    const calls = Array.from(turn.calls.values()).filter((call) => !call.ended);
    calls.sort((a, b) => b.startedAt - a.startedAt);
    return calls[0] || null;
  }

  function findTurnForUsage(sessionKey, context) {
    if (context?.turn) return context.turn;
    const session = getSession(sessionKey);
    return session.current || session.lastTurn || null;
  }

  function recordUsage(rawUsage, source, sessionKey, context = {}, identity = {}) {
    const usage = normalizeUsage(rawUsage);
    if (!usage.exact) return false;
    const session = getSession(sessionKey || activeSessionKey());
    let turn = findTurnForUsage(session.key, context);
    if (!turn || turn.status !== "running") {
      turn = makeTurn(session, text(context.model, 120));
      session.current = turn;
    }
    turn.lastActivityAt = Date.now();
    if (!turn.model && context.model) turn.model = context.model;

    let call = context.callKey ? turn.calls.get(context.callKey) : null;
    if (!call) call = findLatestActiveCall(turn);
    if (!call) {
      const runtimeKey = text(identity.request || identity.stream || identity.turn, 180);
      const fallbackKey = runtimeKey ? `runtime:${runtimeKey}` : `usage:${usageKey(usage)}`;
      call = turn.calls.get(fallbackKey);
      if (!call) {
        call = { key: fallbackKey, requestId: "", startedAt: Date.now(), ended: false, usage: emptyUsage() };
        turn.calls.set(fallbackKey, call);
      }
    }
    call.usage = usage;
    turn.liveUsage = Array.from(turn.calls.values()).reduce((sum, item) => addUsage(sum, item.usage), emptyUsage());
    if (context.originInfo?.kind === "xlyra") turn.xlyraOrigin = context.origin;
    if (isCompletePayload(context.lastPayload) || isCompletePayload(rawUsage)) turn.completeObserved = true;
    void source;
    scheduleRender();
    return true;
  }

  function scheduleTurnFinalize(session, turn) {
    if (!session || !turn || turn.status !== "running") return;
    if (turn.finalizeTimer) window.clearTimeout(turn.finalizeTimer);
    turn.finalizeTimer = window.setTimeout(() => {
      turn.finalizeTimer = 0;
      if (turn.pendingRequests > 0 || session.current !== turn) return;
      finishTurn(session, turn);
    }, turn.completeObserved ? 250 : TURN_IDLE_GRACE_MS);
  }

  function finishTurn(session, turn) {
    if (!session || !turn || turn.status !== "running") return;
    if (turn.finalizeTimer) window.clearTimeout(turn.finalizeTimer);
    turn.finalizeTimer = 0;
    turn.status = "completed";
    turn.completedAt = Date.now();
    session.current = session.current === turn ? null : session.current;
    session.lastTurn = turn;
    session.history = session.history.filter((item) => item.id !== turn.id).concat(turn).slice(-100);
    scheduleRender();
  }

  function markCallEnded(context, error = "") {
    const turn = context?.turn;
    if (!turn || context.ended) return;
    context.ended = true;
    const call = turn.calls.get(context.callKey);
    if (call && !call.ended) {
      call.ended = true;
      turn.pendingRequests = Math.max(0, turn.pendingRequests - 1);
    }
    turn.lastActivityAt = Date.now();
    if (error) turn.error = text(error, 180);
    if (turn.pendingRequests === 0) scheduleTurnFinalize(getSession(turn.sessionKey), turn);
    scheduleRender();
  }

  function releaseRequestContext(context) {
    if (!context) return;
    state.responseContexts.delete(context);
    context.authName = "";
    context.authValue = "";
  }

  function isPortalItemMatch(item, requestId) {
    const id = text(item?.request_id || item?.requestId, 240);
    const parent = text(item?.parent_request_id || item?.parentRequestId, 240);
    return id === requestId || parent === requestId || id.startsWith(`${requestId}:`);
  }

  function portalItemUsage(item) {
    const value = item?.usage && typeof item.usage === "object" ? item.usage : null;
    if (!value) return null;
    const usage = normalizeUsage({
      input_tokens: value.input_tokens,
      prompt_tokens: value.prompt_tokens,
      completion_tokens: value.completion_tokens,
      output_tokens: value.output_tokens,
      total_tokens: value.total_tokens,
      cached_tokens: value.cache_tokens ?? value.cached_tokens ?? value.cached_input_tokens,
      cache_read_input_tokens: value.cache_read_input_tokens,
      cache_write_tokens: value.cache_write_tokens ?? value.cache_creation_tokens,
    });
    return usage.exact ? usage : null;
  }

  function portalItemCost(item) {
    const cost = item?.cost && typeof item.cost === "object" ? item.cost : {};
    const calculation = item?.cost_calculation && typeof item.cost_calculation === "object" ? item.cost_calculation : {};
    const usage = item?.usage && typeof item.usage === "object" ? item.usage : {};
    for (const candidate of [
      item?.cost,
      cost.estimated_cost,
      calculation.estimated_cost,
      calculation.total_cost,
      item?.estimated_cost,
      item?.total_cost,
      usage.estimated_cost,
    ]) {
      const result = nonNegativeNumber(candidate);
      if (result != null) return result;
    }
    return null;
  }

  function portalItemCurrency(item) {
    const cost = item?.cost && typeof item.cost === "object" ? item.cost : {};
    const calculation = item?.cost_calculation && typeof item.cost_calculation === "object" ? item.cost_calculation : {};
    const usage = item?.usage && typeof item.usage === "object" ? item.usage : {};
    return text(cost.currency || calculation.currency || usage.currency || item?.currency, 20);
  }

  function normalizePortalResult(requestId, body, portalSettings) {
    const settings = portalSettings || defaultXlyraPortalSettings();
    const items = (Array.isArray(body?.items) ? body.items : []).filter((item) => isPortalItemMatch(item, requestId));
    if (!items.length) return { requestId, ready: false, pending: true, usage: null, cost: null, currency: "", items: [] };
    const unique = [];
    const seen = new Set();
    for (const item of items) {
      const key = `${text(item?.id, 120)}\u0001${text(item?.request_id || item?.requestId, 240)}`;
      if (seen.has(key)) continue;
      seen.add(key);
      unique.push(item);
    }
    const usages = unique.map(portalItemUsage);
    const costs = unique.map(portalItemCost);
    const usageReady = settings.tokens === false || unique.every((item, index) => item?.success === false || usages[index] !== null);
    const costReady = settings.cost === false || unique.every((item, index) => item?.success === false || costs[index] !== null);
    const currencies = Array.from(new Set(unique.map(portalItemCurrency).filter(Boolean)));
    const currency = currencies.length === 1 ? currencies[0] : currencies.length > 1 ? "" : "USD";
    const usage = usages.filter(Boolean).reduce((sum, item) => addUsage(sum, item), emptyUsage());
    const cost = costReady && settings.cost !== false ? costs.reduce((sum, item) => sum + (item || 0), 0) : null;
    return {
      requestId,
      ready: usageReady && costReady,
      pending: !(usageReady && costReady),
      usage: usage.exact ? usage : null,
      cost,
      currency,
      items: unique,
    };
  }

  async function hydrateSessionFromXlyra(session, origin, auth, info) {
    if (
      !session ||
      !origin ||
      info?.kind !== "xlyra" ||
      !auth?.name ||
      !auth?.value ||
      !state.originalFetch ||
      session.hydratingOrigins.has(origin)
    ) {
      return;
    }
    const requestIds = Array.from(session.requestIds).filter((requestId) => Boolean(requestId) && !session.finalRecords.get(requestId)?.ready);
    if (!requestIds.length) return;
    session.hydratingOrigins.add(origin);
    try {
      const portal = info.portal || defaultXlyraPortalSettings();
      const endpoint = new URL(origin + XLYRA_PORTAL_REQUESTS_PATH);
      endpoint.searchParams.set("page", "1");
      endpoint.searchParams.set("page_size", "200");
      const response = await state.originalFetch(endpoint.toString(), {
        method: "GET",
        cache: "no-store",
        credentials: "omit",
        headers: { Accept: "application/json", [auth.name]: auth.value },
      });
      if (!response?.ok) return;
      const payload = await response.json();
      const grouped = new Map();
      for (const item of Array.isArray(payload?.items) ? payload.items : []) {
        for (const requestId of requestIds) {
          if (!isPortalItemMatch(item, requestId)) continue;
          const items = grouped.get(requestId) || [];
          items.push(item);
          grouped.set(requestId, items);
        }
      }
      for (const [requestId, items] of grouped) {
        const result = normalizePortalResult(requestId, { items }, portal);
        if (result.items.length) applySessionFinalRecord(session, requestId, result);
      }
      scheduleRender();
    } catch {
      // Historical hydration is best effort and must not affect the user request.
    } finally {
      session.hydratingOrigins.delete(origin);
    }
  }

  function recomputeFinalTurn(turn) {
    if (!turn || turn.xlyraRequestIds.size === 0) return;
    const results = Array.from(turn.xlyraRequestIds).map((id) => turn.finalRecords.get(id));
    if (!results.length || results.some((item) => !item?.ready)) {
      turn.finalReady = false;
      scheduleRender();
      return;
    }
    const usageItems = results.map((item) => item.usage).filter(Boolean);
    turn.finalUsage = usageItems.length ? usageItems.reduce((sum, item) => addUsage(sum, item), emptyUsage()) : null;
    const costs = results.map((item) => item.cost).filter((item) => item != null);
    turn.finalCost = costs.length === results.length ? costs.reduce((sum, item) => sum + item, 0) : null;
    const currencies = Array.from(new Set(results.map((item) => item.currency).filter(Boolean)));
    turn.finalCurrency = currencies.length === 1 ? currencies[0] : "";
    turn.finalReady = true;
    scheduleRender();
  }

  function applySessionFinalRecord(session, requestId, result) {
    if (!session || !requestId || !result?.items?.length) return;
    session.finalRecords.set(requestId, result);
    const turns = session.history.slice();
    if (session.current && !turns.some((item) => item.id === session.current.id)) turns.push(session.current);
    for (const turn of turns) {
      if (!turn.xlyraRequestIds.has(requestId)) continue;
      turn.finalRecords.set(requestId, result);
      recomputeFinalTurn(turn);
    }
  }

  async function queryXlyraRequest(context) {
    try {
      const origin = text(context?.origin, 400);
      const requestId = text(context?.requestId, 240);
      const authName = text(context?.authName, 40);
      const authValue = text(context?.authValue, 1200);
      let info = originState(origin);
      if (!origin || info.kind !== "xlyra" || !requestId || !authName || !authValue || !state.originalFetch) return;
      rememberXlyraAuth(origin, { name: authName, value: authValue });
      info = await detectOrigin(origin, { name: authName, value: authValue });
      if (info.kind !== "xlyra" || info.portal?.readable === false) return;
      const turn = context.turn;
      if (!turn) return;
      for (const delay of XLYRA_FINAL_RETRY_DELAYS_MS) {
        if (delay) await new Promise((resolve) => window.setTimeout(resolve, delay));
        try {
          const endpoint = new URL(`${origin}${XLYRA_PORTAL_REQUESTS_PATH}`);
          endpoint.searchParams.set("request_id", requestId);
          endpoint.searchParams.set("page", "1");
          endpoint.searchParams.set("page_size", "200");
          const response = await state.originalFetch(endpoint.toString(), {
            method: "GET",
            cache: "no-store",
            credentials: "omit",
            headers: { Accept: "application/json", [authName]: authValue },
          });
          if (!response?.ok) return;
            const result = normalizePortalResult(requestId, await response.json(), info.portal || defaultXlyraPortalSettings());
          if (result.items.length) {
            applySessionFinalRecord(getSession(context.sessionKey), requestId, result);
            if (result.ready) return;
          }
        } catch {
          // The final Portal read is best effort and must not affect the original response.
        }
      }
      scheduleRender();
    } finally {
      releaseRequestContext(context);
    }
  }

  function todayStartIso() {
    const start = new Date();
    start.setHours(0, 0, 0, 0);
    return start.toISOString();
  }

  function emptyTodayStats() {
    return { usage: emptyUsage(), cost: null, currency: "USD", calls: 0, updatedAt: 0, loading: false, error: "" };
  }

  async function queryXlyraToday(origin, auth, force = false) {
    const info = originState(origin);
    if (!origin || info.kind !== "xlyra" || info.portal?.readable === false || !auth?.name || !auth?.value || !state.originalFetch) return null;
    const cached = state.todayStats.get(origin);
    if (!force && cached?.updatedAt && Date.now() - cached.updatedAt < TODAY_STATS_TTL_MS) return cached;
    const inFlight = state.todayStatsInFlight.get(origin);
    if (inFlight) return inFlight;
    const promise = (async () => {
      try {
        const base = new URL(`${origin}${XLYRA_PORTAL_REQUESTS_PATH}`);
        base.searchParams.set("from", todayStartIso());
        base.searchParams.set("to", new Date().toISOString());
        base.searchParams.set("page", "1");
        base.searchParams.set("page_size", "200");
        const requestPage = async (page) => {
          const endpoint = new URL(base.toString());
          endpoint.searchParams.set("page", String(page));
          const response = await state.originalFetch(endpoint.toString(), {
            method: "GET",
            cache: "no-store",
            credentials: "omit",
            headers: { Accept: "application/json", [auth.name]: auth.value },
          });
          if (!response?.ok) throw new Error(`http_${response?.status || 0}`);
          return response.json();
        };
        const first = await requestPage(1);
        const bodies = [first];
        const totalPages = Math.max(1, count(first?.total_pages));
        for (let page = 2; page <= totalPages; page += 1) bodies.push(await requestPage(page));
        const items = bodies.flatMap((body) => (Array.isArray(body?.items) ? body.items : []));
        let usage = emptyUsage();
        let itemCost = 0;
        let itemCostCount = 0;
        let currency = text(first?.stats?.currency, 20) || "";
        for (const item of items) {
          const itemUsage = portalItemUsage(item);
          if (itemUsage) usage = addUsage(usage, itemUsage);
          const cost = portalItemCost(item);
          if (cost != null) {
            itemCost += cost;
            itemCostCount += 1;
          }
          currency ||= portalItemCurrency(item);
        }
        const reportedTotal = count(first?.stats?.total_tokens);
        if (reportedTotal > 0) {
          usage.total = reportedTotal;
          usage.exact = true;
        }
        const reportedCost = nonNegativeNumber(first?.stats?.cost);
        const result = {
          usage,
          cost: reportedCost != null ? reportedCost : itemCostCount ? itemCost : null,
          currency: currency || "USD",
          calls: count(first?.total) || items.length,
          updatedAt: Date.now(),
          loading: false,
          error: "",
        };
        state.todayStats.set(origin, result);
        scheduleRender();
        return result;
      } catch (error) {
        const previous = state.todayStats.get(origin) || emptyTodayStats();
        const result = { ...previous, loading: false, error: text(error?.message || "today_stats_failed", 120) };
        state.todayStats.set(origin, result);
        scheduleRender();
        return result;
      } finally {
        state.todayStatsInFlight.delete(origin);
      }
    })();
    state.todayStatsInFlight.set(origin, promise);
    return promise;
  }

  function refreshTodayStats(origin, info, force = false) {
    const auth = info?.authName && info?.authValue ? { name: info.authName, value: info.authValue } : null;
    if (info?.kind !== "xlyra" || !auth) return;
    void queryXlyraToday(origin, auth, force);
  }

  function markPayloadComplete(payload, context) {
    if (!isCompletePayload(payload)) return;
    const turn = currentTurnForContext(context);
    if (!turn) return;
    turn.completeObserved = true;
    turn.lastActivityAt = Date.now();
    if (turn.pendingRequests === 0) scheduleTurnFinalize(getSession(turn.sessionKey), turn);
  }

  function inspectPayload(payload, source, context = {}) {
    if (payload == null) return;
    const identity = sessionIdentity(payload);
    const observedKey = sessionKeyFromIdentity(identity);
    const sessionKey = observedKey || context.sessionKey || activeSessionKey();
    if (observedKey) observeSession(payload);
    if (!/body/i.test(String(source || ""))) {
      for (const usage of collectUsages(payload)) recordUsage(usage, source, sessionKey, context, identity);
    }
    markPayloadComplete(payload, context);
    if (identity.request && context.originInfo?.kind === "xlyra") {
      context.requestId ||= identity.request;
    }
    scheduleRender();
  }

  function inspectText(value, source, context = {}) {
    const raw = String(value || "");
    if (!raw) return;
    const parsed = parseJson(raw);
    if (parsed) inspectPayload(parsed, source, context);
    for (const fragment of extractSseFragments(raw)) {
      const payload = parseJson(fragment);
      if (payload) inspectPayload(payload, source, context);
    }
  }

  async function observeFetchResponse(response, context) {
    if (!response) {
      markCallEnded(context, "empty_response");
      return;
    }
    markOriginFromResponse(context.url, response, context);
    try {
      const clone = response.clone?.();
      if (clone?.body?.getReader) {
        const reader = clone.body.getReader();
        const decoder = typeof TextDecoder === "function" ? new TextDecoder() : null;
        let carry = "";
        while (true) {
          const item = await reader.read();
          if (item.done) break;
          const chunk = decoder ? decoder.decode(item.value, { stream: true }) : String.fromCharCode(...item.value);
          carry += chunk;
          const lines = carry.split(/\r?\n/);
          carry = lines.pop() || "";
          for (const line of lines) inspectText(line, "fetch", context);
        }
        if (decoder) carry += decoder.decode();
        if (carry) inspectText(carry, "fetch", context);
      } else if (clone?.text) {
        inspectText(await clone.text(), "fetch", context);
      }
    } catch {
      // A consumed or non-cloneable response must not affect the user request.
    } finally {
      markCallEnded(context, response.ok ? "" : `http_${response.status}`);
      if (context.originInfo?.kind === "xlyra" && context.requestId) void queryXlyraRequest(context);
      else releaseRequestContext(context);
    }
  }

  function beginRequestContext(url, input, init, method) {
    const origin = originForUrl(url);
    const info = originState(origin);
    const body = requestBody(input, init);
    const observedSessionKey = sessionKeyFromIdentity(sessionIdentity(body)) || sessionKeyFromIdentity(sessionIdentityFromUrl(url));
    if (observedSessionKey) activateSession(observedSessionKey);
    const sessionKey = observedSessionKey || activeSessionKey();
    if (body != null) inspectPayload(body, "fetch-body", { sessionKey });
    const model = bodyModel(body);
    const auth = requestAuthHeader(input, init);
    rememberXlyraAuth(origin, auth);
    const existingRequestId = requestIdFrom(input, init);
    const requestId = info.kind === "xlyra" ? existingRequestId || newId("xlyra") : "";
    const context = {
      url,
      origin,
      originInfo: info,
      sessionKey,
      method,
      model,
      authName: auth?.name || "",
      authValue: auth?.value || "",
      requestId,
      callKey: requestId || newId("call"),
      turn: null,
      ended: false,
      lastPayload: null,
    };
    startTurnForRequest(sessionKey, model, context);
    if (info.kind === "xlyra") void hydrateSessionFromXlyra(getSession(sessionKey), origin, auth, info);
    state.responseContexts.add(context);
    return context;
  }

  function installFetchCapture() {
    if (typeof window.fetch !== "function" || window.fetch.__xlyraCodexSessionHudWrapped === VERSION) return;
    state.originalFetch = window.fetch.__xlyraCodexSessionHudOriginal || window.fetch;
    const originalFetch = state.originalFetch;
    async function wrappedFetch(input, init) {
      const url = requestUrl(input);
      const method = requestMethod(input, init);
      if (!isLikelyModelUrl(url) || method === "GET" || method === "HEAD") return originalFetch.call(this, input, init);
      const origin = originForUrl(url);
      const auth = requestAuthHeader(input, init);
      const info = await detectOrigin(origin, auth);
      const body = requestBody(input, init);
      if (!bodyLooksLikeModelRequest(body) && !/\/(responses|chat\/completions|messages|conversation|conversations|threads?)(?:[/?#]|$)/i.test(url)) return originalFetch.call(this, input, init);
      const context = beginRequestContext(url, input, init, method);
      const args = info.kind === "xlyra" && context.requestId ? requestWithXlyraId(input, init, context.requestId) : [input, init];
      try {
        const response = await originalFetch.call(this, ...args);
        void observeFetchResponse(response, context);
        return response;
      } catch (error) {
        markCallEnded(context, error?.message || "fetch_failed");
        if (info.kind === "xlyra" && context.requestId) void queryXlyraRequest(context);
        else releaseRequestContext(context);
        throw error;
      }
    }
    wrappedFetch.__xlyraCodexSessionHudWrapped = VERSION;
    wrappedFetch.__xlyraCodexSessionHudOriginal = originalFetch;
    window.fetch = wrappedFetch;
  }

  function installXhrCapture() {
    const Xhr = window.XMLHttpRequest;
    if (!Xhr?.prototype || Xhr.prototype.__xlyraCodexSessionHudWrapped === VERSION) return;
    state.originalXHR = Xhr;
    const originalOpen = Xhr.prototype.__xlyraCodexSessionHudOriginalOpen || Xhr.prototype.open;
    const originalSend = Xhr.prototype.__xlyraCodexSessionHudOriginalSend || Xhr.prototype.send;
    const originalSetRequestHeader = Xhr.prototype.__xlyraCodexSessionHudOriginalSetRequestHeader || Xhr.prototype.setRequestHeader;
    Xhr.prototype.open = function open(method, url, ...rest) {
      this.__xlyraCodexSessionHudUrl = url;
      this.__xlyraCodexSessionHudMethod = String(method || "GET").toUpperCase();
      return originalOpen.call(this, method, url, ...rest);
    };
    if (typeof originalSetRequestHeader === "function") {
      Xhr.prototype.setRequestHeader = function setRequestHeader(name, value) {
        const key = String(name || "").toLowerCase();
        if (key === "authorization" || key === "x-api-key" || key === "x-request-id") {
          this.__xlyraCodexSessionHudHeaders ||= {};
          this.__xlyraCodexSessionHudHeaders[key] = String(value || "");
        }
        return originalSetRequestHeader.call(this, name, value);
      };
    }
    Xhr.prototype.send = function send(...args) {
      const url = text(this.__xlyraCodexSessionHudUrl, 2000);
      const method = this.__xlyraCodexSessionHudMethod || "GET";
      let context = null;
      if (isLikelyModelUrl(url) && method !== "GET" && method !== "HEAD") {
        const origin = originForUrl(url);
        const info = originState(origin);
        const body = args[0];
        if (bodyLooksLikeModelRequest(body) || /\/(responses|chat\/completions|messages|conversation|conversations|threads?)(?:[/?#]|$)/i.test(url)) {
          const auth = this.__xlyraCodexSessionHudHeaders || {};
          const authHeader = auth.authorization
            ? { name: "Authorization", value: auth.authorization }
            : auth["x-api-key"]
              ? { name: "X-API-Key", value: auth["x-api-key"] }
              : null;
          const requestId = info.kind === "xlyra" ? auth["x-request-id"] || newId("xlyra") : "";
          const inputLike = { headers: { Authorization: auth.authorization || "", "X-API-Key": auth["x-api-key"] || "", "X-Request-ID": auth["x-request-id"] || "" } };
          context = beginRequestContext(url, inputLike, { body, headers: inputLike.headers }, method);
          rememberXlyraAuth(origin, authHeader);
          setRequestContextRequestID(context, requestId);
          if (info.kind === "xlyra" && requestId) {
            try {
              this.setRequestHeader("X-Request-ID", requestId);
            } catch {
              // The original XHR remains usable if the environment blocks custom headers.
            }
          }
          this.addEventListener?.("loadend", () => {
            try {
              inspectText(this.responseText || "", "xhr", context);
              const routeSite = this.getResponseHeader?.("X-Xlyra-Route-Site");
              if (routeSite) {
                const item = originState(origin);
                item.kind = "xlyra";
                item.xlyra = true;
                item.error = "";
                context.originInfo = item;
                bindXlyraContext(context, this.getResponseHeader?.("X-Request-ID") || "");
              }
            } catch {
              // Ignore unreadable XHR response bodies.
            }
            markCallEnded(context, this.status >= 400 ? `http_${this.status}` : "");
            if (context.originInfo?.kind === "xlyra" && context.requestId) void queryXlyraRequest(context);
            else releaseRequestContext(context);
          });
          const detection = detectOrigin(origin, authHeader);
          if (info.kind === "unknown" && detection && typeof detection.then === "function") {
            detection.then((detected) => {
              context.originInfo = detected;
              if (detected.kind === "xlyra") {
                const detectedRequestId = auth["x-request-id"] || newId("xlyra");
                setRequestContextRequestID(context, detectedRequestId);
                try {
                  this.setRequestHeader("X-Request-ID", detectedRequestId);
                } catch {
                  // The original XHR remains usable if the environment blocks custom headers.
                }
              }
              originalSend.apply(this, args);
            }).catch(() => originalSend.apply(this, args));
            return;
          }
        }
      }
      return originalSend.apply(this, args);
    };
    Xhr.prototype.__xlyraCodexSessionHudOriginalOpen = originalOpen;
    Xhr.prototype.__xlyraCodexSessionHudOriginalSend = originalSend;
    Xhr.prototype.__xlyraCodexSessionHudOriginalSetRequestHeader = originalSetRequestHeader;
    Xhr.prototype.__xlyraCodexSessionHudWrapped = VERSION;
  }

  function installWebSocketCapture() {
    if (typeof window.WebSocket !== "function" || window.WebSocket.__xlyraCodexSessionHudWrapped === VERSION) return;
    state.originalWebSocket = window.WebSocket.__xlyraCodexSessionHudOriginal || window.WebSocket;
    const NativeWebSocket = state.originalWebSocket;
    function WrappedWebSocket(...args) {
      const url = text(args[0], 2000);
      if (isLikelyModelUrl(url)) void detectOrigin(originForUrl(url));
      const socket = new NativeWebSocket(...args);
      socket.addEventListener?.("message", (event) => {
        try {
          const sessionKey = observeSession(url) || activeSessionKey();
          const info = originState(originForUrl(url));
          const session = getSession(sessionKey);
          const context = { sessionKey, origin: originForUrl(url), originInfo: info, model: "", callKey: `ws:${url}`, turn: session.current, lastPayload: event.data };
          inspectText(typeof event.data === "string" ? event.data : "", "websocket", context);
        } catch {
          // Keep WebSocket delivery untouched.
        }
      });
      return socket;
    }
    try {
      WrappedWebSocket.prototype = NativeWebSocket.prototype;
      for (const key of ["CONNECTING", "OPEN", "CLOSING", "CLOSED"]) Object.defineProperty(WrappedWebSocket, key, { value: NativeWebSocket[key] });
    } catch {
      // Best-effort compatibility for browser and desktop WebSocket implementations.
    }
    WrappedWebSocket.__xlyraCodexSessionHudWrapped = VERSION;
    WrappedWebSocket.__xlyraCodexSessionHudOriginal = NativeWebSocket;
    window.WebSocket = WrappedWebSocket;
  }

  function installMessageCapture() {
    if (state.messageHandler) return;
    state.messageHandler = (event) => {
      try {
        const sessionKey = observeSession(event?.data) || activeSessionKey();
        const session = getSession(sessionKey);
        const context = { sessionKey, turn: session.current, lastPayload: event?.data };
        inspectPayload(event?.data, "message", context);
      } catch {
        // Message inspection must never affect application events.
      }
    };
    window.addEventListener("message", state.messageHandler, true);
  }

  function formatCount(value) {
    const result = count(value);
    return result.toLocaleString("zh-CN");
  }

  function formatMoney(value, currency = "USD") {
    const amount = Number(value);
    if (!Number.isFinite(amount)) return "—";
    const symbol = currency === "CNY" ? "¥" : currency === "EUR" ? "€" : "$";
    return `${symbol}${amount.toFixed(amount < 0.01 ? 6 : 4)}`;
  }

  function escapeHtml(value) {
    return String(value == null ? "" : value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll('"', "&quot;")
      .replaceAll("'", "&#39;");
  }

  function turnDisplayUsage(turn) {
    if (!turn) return emptyUsage();
    return turn.finalReady && turn.finalUsage ? turn.finalUsage : turn.liveUsage || emptyUsage();
  }

  function turnCostSummary(turn) {
    if (!turn) return { cost: null, currency: "" };
    const readyRecords = Array.from(turn.finalRecords?.values?.() || []).filter((item) => item?.ready && item.cost != null);
    if (readyRecords.length) {
      const currencies = Array.from(new Set(readyRecords.map((item) => text(item.currency, 20)).filter(Boolean)));
      return {
        cost: readyRecords.reduce((sum, item) => sum + (Number(item.cost) || 0), 0),
        currency: currencies.length === 1 ? currencies[0] : "USD",
      };
    }
    return { cost: turn.finalCost != null ? Number(turn.finalCost) : null, currency: text(turn.finalCurrency, 20) };
  }

  function aggregateSession(session) {
    const usage = emptyUsage();
    let cost = 0;
    let costCount = 0;
    let restoredCurrency = "";
    const turns = session.history.slice();
    if (session.current && !turns.some((item) => item.id === session.current.id)) turns.push(session.current);
    const associatedRequestIds = new Set();
    for (const turn of turns) {
      const item = turnDisplayUsage(turn);
      Object.assign(usage, addUsage(usage, item));
      for (const requestId of turn.xlyraRequestIds) associatedRequestIds.add(requestId);
      const turnCost = turnCostSummary(turn);
      if (turnCost.cost != null) {
        cost += Number(turnCost.cost) || 0;
        costCount += 1;
        restoredCurrency ||= turnCost.currency;
      }
    }
    let restoredCalls = 0;
    for (const [requestId, result] of session.finalRecords) {
      if (associatedRequestIds.has(requestId) || !result?.ready) continue;
      restoredCalls += 1;
      if (result.usage) Object.assign(usage, addUsage(usage, result.usage));
      if (result.cost != null) {
        cost += Number(result.cost) || 0;
        costCount += 1;
      }
      restoredCurrency ||= text(result.currency, 20);
    }
    return {
      usage,
      cost: costCount ? cost : null,
      currency: turns.find((item) => item.finalCurrency)?.finalCurrency || restoredCurrency || "USD",
      calls: turns.reduce((sum, item) => sum + item.calls.size, 0) + restoredCalls,
    };
  }

  function currentOriginInfo(session, turn) {
    const origin = text(turn?.origin, 400) || text(session?.activeOrigin, 400);
    if (origin) return originState(origin);
    const detected = Array.from(state.originStates.values()).filter((item) => item?.kind === "xlyra");
    if (detected.length === 1) {
      session.activeOrigin ||= detected[0].origin;
      return detected[0];
    }
    return { kind: "unknown", xlyra: false, portal: null };
  }

  function formatPercent(numerator, denominator) {
    const numeratorValue = Number(numerator);
    const denominatorValue = Number(denominator);
    if (!Number.isFinite(numeratorValue) || !Number.isFinite(denominatorValue) || denominatorValue <= 0) return "—";
    return `${Math.min(100, Math.max(0, (numeratorValue / denominatorValue) * 100)).toFixed(1)}%`;
  }

  function mainEditable() {
    let nodes = [];
    try {
      nodes = Array.from(document.querySelectorAll("textarea,[contenteditable='true']"));
    } catch {
      nodes = [];
    }
    const height = window.innerHeight || document.documentElement?.clientHeight || 1000;
    return nodes
      .filter((node) => !state.root?.contains?.(node))
      .map((node) => ({ node, rect: node.getBoundingClientRect?.() || { width: 0, height: 0, bottom: 0, top: 0 } }))
      .filter(({ rect }) => rect.width >= 240 && rect.height >= 20 && rect.bottom > 0 && rect.top < height)
      .sort((a, b) => b.rect.bottom - a.rect.bottom)[0]?.node || null;
  }

  function findComposerBox() {
    const editable = mainEditable();
    if (!editable) return null;
    let node = editable;
    let candidate = editable.parentElement || editable;
    for (let depth = 0; node?.parentElement && depth < 8; depth += 1, node = node.parentElement) {
      const rect = node.getBoundingClientRect?.() || { width: 0, height: 0 };
      const style = window.getComputedStyle?.(node);
      if (rect.width >= 320 && rect.height >= 36 && rect.height <= 260 && style?.display !== "contents") candidate = node;
    }
    return candidate;
  }

  function findCodexPlusAnchor() {
    const byId = document.getElementById?.(CODEX_PLUS_MENU_ID);
    if (byId && !state.headerShell?.contains?.(byId)) return byId;
    const selectors = [
      "[data-testid*='codex-plus']",
      "[id*='codex-plus']",
      "[class*='codex-plus']",
    ];
    for (const selector of selectors) {
      const node = document.querySelector?.(selector);
      if (node && !state.headerShell?.contains?.(node)) return node;
    }
    const candidates = Array.from(document.querySelectorAll?.("header button,header [role='button'],nav button,nav [role='button'],button") || []);
    return candidates.find((node) => {
      if (state.headerShell?.contains?.(node)) return false;
      const value = text(`${node.getAttribute?.("aria-label") || ""} ${node.textContent || ""}`, 160);
      return /(^|[\s([{<])Codex\+\+(?=$|[\s)\]}>:：·|\/-])/i.test(value) && node.getBoundingClientRect?.().width > 0;
    }) || null;
  }

  function updateHeaderMenu(info, stats) {
    if (!state.headerMenu) return;
    const usage = stats?.usage || emptyUsage();
    const cacheRate = formatPercent(usage.cached, usage.input);
    const cost = stats?.cost == null ? "—" : formatMoney(stats.cost, stats.currency || "USD");
    const status = stats?.error ? "Portal 读取失败" : stats?.updatedAt ? "来自 xLyra Portal" : "正在读取 xLyra Portal…";
    state.headerMenu.innerHTML = `
      <div class="xlyra-hud-menu-title">今日统计</div>
      <div class="xlyra-hud-menu-row"><span>总 Token</span><strong>${formatCount(usage.total || usage.input + usage.output)}</strong></div>
      <div class="xlyra-hud-menu-row"><span>输入 / 输出</span><strong>${formatCount(usage.input)} / ${formatCount(usage.output)}</strong></div>
      <div class="xlyra-hud-menu-row"><span>缓存量</span><strong>${formatCount(usage.cached)}</strong></div>
      <div class="xlyra-hud-menu-row"><span>缓存命中率</span><strong>${escapeHtml(cacheRate)}</strong></div>
      <div class="xlyra-hud-menu-row"><span>总费用</span><strong>${escapeHtml(cost)}</strong></div>
      <div class="xlyra-hud-menu-row"><span>调用次数</span><strong>${formatCount(stats?.calls || 0)} 次</strong></div>
      <div class="xlyra-hud-menu-foot">${escapeHtml(status)}</div>
    `;
  }

  function ensureHeaderWidget(info, stats) {
    const enabled = info?.kind === "xlyra";
    if (!enabled || !document.body) {
      if (state.headerShell) {
        state.headerShell.hidden = true;
        state.headerShell.dataset.open = "false";
      }
      return;
    }
    if (!state.headerShell) {
      const shell = document.createElement("div");
      shell.id = HEADER_ID;
      shell.dataset.open = "false";
      const button = document.createElement("button");
      button.type = "button";
      button.className = "xlyra-hud-header-button";
      button.setAttribute("aria-haspopup", "dialog");
      button.addEventListener("click", (event) => {
        event.preventDefault();
        event.stopPropagation();
        const open = shell.dataset.open !== "true";
        shell.dataset.open = String(open);
        button.setAttribute("aria-expanded", String(open));
        menu.hidden = !open;
      });
      const menu = document.createElement("div");
      menu.className = "xlyra-hud-header-menu";
      menu.hidden = true;
      shell.append(button, menu);
      state.headerShell = shell;
      state.headerButton = button;
      state.headerMenu = menu;
      state.headerOutsideHandler = (event) => {
        if (shell.dataset.open === "true" && !shell.contains(event.target)) {
          shell.dataset.open = "false";
          button.setAttribute("aria-expanded", "false");
          menu.hidden = true;
        }
      };
      state.headerKeyHandler = (event) => {
        if (event.key === "Escape") {
          shell.dataset.open = "false";
          button.setAttribute("aria-expanded", "false");
          menu.hidden = true;
        }
      };
      document.addEventListener("click", state.headerOutsideHandler, true);
      document.addEventListener("keydown", state.headerKeyHandler, true);
    }
    state.headerShell.hidden = false;
    const anchor = findCodexPlusAnchor();
    const anchorParent = anchor?.parentElement;
    const anchorRect = anchor?.getBoundingClientRect?.();
    const floating = Boolean(anchorParent && (anchorParent === document.body || anchorParent === document.documentElement || window.getComputedStyle?.(anchor).position === "fixed"));
    state.headerShell.dataset.floating = String(floating || !anchorParent);
    if (anchorParent && !floating) {
      state.headerShell.style.left = "";
      state.headerShell.style.top = "";
      state.headerShell.style.right = "";
      if (state.headerShell.parentElement !== anchorParent || anchor.nextSibling !== state.headerShell) anchorParent.insertBefore(state.headerShell, anchor.nextSibling);
    } else {
      if (state.headerShell.parentElement !== document.body) document.body.appendChild(state.headerShell);
      const width = state.headerShell.getBoundingClientRect?.().width || 96;
      const left = anchorRect ? Math.max(8, anchorRect.right + 6) : Math.max(8, window.innerWidth - width - 16);
      const top = anchorRect ? Math.max(8, anchorRect.top) : 12;
      state.headerShell.style.left = `${Math.round(left)}px`;
      state.headerShell.style.top = `${Math.round(top)}px`;
      state.headerShell.style.right = "auto";
    }
    const usage = stats?.usage || emptyUsage();
    state.headerButton.textContent = `今日 ${formatCount(usage.total || usage.input + usage.output)}`;
    state.headerButton.title = "查看今日 xLyra Token 与费用统计";
    state.headerButton.setAttribute("aria-label", `今日 ${formatCount(usage.total || usage.input + usage.output)} Token，查看统计`);
    state.headerButton.setAttribute("aria-expanded", String(state.headerShell.dataset.open === "true"));
    updateHeaderMenu(info, stats);
  }

  function ensureHud() {
    if (!document.body) return null;
    let style = document.getElementById(STYLE_ID);
    if (!style) {
      style = document.createElement("style");
      style.id = STYLE_ID;
      style.textContent = `
        #${ROOT_ID} {
          box-sizing: border-box;
          position: relative;
          display: grid;
          grid-template-columns: minmax(0, 1.7fr) minmax(0, .82fr) minmax(0, .95fr) minmax(0, .95fr) minmax(0, 1fr) minmax(0, 1.35fr);
          align-items: center;
          gap: 0;
          width: min(100%, 760px);
          height: 61px;
          margin: 0 auto -18px;
          padding: 8px 10px 25px;
          border-radius: 20px 20px 0 0;
          background: color-mix(in srgb, var(--color-token-main-surface-secondary, #f4f4f4) 86%, transparent);
          color: var(--color-token-text-tertiary, #6b7280);
          font: 12px/1.35 ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
          letter-spacing: 0;
          z-index: 0;
        }
        #${ROOT_ID} .xlyra-hud-pill {
          box-sizing: border-box;
          display: inline-flex;
          align-items: center;
          justify-content: center;
          min-width: 0;
          width: 100%;
          padding: 0 7px;
          white-space: nowrap;
          overflow: hidden;
          text-overflow: ellipsis;
          color: var(--color-token-text-tertiary, #6b7280);
          font-variant-numeric: tabular-nums;
        }
        #${ROOT_ID} .xlyra-hud-pill strong { color: var(--color-token-text-primary, #111827); font-weight: 500; }
        #${ROOT_ID} .xlyra-hud-model { overflow: hidden; text-overflow: ellipsis; }
        #${ROOT_ID} .xlyra-hud-muted { color: var(--color-token-text-tertiary, #9ca3af); }
        #${HEADER_ID} {
          box-sizing: border-box;
          position: relative;
          display: inline-flex;
          align-items: center;
          z-index: 2147483647;
          color-scheme: light dark;
          font: 13px/18px ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
        }
        #${HEADER_ID}[data-floating="true"] { position: fixed; }
        #${HEADER_ID} .xlyra-hud-header-button {
          box-sizing: border-box;
          min-width: 76px;
          height: 30px;
          padding: 0 8px;
          border: 0;
          border-radius: 7px;
          background: transparent;
          color: var(--color-token-text-tertiary, #6b7280);
          cursor: pointer;
          pointer-events: auto;
          -webkit-app-region: no-drag;
          white-space: nowrap;
          font: inherit;
        }
        #${HEADER_ID} .xlyra-hud-header-button:hover,
        #${HEADER_ID} .xlyra-hud-header-button:focus-visible { background: var(--color-token-list-hover-background, rgba(0,0,0,.06)); outline: none; }
        #${HEADER_ID} .xlyra-hud-header-menu {
          position: absolute;
          top: calc(100% + 6px);
          right: 0;
          width: 238px;
          box-sizing: border-box;
          padding: 10px 12px;
          border: 1px solid var(--color-token-border-light, rgba(128,128,128,.25));
          border-radius: 12px;
          background: var(--color-token-dropdown-background, var(--color-token-main-surface-primary, #fff));
          color: var(--color-token-text-primary, #111827);
          box-shadow: 0 12px 32px rgba(0,0,0,.18);
          pointer-events: auto;
        }
        #${HEADER_ID}[data-floating="true"] .xlyra-hud-header-menu { position: fixed; top: 45px; right: 0; }
        #${HEADER_ID} .xlyra-hud-menu-title { margin-bottom: 6px; font-weight: 600; }
        #${HEADER_ID} .xlyra-hud-menu-row { display: flex; justify-content: space-between; gap: 12px; padding: 4px 0; color: var(--color-token-text-tertiary, #6b7280); }
        #${HEADER_ID} .xlyra-hud-menu-row strong { color: var(--color-token-text-primary, #111827); font-weight: 500; font-variant-numeric: tabular-nums; }
        #${HEADER_ID} .xlyra-hud-menu-foot { margin-top: 6px; color: var(--color-token-text-tertiary, #9ca3af); font-size: 11px; }
      `;
      document.head?.appendChild(style);
    }
    const composer = findComposerBox();
    if (!composer?.parentElement) {
      state.root?.remove();
      return null;
    }
    if (!state.root) {
      state.root = document.createElement("section");
      state.root.id = ROOT_ID;
      state.root.setAttribute("aria-live", "polite");
    }
    if (state.root.parentElement !== composer.parentElement || state.root.nextElementSibling !== composer) composer.parentElement.insertBefore(state.root, composer);
    return state.root;
  }

  function render() {
    state.renderTimer = 0;
    syncActiveSession();
    const root = ensureHud();
    const session = getSession(activeSessionKey());
    const turn = session.current || session.lastTurn;
    const usage = turnDisplayUsage(turn);
    const sessionStats = aggregateSession(session);
    const info = currentOriginInfo(session, turn);
    const origin = text(turn?.origin, 400) || text(session.activeOrigin, 400);
    const todayStats = info.kind === "xlyra" ? state.todayStats.get(origin) || emptyTodayStats() : null;
    if (info.kind === "xlyra") refreshTodayStats(origin, info);
    ensureHeaderWidget(info, todayStats);
    if (!root) return;
    const running = Boolean(session.current?.status === "running");
    const status = running ? (turn?.finalReady ? "xLyra 已回填" : "进行中") : turn ? (turn.finalReady ? "已完成" : "已完成·待回填") : "等待调用";
    const source = turn?.finalReady ? "xLyra 最终数据" : usage.exact ? "Codex token_count" : "等待 Token 数据";
    const costAvailable = info.kind === "xlyra" && info.portal?.readable !== false && info.portal?.cost !== false;
    const currentCost = turnCostSummary(turn);
    const cost = currentCost.cost != null ? formatMoney(currentCost.cost, currentCost.currency || "USD") : costAvailable ? "待结算" : "—";
    const sessionCost = sessionStats.cost == null ? "—" : formatMoney(sessionStats.cost, sessionStats.currency);
    const connection = info.kind === "xlyra" ? (info.portal?.readable === false ? "xLyra · Portal 未启用" : "xLyra · Portal 已连接") : info.kind === "other" ? "非 xLyra · 费用功能已禁用" : "检测上游中";
    const model = text(turn?.model, 120) || "模型未识别";
    const sessionTotal = sessionStats.usage.total || sessionStats.usage.input + sessionStats.usage.output;
    root.innerHTML = `
      <span class="xlyra-hud-pill">本轮&nbsp;输入 <strong>${formatCount(usage.input)}</strong>&nbsp; 输出 <strong>${formatCount(usage.output)}</strong>&nbsp; · ${formatCount(turn?.calls?.size || 0)} 次</span>
      <span class="xlyra-hud-pill">会话 <strong>${formatCount(sessionTotal)}</strong></span>
      <span class="xlyra-hud-pill">缓存 <strong>${formatCount(sessionStats.usage.cached)}</strong>&nbsp;(${escapeHtml(formatPercent(sessionStats.usage.cached, sessionStats.usage.input))})</span>
      <span class="xlyra-hud-pill">本轮费用 <strong class="${cost === "—" || cost === "待结算" ? "xlyra-hud-muted" : ""}">${escapeHtml(cost)}</strong></span>
      <span class="xlyra-hud-pill">会话费用 <strong class="${sessionCost === "—" ? "xlyra-hud-muted" : ""}">${escapeHtml(sessionCost)}</strong></span>
      <span class="xlyra-hud-pill xlyra-hud-model" title="${escapeHtml(model)}">${escapeHtml(model)}</span>
    `;
    root.dataset.status = status;
    root.dataset.source = source;
    root.title = `${status} · ${connection}`;
  }

  function scheduleRender(delay = RENDER_THROTTLE_MS) {
    if (state.renderTimer) return;
    state.renderTimer = window.setTimeout(render, Math.max(0, delay));
  }

  function installCapture() {
    installFetchCapture();
    installXhrCapture();
    installWebSocketCapture();
    installMessageCapture();
  }

  function start() {
    if (state.started) return;
    state.started = true;
    installCapture();
    installDomObservers();
    syncActiveSession();
    render();
    window.setTimeout(render, 1000);
  }

  function restoreCapture() {
    if (window.fetch?.__xlyraCodexSessionHudWrapped === VERSION) window.fetch = window.fetch.__xlyraCodexSessionHudOriginal;
    const Xhr = state.originalXHR;
    if (Xhr?.prototype?.__xlyraCodexSessionHudWrapped === VERSION) {
      Xhr.prototype.open = Xhr.prototype.__xlyraCodexSessionHudOriginalOpen;
      Xhr.prototype.send = Xhr.prototype.__xlyraCodexSessionHudOriginalSend;
      if (Xhr.prototype.__xlyraCodexSessionHudOriginalSetRequestHeader) Xhr.prototype.setRequestHeader = Xhr.prototype.__xlyraCodexSessionHudOriginalSetRequestHeader;
      delete Xhr.prototype.__xlyraCodexSessionHudWrapped;
    }
    if (window.WebSocket?.__xlyraCodexSessionHudWrapped === VERSION) window.WebSocket = window.WebSocket.__xlyraCodexSessionHudOriginal;
    if (state.messageHandler) window.removeEventListener("message", state.messageHandler, true);
  }

  function destroy() {
    state.started = false;
    if (state.renderTimer) window.clearTimeout(state.renderTimer);
    removeDomObservers();
    for (const session of state.sessions.values()) {
      if (session.current?.finalizeTimer) window.clearTimeout(session.current.finalizeTimer);
    }
    restoreCapture();
    if (state.headerOutsideHandler) document.removeEventListener("click", state.headerOutsideHandler, true);
    if (state.headerKeyHandler) document.removeEventListener("keydown", state.headerKeyHandler, true);
    state.headerOutsideHandler = null;
    state.headerKeyHandler = null;
    state.headerShell?.remove();
    state.root?.remove();
    document.getElementById(STYLE_ID)?.remove();
    state.root = null;
    state.headerShell = null;
    state.headerButton = null;
    state.headerMenu = null;
    state.messageHandler = null;
    delete window.__xlyraCodexSessionHud;
    if (window.__xlyraCodexSessionHudVersion === VERSION) delete window.__xlyraCodexSessionHudVersion;
  }

  window.__xlyraCodexSessionHud = {
    version: VERSION,
    destroy,
    render,
    inspectPayload,
    normalizeUsage,
    isTokenCountPayload,
    collectUsages,
    detectOrigin,
  };

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start, { once: true });
  else start();
  window.setTimeout(start, 1200);
})();
