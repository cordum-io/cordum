const STORAGE_KEY = "cordum-mock-bank-config";
const DEFAULT_CONFIG = {
  apiBaseUrl: "",
  apiKey: "",
  tenantId: "default",
  principalId: "demo",
  principalRole: "admin",
  orgId: "default",
};

const WORKFLOW_ID = "demo-mock-bank.transfer";

const state = {
  balance: 1_000_000,
  transactions: [],
  requests: [],
  config: loadConfig(),
};

const ui = {
  balance: document.getElementById("balance"),
  lastUpdated: document.getElementById("lastUpdated"),
  transactionsBody: document.getElementById("transactionsBody"),
  transactionsEmpty: document.getElementById("transactionsEmpty"),
  chatBody: document.getElementById("chatBody"),
  chatForm: document.getElementById("chatForm"),
  chatInput: document.getElementById("chatInput"),
};

init();

function init() {
  renderBalance();
  renderTransactions();
  appendChat("agent", "Welcome to MegaCorp Bank. I can help with transfer requests.");
  bindEvents();
}

function bindEvents() {
  if (!ui.chatForm) {
    return;
  }
  ui.chatForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const text = ui.chatInput.value.trim();
    if (!text) {
      return;
    }
    ui.chatInput.value = "";
    appendChat("client", text);
    handleChatMessage(text);
  });
}

function appendChat(role, text) {
  if (!ui.chatBody) {
    return;
  }
  const bubble = document.createElement("div");
  bubble.className = `chat-bubble ${role}`;
  bubble.textContent = text;
  ui.chatBody.appendChild(bubble);
  ui.chatBody.scrollTop = ui.chatBody.scrollHeight;
}

function handleChatMessage(text) {
  const amount = parseAmount(text);
  if (!amount) {
    appendChat("agent", respondToMessage(text));
    return;
  }

  appendChat("agent", `Processing transfer request for ${formatMoney(amount)}.`);
  submitTransfer({
    amount,
    customer: "Alex Morgan",
    reason: "Client transfer request",
    note: text,
  });
}

function respondToMessage(text) {
  const lower = text.toLowerCase();
  if (/(hi|hello|hey|good morning|good afternoon|good evening)/.test(lower)) {
    return "Hello Alex. How can I help today?";
  }
  if (/(transfer|send|wire|payment|payout)/.test(lower)) {
    return "Certainly. What amount should I transfer?";
  }
  return "I can help with transfers. Just let me know the amount.";
}

function formatMoney(amount) {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 2,
  }).format(amount);
}

function formatDate(date = new Date()) {
  return date.toLocaleString("en-US", {
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function renderBalance() {
  if (ui.balance) {
    ui.balance.textContent = formatMoney(state.balance);
  }
  if (ui.lastUpdated) {
    ui.lastUpdated.textContent = `Updated ${formatDate()}`;
  }
}

function renderTransactions() {
  if (!ui.transactionsBody) {
    return;
  }
  ui.transactionsBody.innerHTML = "";
  state.transactions.forEach((tx) => {
    const row = document.createElement("tr");
    row.innerHTML = `
      <td>${tx.date}</td>
      <td>${tx.memo}</td>
      <td class="num">${tx.amount}</td>
      <td>${tx.status}</td>
    `;
    ui.transactionsBody.appendChild(row);
  });
  if (ui.transactionsEmpty) {
    ui.transactionsEmpty.style.display = state.transactions.length ? "none" : "block";
  }
}

function parseAmount(text) {
  const cleaned = text.replace(/,/g, "");
  const match = cleaned.match(/\$?\s*(\d+(?:\.\d{1,2})?)/);
  if (!match) {
    return 0;
  }
  return Number(match[1]);
}

async function submitTransfer({ amount, customer, reason, note }) {
  const request = {
    id: `req-${Date.now()}`,
    amount,
    customer,
    reason,
    note,
    workflowId: WORKFLOW_ID,
    status: "processing",
  };
  state.requests.unshift(request);

  try {
    const payload = {
      amount,
      currency: "USD",
      customer,
      reason,
      note,
      requested_by: "agent-demo",
    };
    const runResp = await apiRequest(`/api/v1/workflows/${WORKFLOW_ID}/runs`, {
      method: "POST",
      body: payload,
      query: { org_id: state.config.orgId },
    });
    request.runId = runResp.run_id;
    appendChat("agent", "Please wait, waiting for representative for approval.");

    await pollRun(request);
  } catch (err) {
    request.status = "error";
    appendChat("agent", buildErrorMessage(err));
  }
}

async function pollRun(request) {
  let done = false;
  while (!done) {
    let run;
    try {
      run = await apiRequest(`/api/v1/workflow-runs/${encodeURIComponent(request.runId)}`);
    } catch (err) {
      if (isNotFound(err)) {
        await sleep(800);
        continue;
      }
      throw err;
    }
    const status = String(run.status || "").toLowerCase();

    if (status === "waiting") {
      setRequestStatus(request, "approval", () => {
        appendChat(
          "agent",
          "This transfer requires manager approval per safety policy. Open the Cordum dashboard to approve or reject.",
        );
      });
    } else if (status === "denied") {
      setRequestStatus(request, "blocked", () => {
        appendChat(
          "agent",
          "This transfer was blocked by the Safety Kernel. Transfers of $300 or more are not permitted.",
        );
      });
      done = true;
    } else if (status === "succeeded") {
      setRequestStatus(request, "completed", () => {
        applyTransaction(request);
        appendChat("agent", "Transfer completed. Your balance has been updated.");
      });
      done = true;
    } else if (status === "failed") {
      const hasDeniedStep = run.steps && Object.values(run.steps).some(
        (s) => s.status === "denied" || (s.safety_decision && s.safety_decision.type === "deny"),
      );
      if (hasDeniedStep) {
        setRequestStatus(request, "blocked", () => {
          appendChat("agent", "This transfer was blocked by the Safety Kernel.");
        });
      } else {
        setRequestStatus(request, "blocked", () => {
          appendChat("agent", "The transfer could not be completed.");
        });
      }
      done = true;
    } else if (["cancelled", "timed_out"].includes(status)) {
      setRequestStatus(request, "blocked", () => {
        appendChat("agent", `The transfer was ${status.replace("_", " ")}.`);
      });
      done = true;
    }

    if (!done) {
      await sleep(1200);
    }
  }
}

function applyTransaction(request) {
  const amount = Number(request.amount);
  if (!amount) {
    return;
  }
  state.balance -= amount;
  state.transactions.unshift({
    date: formatDate(),
    memo: `Transfer for ${request.customer}`,
    amount: `-${formatMoney(amount)}`,
    status: "Completed",
  });
  renderBalance();
  renderTransactions();
}

function setRequestStatus(request, status, onChange) {
  const prev = request.status;
  request.status = status;
  if (prev !== status && typeof onChange === "function") {
    onChange();
  }
}

async function apiRequest(path, options = {}) {
  const base = state.config.apiBaseUrl;
  const url = base ? new URL(path, base) : new URL(path, window.location.origin);
  if (options.query) {
    Object.entries(options.query).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") {
        return;
      }
      url.searchParams.set(key, String(value));
    });
  }

  const headers = new Headers(options.headers || {});
  headers.set("Accept", "application/json");
  if (!ensureApiKey()) {
    throw new Error("API key is required.");
  }
  headers.set("X-API-Key", state.config.apiKey);
  const tenantId = state.config.tenantId || state.config.orgId;
  if (tenantId) {
    headers.set("X-Tenant-ID", tenantId);
  }
  if (state.config.principalId) {
    headers.set("X-Principal-Id", state.config.principalId);
  }
  if (state.config.principalRole) {
    headers.set("X-Principal-Role", state.config.principalRole);
  }

  let body = options.body;
  if (body && typeof body === "object" && !(body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(body);
  }

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 15_000);
  try {
    const res = await fetch(url.toString(), {
      method: options.method || "GET",
      headers,
      body,
      signal: controller.signal,
    });
    if (!res.ok) {
      const message = await res.text();
      const err = new Error(message || `Request failed (${res.status})`);
      err.status = res.status;
      throw err;
    }
    if (res.status === 204) {
      return null;
    }
    return await res.json();
  } finally {
    clearTimeout(timeout);
  }
}

function loadConfig() {
  const stored = loadStoredConfig();
  const query = loadQueryConfig();
  const config = sanitizeConfig({ ...stored, ...query });
  if (Object.keys(query).length) {
    persistConfig(config);
  }
  return config;
}

function loadStoredConfig() {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    return parsed;
  } catch (err) {
    return {};
  }
}

function loadQueryConfig() {
  if (typeof window === "undefined" || !window.location || !window.location.search) {
    return {};
  }
  const params = new URLSearchParams(window.location.search);
  const config = {};
  const apiBaseUrl = params.get("apiBaseUrl");
  if (apiBaseUrl) {
    config.apiBaseUrl = apiBaseUrl;
  }
  const apiKey = params.get("apiKey");
  if (apiKey) {
    config.apiKey = apiKey;
  }
  const tenantId = params.get("tenantId") || params.get("tenant_id");
  if (tenantId) {
    config.tenantId = tenantId;
  }
  const principalId = params.get("principalId");
  if (principalId) {
    config.principalId = principalId;
  }
  const principalRole = params.get("principalRole");
  if (principalRole) {
    config.principalRole = principalRole;
  }
  const orgId = params.get("orgId");
  if (orgId) {
    config.orgId = orgId;
  }
  return config;
}

function sanitizeConfig(input) {
  const next = input || {};
  const tenantId = next.tenantId || next.orgId || DEFAULT_CONFIG.tenantId;
  const orgId = next.orgId || tenantId || DEFAULT_CONFIG.orgId;
  return {
    apiBaseUrl: normalizeBaseUrl(next.apiBaseUrl || DEFAULT_CONFIG.apiBaseUrl),
    apiKey: next.apiKey || DEFAULT_CONFIG.apiKey,
    tenantId,
    principalId: next.principalId || DEFAULT_CONFIG.principalId,
    principalRole: next.principalRole || DEFAULT_CONFIG.principalRole,
    orgId,
  };
}

function persistConfig(config) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(config));
  } catch (err) {
    // Ignore storage failures.
  }
}

function normalizeBaseUrl(url) {
  if (!url) {
    return "";
  }
  let next = String(url).trim();
  if (!next) {
    return "";
  }
  if (next.startsWith(":")) {
    next = `http://localhost${next}`;
  } else if (next.startsWith("//")) {
    next = `http:${next}`;
  } else if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(next)) {
    next = `http://${next}`;
  }
  return next.replace(/\/+$/, "");
}

function ensureApiKey() {
  if (state.config.apiKey) {
    return true;
  }
  const entered = window.prompt("Enter your Cordum API key to continue:");
  if (entered && entered.trim()) {
    state.config.apiKey = entered.trim();
    persistConfig(state.config);
    return true;
  }
  return false;
}

function buildErrorMessage(err) {
  const message = String(err && err.message ? err.message : err || "");
  const lower = message.toLowerCase();
  if (lower.includes("failed to fetch")) {
    return "I could not reach the bank API. Please ensure the Cordum API is running and the page is served over http://localhost.";
  }
  if (lower.includes("401") || lower.includes("unauthorized")) {
    return "The request was rejected. Check your API key and retry (refresh to re-enter).";
  }
  if (lower.includes("403") || lower.includes("tenant")) {
    return "The request was rejected. Set a tenant (try ?tenantId=default) and reload.";
  }
  if (message.trim()) {
    return `I could not submit the request. ${message}`;
  }
  return "I could not submit the request due to a network error.";
}

function isNotFound(err) {
  if (!err) {
    return false;
  }
  if (typeof err.status === "number" && err.status === 404) {
    return true;
  }
  const message = String(err.message || err).toLowerCase();
  return message.includes("404") || message.includes("not found");
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
