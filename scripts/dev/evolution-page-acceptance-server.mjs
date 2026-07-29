import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = new URL("../../web-frontend/dist/", import.meta.url);
const rootPath = fileURLToPath(root);
const port = Number(process.argv[2] || 4174);
const now = Date.now();
const instruments = [
  { id: "BTCUSDT", symbol: "BTCUSDT", display_name: "Bitcoin", data_source: "binance", supported_intervals: ["1d", "1h"], market: "crypto" },
  { id: "SOXL", symbol: "SOXL", display_name: "SOXL", data_source: "yahoo", supported_intervals: ["1d"], market: "us" },
  { id: "SPY", symbol: "SPY", display_name: "SPY", data_source: "yahoo", supported_intervals: ["1d"], market: "us" }
];
const requests = [];
let lastTaskInput = null;
let taskMode = "empty";

function json(response, status, payload) {
  response.writeHead(status, { "Content-Type": "application/json; charset=utf-8" });
  response.end(JSON.stringify(payload));
}

async function body(request) {
  let raw = "";
  for await (const chunk of request) raw += chunk;
  return raw ? JSON.parse(raw) : {};
}

function statusPayload(id) {
  const instrument = instruments.find((item) => item.id === id) ?? instruments[0];
  return {
    instrument,
    instrument_id: instrument.id,
    data_source: instrument.data_source,
    symbol: instrument.symbol,
    supported_intervals: instrument.supported_intervals,
    datasets: instrument.supported_intervals.map((interval) => ({
      instrument_id: instrument.id,
      data_source: instrument.data_source,
      symbol: instrument.symbol,
      interval,
      count: 3000,
      first_open_ms: now - 3000 * 86400000,
      last_open_ms: now
    }))
  };
}

const server = createServer(async (request, response) => {
  const url = new URL(request.url || "/", `http://${request.headers.host}`);
  requests.push(`${request.method} ${url.pathname}${url.search}`);
  if (requests.length > 500) requests.splice(0, requests.length - 500);
  if (url.pathname === "/acceptance/requests") {
    json(response, 200, { requests, last_task_input: lastTaskInput, task_mode: taskMode });
    return;
  }
  if (url.pathname === "/acceptance/task-mode" && request.method === "POST") {
    const input = await body(request);
    taskMode = input.mode || "empty";
    json(response, 200, { task_mode: taskMode });
    return;
  }
  if (url.pathname === "/api/v1/auth/login" && request.method === "POST") {
    json(response, 200, { token: "acceptance-token", user: { id: 1, email: "acceptance@example.test", role: "admin" } });
    return;
  }
  if (url.pathname === "/api/v1/auth/me") {
    json(response, 200, { id: 1, email: "acceptance@example.test", role: "admin" });
    return;
  }
  if (url.pathname === "/api/v1/market-data/instruments") {
    json(response, 200, { instruments, execution_modes: ["close_next_open", "close_same_bar"] });
    return;
  }
  if (url.pathname === "/api/v1/market-data/klines/status") {
    json(response, 200, statusPayload(url.searchParams.get("instrument_id") || "BTCUSDT"));
    return;
  }
  if (url.pathname === "/api/v1/research-datasets") {
    json(response, 200, { datasets: [] });
    return;
  }
  if (url.pathname === "/api/v1/evolution/genomes") {
    json(response, 200, []);
    return;
  }
  if (url.pathname === "/api/v1/evolution/tasks" && request.method === "GET") {
    if (taskMode === "empty") {
      json(response, 200, { current_task: null, running: false, tasks: [], latest_challenger: null, champion: null, window_summaries: {} });
      return;
    }
    const hasBest = taskMode === "running_best";
    const status = hasBest ? "running" : taskMode;
    const task = {
      id: 99,
      status,
      progress: 0.36,
      current_generation: 4,
      pop_size: 30,
      max_generations: 10,
      evaluated_count: 10,
      valid_count: hasBest ? 5 : 0,
      skipped_count: 3,
      failed_count: 2,
      planned_evaluations: 300,
      best_valid: hasBest,
      best_score: hasBest ? -0.42 : null,
      max_drawdown: hasBest ? 0.37 : null,
      grid_coverage_enabled: true,
      multi_market_enabled: true,
      multi_market_selections: [
        { instrument_id: "BTCUSDT", pair: "BTCUSDT", data_source: "binance", interval: "1d", use_all_data: true },
        { instrument_id: "SOXL", pair: "SOXL", data_source: "yahoo", interval: "1d", use_all_data: false, start_time_ms: now - 3000 * 86400000, end_time_ms: now }
      ],
      market_performance: hasBest ? [
        { instrument_id: "BTCUSDT", pair: "BTCUSDT", data_source: "binance", interval: "1d", total_return: -0.1, annualized_return: -0.04, max_drawdown: 0.22 },
        { instrument_id: "SOXL", pair: "SOXL", data_source: "yahoo", interval: "1d", total_return: -0.35, annualized_return: -0.2, max_drawdown: 0.37 }
      ] : [],
      created_at: new Date(now - 60_000).toISOString(),
      started_at: new Date(now - 55_000).toISOString()
    };
    json(response, 200, { current_task: task, running: ["pending", "running", "cancelling"].includes(status), tasks: [task], latest_challenger: null, champion: null, window_summaries: {} });
    return;
  }
  if (url.pathname === "/api/v1/evolution/tasks/99/grid-coverage") {
    json(response, 200, {
      task_id: 99,
      search_hash: "acceptance-search",
      axes: [
        { key: "beta", label: "Beta", kind: "float", state: "evolving", minimum: 0.1, maximum: 4, step: 0.05, grid_size: 79, total_count: 10, last_generation: 1, points: [{ value: 1, count: 4 }, { value: 1.05, count: 6 }] },
        { key: "micro_reserve_pct", label: "微觀保留比例", kind: "float", state: "disabled", minimum: 0, maximum: 0.95, step: 0.05, grid_size: 1, total_count: 10, last_generation: 1, points: [{ value: 0, count: 10 }] }
      ],
      generations: [{ generation: 0, count: 5 }, { generation: 1, count: 5 }]
    });
    return;
  }
  if (url.pathname === "/api/v1/evolution/tasks/99/cancel" && request.method === "POST") {
    taskMode = "cancelling";
    json(response, 202, { status: taskMode, task_id: 99 });
    return;
  }
  if (url.pathname === "/api/v1/evolution/tasks/compute-estimate") {
    json(response, 200, { enabled: true, units_per_individual: 6000, planned_units: 9000000 });
    return;
  }
  if (url.pathname === "/api/v1/evolution/tasks" && request.method === "POST") {
    const input = await body(request);
    lastTaskInput = input;
    if (input.monthly_dca !== 12345) {
      json(response, 409, { error: "驗收模擬：建立失敗，表單必須維持展開" });
      return;
    }
    json(response, 202, {
      id: 99,
      status: "pending",
      progress: 0,
      created_at: new Date().toISOString(),
      ...input
    });
    return;
  }
  if (url.pathname.startsWith("/api/")) {
    json(response, 404, { error: `acceptance mock has no route ${url.pathname}` });
    return;
  }

  const requested = normalize(url.pathname).replace(/^([/\\])+/, "");
  const fileName = requested && extname(requested) ? requested : "index.html";
  let selectedFile = join(rootPath, fileName);
  let type = extname(fileName) === ".js" ? "text/javascript" : extname(fileName) === ".css" ? "text/css" : "text/html";
  let bytes;
  try {
    bytes = await readFile(selectedFile);
  } catch {
    selectedFile = join(rootPath, "index.html");
    type = "text/html";
    bytes = await readFile(selectedFile);
  }
  if (selectedFile.endsWith("index.html")) {
    bytes = Buffer.from(bytes.toString("utf8").replace("</head>", "<script>window.localStorage.setItem('qs_jwt','acceptance-token')</script></head>"));
  }
  response.writeHead(200, { "Content-Type": `${type}; charset=utf-8` });
  response.end(bytes);
});

server.listen(port, "127.0.0.1");
