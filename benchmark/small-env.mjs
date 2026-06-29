#!/usr/bin/env node
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import https from "node:https";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const benchmarkDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(benchmarkDir, "..");

const defaults = {
  customers: 8,
  durationMs: 20_000,
  products: 2,
  sampleIntervalMs: 250,
  staff: 2,
  ticketsPerCustomer: 3,
  userThinkMs: 300
};

async function main() {
  if (os.platform() !== "linux") {
    throw new Error("small-env benchmark currently requires Linux /proc RSS metrics");
  }

  const config = parseConfig(process.argv.slice(2));
  if (config.help) {
    printHelp();
    return;
  }

  const tempDir = await mkdtemp(path.join(os.tmpdir(), "pappice-bench-"));
  const binaryPath = path.join(tempDir, "pappice");
  const certPath = path.join(tempDir, "localhost.pem");
  const keyPath = path.join(tempDir, "localhost-key.pem");
  const dbPath = path.join(tempDir, "pappice.db");
  const uploadDir = path.join(tempDir, "uploads");
  const backupDir = path.join(tempDir, "backups");
  let appProcess = null;
  const clients = [];

  try {
    await runCommand("go", ["build", "-o", binaryPath, "./cmd/pappice"], { cwd: repoRoot });
    await generateCertificate(certPath, keyPath);

    const appPort = await freePort();
    const appURL = `https://127.0.0.1:${appPort}`;
    appProcess = startApp({ appURL, appPort, backupDir, binaryPath, certPath, dbPath, keyPath, uploadDir, tempDir });
    await waitForHTTPS(`${appURL}/api/health`, appProcess);

    const startupRSS = await readRSS(appProcess.pid);
    const scenario = await seedScenario(appURL, config, clients);
    const seededRSS = await readRSS(appProcess.pid);
    await warmup(scenario.sessions);
    const measured = await measure(appProcess, scenario.sessions, config);
    const result = {
      benchmark: "small-env",
      version: await currentVersion(),
      measured_at: new Date().toISOString(),
      scenario: {
        products: scenario.productIDs.length,
        staff: scenario.staffSessions.length,
        customers: scenario.customerSessions.length,
        tickets: scenario.ticketIDs.length,
        duration_ms: config.durationMs,
        sample_interval_ms: config.sampleIntervalMs,
        user_think_ms: config.userThinkMs
      },
      rss_bytes: {
        startup: startupRSS,
        seeded: seededRSS,
        min: measured.rss.min,
        mean: measured.rss.mean,
        p50: measured.rss.p50,
        p95: measured.rss.p95,
        max: measured.rss.max
      },
      requests: measured.requests,
      samples: measured.samples.length
    };

    if (config.json) {
      console.log(JSON.stringify(result, null, 2));
    } else {
      printResult(result);
    }
  } finally {
    for (const client of clients) client.close();
    await stopProcess(appProcess);
    if (!config.keepTemp) {
      await rm(tempDir, { force: true, recursive: true });
    } else {
      console.error(`kept benchmark temp dir: ${tempDir}`);
    }
  }
}

function parseConfig(args) {
  const config = {
    ...defaults,
    json: false,
    keepTemp: false
  };
  const setters = {
    customers: (value) => config.customers = positiveInt(value, "customers"),
    duration: (value) => config.durationMs = parseDuration(value, "duration"),
    products: (value) => config.products = positiveInt(value, "products"),
    "sample-interval": (value) => config.sampleIntervalMs = parseDuration(value, "sample-interval"),
    staff: (value) => config.staff = positiveInt(value, "staff"),
    "tickets-per-customer": (value) => config.ticketsPerCustomer = positiveInt(value, "tickets-per-customer"),
    "user-think": (value) => config.userThinkMs = parseDuration(value, "user-think")
  };

  for (const arg of args) {
    if (arg === "--help" || arg === "-h") {
      config.help = true;
      continue;
    }
    if (arg === "--json") {
      config.json = true;
      continue;
    }
    if (arg === "--keep-temp") {
      config.keepTemp = true;
      continue;
    }
    const match = arg.match(/^--([^=]+)=(.+)$/);
    if (!match || !setters[match[1]]) {
      throw new Error(`unknown option: ${arg}`);
    }
    setters[match[1]](match[2]);
  }
  return config;
}

function printHelp() {
  console.log(`Usage: npm run bench:small -- [options]

Starts an isolated HTTPS Pappice instance, seeds a small support desk, keeps
authenticated sessions active, and samples Pappice process RSS.

Options:
  --customers=N              customer sessions, default ${defaults.customers}
  --staff=N                  staff sessions, default ${defaults.staff}
  --products=N               products, default ${defaults.products}
  --tickets-per-customer=N   tickets opened by each customer, default ${defaults.ticketsPerCustomer}
  --duration=20s             measurement duration
  --sample-interval=250ms    RSS sampling interval
  --user-think=300ms         delay between each simulated user loop
  --json                     print machine-readable JSON
  --keep-temp                keep the temporary benchmark directory
`);
}

async function seedScenario(appURL, config, clients) {
  const admin = new APIClient(appURL);
  clients.push(admin);
  await admin.post("/api/setup", {
    display_name: "Bench Admin",
    email: "admin@example.test",
    password: "correct horse battery staple"
  }, 201);

  const productIDs = await ensureProducts(admin, config.products);
  const staffSessions = [];
  for (let i = 0; i < config.staff; i++) {
    const email = `staff${i + 1}@example.test`;
    const user = await admin.post("/api/users", {
      display_name: `Bench Staff ${i + 1}`,
      email,
      password: "correct horse battery staple",
      role: "staff"
    }, 201);
    for (const productID of productIDs) {
      await admin.post(`/api/products/${productID}/members`, { user_id: user.id, role: "staff" }, 201);
    }
    const session = new APIClient(appURL, `staff ${i + 1}`);
    clients.push(session);
    await session.post("/api/login", { email, password: "correct horse battery staple" });
    staffSessions.push(session);
  }

  const customerSessions = [];
  const customerTickets = new Map();
  for (let i = 0; i < config.customers; i++) {
    const email = `customer${i + 1}@example.test`;
    const productID = productIDs[i % productIDs.length];
    const user = await admin.post("/api/users", {
      display_name: `Bench Customer ${i + 1}`,
      email,
      password: "correct horse battery staple",
      role: "customer"
    }, 201);
    await admin.post(`/api/products/${productID}/members`, { user_id: user.id, role: "customer" }, 201);
    const session = new APIClient(appURL, `customer ${i + 1}`);
    clients.push(session);
    await session.post("/api/login", { email, password: "correct horse battery staple" });
    customerSessions.push(session);
    customerTickets.set(session, []);
  }

  const ticketIDs = [];
  for (let i = 0; i < customerSessions.length; i++) {
    const session = customerSessions[i];
    const productID = productIDs[i % productIDs.length];
    for (let ticketIndex = 0; ticketIndex < config.ticketsPerCustomer; ticketIndex++) {
      const ticket = await session.post("/api/tickets", {
        product_id: productID,
        title: `Benchmark request ${i + 1}.${ticketIndex + 1}`,
        description: "A repeatable small-team support ticket used by the memory benchmark."
      }, 201);
      ticketIDs.push(ticket.id);
      customerTickets.get(session).push(ticket.id);
    }
  }

  const responder = staffSessions[0] || admin;
  for (const ticketID of ticketIDs.slice(0, Math.min(ticketIDs.length, config.customers))) {
    await responder.post(`/api/tickets/${ticketID}/comments`, {
      body: "Benchmark staff reply.",
      visibility: "public"
    }, 201);
  }

  return {
    customerSessions,
    customerTickets,
    productIDs,
    sessions: [...customerSessions, ...staffSessions],
    staffSessions,
    ticketIDs
  };
}

async function ensureProducts(admin, count) {
  const payload = await admin.get("/api/products");
  const products = [...payload.products];
  while (products.length < count) {
    const index = products.length + 1;
    products.push(await admin.post("/api/products", {
      key: `BENCH${index}`,
      name: `Benchmark Product ${index}`
    }, 201));
  }
  return products.slice(0, count).map((product) => product.id);
}

async function warmup(sessions) {
  await Promise.all(sessions.map(async (session) => {
    await session.get("/api/session");
    await session.get("/api/tickets?status=new&status=assigned&include_unread_outside_status=1");
  }));
}

async function measure(appProcess, sessions, config) {
  const samples = [];
  const counters = { failed: 0, ok: 0 };
  const state = { stop: false };
  const sampler = setInterval(async () => {
    try {
      samples.push(await readRSS(appProcess.pid));
    } catch {
      // Process shutdown is handled below.
    }
  }, config.sampleIntervalMs);
  sampler.unref();

  const loops = sessions.map((session, index) => userLoop(session, index, config, counters, state));
  await sleep(config.durationMs);
  state.stop = true;
  await Promise.all(loops);
  clearInterval(sampler);
  samples.push(await readRSS(appProcess.pid));

  return {
    requests: {
      failed: counters.failed,
      ok: counters.ok,
      per_second: Number((counters.ok / (config.durationMs / 1000)).toFixed(2))
    },
    rss: summarize(samples),
    samples
  };
}

async function userLoop(session, index, config, counters, state) {
  let cursor = index;
  while (!state.stop) {
    try {
      const list = await session.get("/api/tickets?status=new&status=assigned&include_unread_outside_status=1");
      counters.ok++;
      const tickets = list.tickets || [];
      if (tickets.length > 0) {
        const ticket = tickets[cursor % tickets.length];
        cursor++;
        await session.get(`/api/tickets/${ticket.id}`);
        counters.ok++;
      }
    } catch {
      counters.failed++;
    }
    await sleep(config.userThinkMs);
  }
}

class APIClient {
  constructor(baseURL, label = "client") {
    this.baseURL = baseURL;
    this.cookie = "";
    this.csrf = "";
    this.label = label;
    this.agent = new https.Agent({
      keepAlive: true,
      maxSockets: 2,
      rejectUnauthorized: false
    });
  }

  get(pathname, expected = 200) {
    return this.request("GET", pathname, null, expected);
  }

  post(pathname, body, expected = 200) {
    return this.request("POST", pathname, body, expected);
  }

  async request(method, pathname, body, expected) {
    const url = new URL(pathname, this.baseURL);
    const payload = body == null ? null : JSON.stringify(body);
    const headers = {
      "Accept": "application/json"
    };
    if (payload != null) {
      headers["Content-Type"] = "application/json";
      headers["Content-Length"] = Buffer.byteLength(payload);
    }
    if (this.cookie) headers["Cookie"] = this.cookie;
    if (isUnsafe(method)) {
      headers["Origin"] = this.baseURL;
      if (this.csrf) headers["X-Pappice-CSRF"] = this.csrf;
    }

    const { statusCode, headers: responseHeaders, text } = await httpsRequest(url, {
      agent: this.agent,
      headers,
      method
    }, payload);
    this.captureCookies(responseHeaders["set-cookie"]);
    const data = text ? JSON.parse(text) : {};
    if (data.csrf_token) this.csrf = data.csrf_token;
    if (statusCode !== expected) {
      throw new Error(`${this.label} ${method} ${pathname} returned ${statusCode}: ${text}`);
    }
    return data;
  }

  captureCookies(setCookies) {
    if (!setCookies?.length) return;
    const cookies = setCookies.map((cookie) => cookie.split(";")[0]);
    this.cookie = cookies.join("; ");
  }

  close() {
    this.agent.destroy();
  }
}

function httpsRequest(url, options, payload) {
  return new Promise((resolvePromise, reject) => {
    const request = https.request(url, options, (response) => {
      const chunks = [];
      response.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
      response.on("end", () => {
        resolvePromise({
          headers: response.headers,
          statusCode: response.statusCode || 0,
          text: Buffer.concat(chunks).toString("utf8")
        });
      });
    });
    request.on("error", reject);
    if (payload != null) request.write(payload);
    request.end();
  });
}

function startApp({ appURL, appPort, backupDir, binaryPath, certPath, dbPath, keyPath, uploadDir, tempDir }) {
  const child = spawn(binaryPath, [
    "serve",
    "-addr", `127.0.0.1:${appPort}`,
    "-backup-dir", backupDir,
    "-db", dbPath,
    "-email-notifications=false",
    "-login-rate-limit", "1000",
    "-public-url", appURL,
    "-tls-cert", certPath,
    "-tls-key", keyPath,
    "-upload-dir", uploadDir
  ], {
    cwd: tempDir,
    env: {
      HOME: process.env.HOME || tempDir,
      PATH: process.env.PATH || "",
      TMPDIR: process.env.TMPDIR || os.tmpdir(),
      PAPPICE_EMAIL_NOTIFICATIONS: "false"
    },
    stdio: ["ignore", "pipe", "pipe"]
  });
  const output = [];
  child.stdout.on("data", (chunk) => output.push(Buffer.from(chunk)));
  child.stderr.on("data", (chunk) => output.push(Buffer.from(chunk)));
  child.outputText = () => Buffer.concat(output).toString("utf8");
  return child;
}

async function waitForHTTPS(url, child, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`app exited before becoming ready\n${child.outputText()}`);
    }
    try {
      const response = await httpsRequest(new URL(url), {
        agent: new https.Agent({ rejectUnauthorized: false }),
        method: "GET"
      });
      if (response.statusCode >= 200 && response.statusCode < 500) return;
    } catch (error) {
      lastError = error;
    }
    await sleep(150);
  }
  throw new Error(`app did not become ready: ${lastError?.message || "timeout"}\n${child.outputText()}`);
}

async function generateCertificate(certPath, keyPath) {
  await runCommand("openssl", [
    "req",
    "-x509",
    "-newkey", "rsa:2048",
    "-nodes",
    "-keyout", keyPath,
    "-out", certPath,
    "-days", "1",
    "-subj", "/CN=127.0.0.1",
    "-addext", "subjectAltName=IP:127.0.0.1,DNS:localhost"
  ], { cwd: repoRoot });
}

async function runCommand(command, args, options = {}) {
  const child = spawn(command, args, {
    ...options,
    stdio: ["ignore", "pipe", "pipe"]
  });
  const output = [];
  child.stdout.on("data", (chunk) => output.push(Buffer.from(chunk)));
  child.stderr.on("data", (chunk) => output.push(Buffer.from(chunk)));
  const code = await new Promise((resolvePromise, reject) => {
    child.on("error", reject);
    child.on("exit", resolvePromise);
  });
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}\n${Buffer.concat(output).toString("utf8")}`);
  }
}

async function freePort() {
  const server = net.createServer();
  await new Promise((resolvePromise, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolvePromise);
  });
  const port = server.address().port;
  await new Promise((resolvePromise) => server.close(resolvePromise));
  return port;
}

async function readRSS(pid) {
  const status = await readFile(`/proc/${pid}/status`, "utf8");
  const match = status.match(/^VmRSS:\s+(\d+)\s+kB$/m);
  if (!match) throw new Error(`VmRSS not found for pid ${pid}`);
  return Number(match[1]) * 1024;
}

function summarize(values) {
  if (values.length === 0) {
    return { max: 0, mean: 0, min: 0, p50: 0, p95: 0 };
  }
  const sorted = [...values].sort((a, b) => a - b);
  const sum = sorted.reduce((total, value) => total + value, 0);
  return {
    max: sorted[sorted.length - 1],
    mean: Math.round(sum / sorted.length),
    min: sorted[0],
    p50: percentile(sorted, 0.50),
    p95: percentile(sorted, 0.95)
  };
}

function percentile(sorted, quantile) {
  const index = Math.min(sorted.length - 1, Math.max(0, Math.ceil(sorted.length * quantile) - 1));
  return sorted[index];
}

async function currentVersion() {
  try {
    return (await readFile(path.join(repoRoot, "VERSION"), "utf8")).trim();
  } catch {
    return "unknown";
  }
}

async function stopProcess(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  const stopped = await waitForExit(child, 2500);
  if (!stopped) {
    child.kill("SIGKILL");
    await waitForExit(child, 2500);
  }
}

function waitForExit(child, timeoutMs) {
  return new Promise((resolvePromise) => {
    const timer = setTimeout(() => resolvePromise(false), timeoutMs);
    child.once("exit", () => {
      clearTimeout(timer);
      resolvePromise(true);
    });
  });
}

function printResult(result) {
  console.log("Pappice small environment memory benchmark\n");
  console.log(`version:    ${result.version}`);
  console.log(`duration:   ${(result.scenario.duration_ms / 1000).toFixed(1)}s`);
  console.log(`scenario:   ${result.scenario.products} products, ${result.scenario.staff} staff, ${result.scenario.customers} customers, ${result.scenario.tickets} tickets`);
  console.log(`requests:   ${result.requests.ok} ok, ${result.requests.failed} failed, ${result.requests.per_second}/s`);
  console.log(`samples:    ${result.samples}`);
  console.log("");
  console.log("RSS");
  console.log(`startup:    ${formatMiB(result.rss_bytes.startup)}`);
  console.log(`seeded:     ${formatMiB(result.rss_bytes.seeded)}`);
  console.log(`min:        ${formatMiB(result.rss_bytes.min)}`);
  console.log(`mean:       ${formatMiB(result.rss_bytes.mean)}`);
  console.log(`p50:        ${formatMiB(result.rss_bytes.p50)}`);
  console.log(`p95:        ${formatMiB(result.rss_bytes.p95)}`);
  console.log(`max:        ${formatMiB(result.rss_bytes.max)}`);
}

function isUnsafe(method) {
  return method === "POST" || method === "PATCH" || method === "PUT" || method === "DELETE";
}

function parseDuration(value, label) {
  const match = String(value).trim().match(/^(\d+)(ms|s|m)?$/);
  if (!match) throw new Error(`invalid ${label}: ${value}`);
  const amount = Number(match[1]);
  const unit = match[2] || "ms";
  if (amount < 1) throw new Error(`${label} must be positive`);
  if (unit === "ms") return amount;
  if (unit === "s") return amount * 1000;
  return amount * 60_000;
}

function positiveInt(value, label) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 1) {
    throw new Error(`${label} must be a positive integer`);
  }
  return parsed;
}

function formatMiB(bytes) {
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`;
}

function sleep(ms) {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, ms));
}

main().catch((error) => {
  console.error(error.stack || error.message);
  process.exitCode = 1;
});
