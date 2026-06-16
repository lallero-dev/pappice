import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import https from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const toolDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(toolDir, "../..");
const defaultChromiumPath = process.env.PAPPICE_E2E_CHROMIUM || process.env.CHROMIUM || "/usr/bin/chromium";

async function startLocalPappice(options = {}) {
  const tempPrefix = options.tempPrefix || "pappice-local-";
  const tempDir = await mkdtemp(path.join(tmpdir(), tempPrefix));
  const certPath = path.join(tempDir, "localhost.pem");
  const keyPath = path.join(tempDir, "localhost-key.pem");
  const dbPath = path.join(tempDir, "pappice.db");
  const binaryPath = path.join(tempDir, "pappice");
  const state = {
    appProcess: null,
    chromeProcess: null,
    page: null,
    smtpServer: null,
    tempDir
  };

  try {
    await buildApp(binaryPath);
    await generateCertificate(certPath, keyPath);
    state.smtpServer = await startFakeSMTP();

    const appPort = await freePort();
    const appURL = `https://127.0.0.1:${appPort}`;
    state.appProcess = startApp({
      appPort,
      appURL,
      binaryPath,
      certPath,
      dbPath,
      emailNotifications: options.emailNotifications ?? true,
      env: options.env || {},
      keyPath,
      notificationDelay: options.notificationDelay || "1h",
      sessionTTL: options.sessionTTL || "2h",
      smtpPort: state.smtpServer.port
    });
    await waitForHTTPS(`${appURL}/api/health`, state.appProcess);

    const chromePort = await freePort();
    state.chromeProcess = startChromium({
      appURL,
      chromiumPath: options.chromiumPath || defaultChromiumPath,
      port: chromePort,
      userDataDir: path.join(tempDir, "chrome"),
      viewport: options.viewport
    });
    state.page = await connectToPage(chromePort);
    await state.page.send("Page.enable");
    await state.page.send("Runtime.enable");

    return {
      appURL,
      page: state.page,
      tempDir,
      cleanup: () => cleanup(state, Boolean(options.keepTemp))
    };
  } catch (error) {
    await cleanup(state, Boolean(options.keepTemp));
    throw error;
  }
}

class CDPClient {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 1;
    this.pending = new Map();
    socket.addEventListener("message", (event) => this.handleMessage(event.data));
    socket.addEventListener("close", () => {
      for (const { reject, timer } of this.pending.values()) {
        clearTimeout(timer);
        reject(new Error("CDP websocket closed"));
      }
      this.pending.clear();
    });
  }

  send(method, params = {}) {
    const id = this.nextID++;
    const payload = JSON.stringify({ id, method, params });
    return new Promise((resolvePromise, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 30000);
      this.pending.set(id, { method, resolve: resolvePromise, reject, timer });
      this.socket.send(payload);
    });
  }

  async close() {
    if (this.socket.readyState === 3) return;
    await new Promise((resolvePromise) => {
      let settled = false;
      const settle = () => {
        if (settled) return;
        settled = true;
        resolvePromise();
      };
      this.socket.addEventListener("close", settle, { once: true });
      this.socket.close();
      setTimeout(settle, 1000).unref();
    });
  }

  handleMessage(raw) {
    const message = JSON.parse(String(raw));
    if (!message.id) return;
    const pending = this.pending.get(message.id);
    if (!pending) return;
    clearTimeout(pending.timer);
    this.pending.delete(message.id);
    if (message.error) {
      pending.reject(new Error(`CDP ${pending.method} failed: ${message.error.message}`));
      return;
    }
    pending.resolve(message.result || {});
  }
}

async function connectToPage(port) {
  const deadline = Date.now() + 12000;
  let target = null;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/list`);
      const targets = await response.json();
      target = targets.find((candidate) => candidate.type === "page" && candidate.webSocketDebuggerUrl);
      if (target) break;
    } catch {
      // Chromium is still starting.
    }
    await sleep(100);
  }
  if (!target) throw new Error("Chromium DevTools page target was not available");
  const socket = await connectWebSocket(target.webSocketDebuggerUrl);
  return new CDPClient(socket);
}

async function connectWebSocket(url) {
  return new Promise((resolvePromise, reject) => {
    const socket = new WebSocket(url);
    const timer = setTimeout(() => {
      socket.close();
      reject(new Error("Timed out connecting to Chromium DevTools websocket"));
    }, 10000);
    socket.addEventListener("open", () => {
      clearTimeout(timer);
      resolvePromise(socket);
    }, { once: true });
    socket.addEventListener("error", (event) => {
      clearTimeout(timer);
      reject(new Error(`DevTools websocket error: ${event.message || "unknown error"}`));
    }, { once: true });
  });
}

function startChromium({ appURL, chromiumPath, port, userDataDir, viewport }) {
  const args = [
    "--headless=new",
    `--remote-debugging-port=${port}`,
    "--remote-allow-origins=*",
    `--user-data-dir=${userDataDir}`,
    "--ignore-certificate-errors",
    "--no-first-run",
    "--disable-gpu",
    "--disable-dev-shm-usage",
    "--no-sandbox"
  ];
  if (viewport?.width && viewport?.height) {
    args.push(`--window-size=${viewport.width},${viewport.height}`);
  }
  args.push(appURL);
  return spawnProcess(chromiumPath, args, {
    cwd: repoRoot,
    label: "chromium"
  });
}

async function buildApp(binaryPath) {
  await runCommand("go", ["build", "-o", binaryPath, "./cmd/pappice"], { cwd: repoRoot });
}

function startApp({ appPort, appURL, binaryPath, certPath, dbPath, emailNotifications, env, keyPath, notificationDelay, sessionTTL, smtpPort }) {
  const appEnv = {
    ...process.env,
    ...env,
    PAPPICE_EMAIL_NOTIFICATIONS: emailNotifications ? "true" : "false",
    PAPPICE_PUBLIC_URL: appURL,
    PAPPICE_SMTP_FROM: "no-reply@example.test",
    PAPPICE_SMTP_HOST: "127.0.0.1",
    PAPPICE_SMTP_PASSWORD: "",
    PAPPICE_SMTP_PORT: String(smtpPort),
    PAPPICE_SMTP_TLS_MODE: "none",
    PAPPICE_SMTP_USER: ""
  };
  return spawnProcess(binaryPath, [
    "serve",
    "-addr", `127.0.0.1:${appPort}`,
    "-db", dbPath,
    "-tls-cert", certPath,
    "-tls-key", keyPath,
    "-public-url", appURL,
    "-smtp-host", "127.0.0.1",
    "-smtp-port", String(smtpPort),
    "-smtp-from", "no-reply@example.test",
    "-smtp-tls-mode", "none",
    "-notification-delay", notificationDelay,
    "-session-ttl", sessionTTL
  ], {
    cwd: repoRoot,
    env: appEnv,
    label: "pappice"
  });
}

function spawnProcess(command, args, { cwd, env = process.env, label }) {
  const child = spawn(command, args, {
    cwd,
    env,
    stdio: ["ignore", "pipe", "pipe"]
  });
  const output = [];
  child.stdout.on("data", (chunk) => captureOutput(output, label, chunk));
  child.stderr.on("data", (chunk) => captureOutput(output, label, chunk));
  child.outputText = () => Buffer.concat(output).toString("utf8");
  child.on("error", (error) => {
    output.push(Buffer.from(`${label}: ${error.message}\n`));
  });
  return child;
}

function captureOutput(output, label, chunk) {
  output.push(Buffer.from(chunk));
  if (process.env.PAPPICE_E2E_VERBOSE || process.env.PAPPICE_DEMO_VERBOSE) {
    const text = chunk.toString("utf8").replace(/\n$/, "");
    for (const line of text.split("\n")) {
      if (line) console.error(`[${label}] ${line}`);
    }
  }
}

async function waitForHTTPS(url, child, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = null;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`app exited before becoming ready\n${child.outputText()}`);
    }
    try {
      const status = await httpsStatus(url);
      if (status >= 200 && status < 500) return;
    } catch (error) {
      lastError = error;
    }
    await sleep(150);
  }
  throw new Error(`app did not become ready: ${lastError?.message || "timeout"}\n${child.outputText()}`);
}

async function httpsStatus(url) {
  return new Promise((resolvePromise, reject) => {
    const request = https.get(url, { rejectUnauthorized: false, timeout: 1500 }, (response) => {
      response.resume();
      response.on("end", () => resolvePromise(response.statusCode || 0));
    });
    request.on("timeout", () => {
      request.destroy(new Error("request timed out"));
    });
    request.on("error", reject);
  });
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
    child.on("exit", (exitCode) => resolvePromise(exitCode));
  });
  if (code !== 0) {
    throw new Error(`${command} exited with ${code}\n${Buffer.concat(output).toString("utf8")}`);
  }
}

async function startFakeSMTP() {
  const messages = [];
  const sockets = new Set();
  const server = net.createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.setEncoding("utf8");
    socket.write("220 pappice-local\r\n");
    let buffer = "";
    let dataMode = false;
    let dataLines = [];
    let message = { from: "", to: [], data: "" };

    socket.on("data", (chunk) => {
      buffer += chunk;
      while (buffer.includes("\n")) {
        const index = buffer.indexOf("\n");
        const line = buffer.slice(0, index).replace(/\r$/, "");
        buffer = buffer.slice(index + 1);
        if (dataMode) {
          if (line === ".") {
            message.data = dataLines.join("\r\n");
            messages.push(message);
            message = { from: "", to: [], data: "" };
            dataLines = [];
            dataMode = false;
            socket.write("250 OK\r\n");
          } else {
            dataLines.push(line);
          }
          continue;
        }

        const upper = line.toUpperCase();
        if (upper.startsWith("EHLO") || upper.startsWith("HELO")) {
          socket.write("250-localhost\r\n250 OK\r\n");
        } else if (upper.startsWith("AUTH")) {
          socket.write("235 Authentication successful\r\n");
        } else if (upper.startsWith("MAIL FROM:")) {
          message.from = line.slice("MAIL FROM:".length).trim();
          socket.write("250 OK\r\n");
        } else if (upper.startsWith("RCPT TO:")) {
          message.to.push(line.slice("RCPT TO:".length).trim());
          socket.write("250 OK\r\n");
        } else if (upper === "DATA") {
          dataMode = true;
          socket.write("354 End data with <CR><LF>.<CR><LF>\r\n");
        } else if (upper === "RSET") {
          message = { from: "", to: [], data: "" };
          dataLines = [];
          dataMode = false;
          socket.write("250 OK\r\n");
        } else if (upper === "NOOP") {
          socket.write("250 OK\r\n");
        } else if (upper === "QUIT") {
          socket.write("221 Bye\r\n");
          socket.end();
        } else {
          socket.write("250 OK\r\n");
        }
      }
    });
  });
  await new Promise((resolvePromise, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      server.off("error", reject);
      resolvePromise();
    });
  });
  return {
    messages,
    port: server.address().port,
    close: () => new Promise((resolvePromise) => {
      let settled = false;
      const settle = () => {
        if (settled) return;
        settled = true;
        resolvePromise();
      };
      for (const socket of sockets) socket.destroy();
      server.close(settle);
      setTimeout(settle, 1000).unref();
    })
  };
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

async function cleanup(state, keepTemp) {
  if (state.page) {
    try {
      await state.page.close();
    } catch {
      // Ignore cleanup failures.
    }
    state.page = null;
  }
  await stopProcess(state.chromeProcess);
  state.chromeProcess = null;
  await stopProcess(state.appProcess);
  state.appProcess = null;
  if (state.smtpServer) {
    try {
      await state.smtpServer.close();
    } catch {
      // Ignore cleanup failures.
    }
    state.smtpServer = null;
  }
  if (state.tempDir && !keepTemp) {
    await rm(state.tempDir, { force: true, recursive: true });
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

async function waitForExit(child, timeoutMs) {
  return new Promise((resolvePromise) => {
    const timer = setTimeout(() => resolvePromise(false), timeoutMs);
    child.once("exit", () => {
      clearTimeout(timer);
      resolvePromise(true);
    });
  });
}

function sleep(ms) {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, ms));
}

export { repoRoot, runCommand, startLocalPappice };
