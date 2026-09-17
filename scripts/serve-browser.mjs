#!/usr/bin/env node
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

function acceptsGzip(header = "") {
  const encodings = header.toLowerCase().split(",").map((value) => value.trim().split(/\s*;\s*/));
  const encoding = encodings.find(([name]) => name === "gzip") || encodings.find(([name]) => name === "*");
  return Boolean(encoding) && Number(encoding.find((value) => value.startsWith("q="))?.slice(2) ?? 1) > 0;
}

// A static file server: there is deliberately no API or database here.
export async function serveBrowser(directory, { port = 8389, onRequest = () => {} } = {}) {
  const root = path.resolve(directory);
  const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".wasm": "application/wasm" };
  const server = createServer(async (req, res) => {
    onRequest(req.url);
    try {
      if (!["GET", "HEAD"].includes(req.method)) {
        res.writeHead(405).end();
        return;
      }
      const pathname = decodeURIComponent(new URL(req.url, "http://localhost").pathname);
      const filename = path.resolve(root, `.${pathname.endsWith("/") ? pathname + "index.html" : pathname}`);
      if (!filename.startsWith(root + path.sep)) {
        res.writeHead(404).end();
        return;
      }
      let gzip = filename.endsWith(".wasm") && acceptsGzip(req.headers["accept-encoding"]);
      let content;
      if (gzip) {
        try {
          content = await readFile(filename + ".gz");
        } catch (error) {
          if (error.code !== "ENOENT") throw error;
          gzip = false;
        }
      }
      content ??= await readFile(filename);
      res.setHeader("Content-Type", types[path.extname(filename)] || "application/octet-stream");
      res.setHeader("Cache-Control", "no-cache");
      if (filename.endsWith(".wasm")) res.setHeader("Vary", "Accept-Encoding");
      if (gzip) res.setHeader("Content-Encoding", "gzip");
      res.writeHead(200).end(req.method === "HEAD" ? undefined : content);
    } catch {
      res.writeHead(404).end();
    }
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, "127.0.0.1", resolve);
  });
  return server;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const server = await serveBrowser(process.argv[2] || "dist/browser");
  console.log(`Browser demo: http://127.0.0.1:${server.address().port}`);
}
