#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { cp, mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const root = fileURLToPath(new URL("../", import.meta.url));
const browserRoot = path.join(root, "demo/browser");

export async function buildBrowser(destination = path.join(root, "dist/browser")) {
  const output = path.resolve(destination);
  await mkdir(output, { recursive: true });
  execFileSync("go", ["build", "-trimpath", "-ldflags=-s -w", "-o", path.join(output, "pappice.wasm"), "./cmd"], {
    cwd: browserRoot,
    env: { ...process.env, GOOS: "js", GOARCH: "wasm", CGO_ENABLED: "0" },
    stdio: "inherit"
  });
  const goroot = execFileSync("go", ["env", "GOROOT"], { cwd: browserRoot, encoding: "utf8" }).trim();
  await cp(path.join(goroot, "lib/wasm/wasm_exec.js"), path.join(output, "wasm_exec.js"));
  await cp(path.join(root, "internal/server/web/static"), path.join(output, "static"), { recursive: true });
  for (const file of ["boot.js", "platform.js", "transport.js", "worker.js", "browser.css"]) {
    await cp(path.join(browserRoot, file), path.join(output, file));
  }
  let html = await readFile(path.join(root, "internal/server/web/index.html"), "utf8");
  html = html.replaceAll('"/static/', '"./static/')
    .replace('src="./static/app.js"', 'src="./boot.js"')
    .replace("</head>", `<link rel="stylesheet" href="./browser.css">
    <script type="importmap">{"imports":{"./static/platform.js":"./platform.js"}}</script>
  </head>`)
    .replace("<body>", `<body>
    <aside class="browser-demo" aria-label="Browser demo">
      <p id="browserStatus" role="status">Starting your private demo…</p>
      <details><summary>About this demo</summary><p>Each tab has its own instance. Reloading or closing it discards your data. All sections are available; file transfers and outgoing email/webhook deliveries are unsupported. Account links work only in this tab.</p></details>
      <button class="ghost-button" id="browserReset" type="button" disabled>Reset</button>
    </aside>
    <noscript>This demo requires JavaScript and WebAssembly.</noscript>`);
  await writeFile(path.join(output, "index.html"), html);
  await cp(path.join(root, "LICENSE"), path.join(output, "LICENSE"));
  let licenses = `Go\n${await readFile(path.join(root, "demo/browser/GO-LICENSE.txt"), "utf8")}\n`;
  for (const module of ["github.com/ncruces/go-sqlite3", "github.com/ncruces/go-sqlite3-wasm/v6", "github.com/ncruces/julianday", "golang.org/x/sys"]) {
    const directory = execFileSync("go", ["list", "-m", "-f", "{{.Dir}}", module], { cwd: browserRoot, encoding: "utf8" }).trim();
    licenses += `\n${module}\n${await readFile(path.join(directory, "LICENSE"), "utf8")}\n`;
  }
  await writeFile(path.join(output, "THIRD_PARTY_LICENSES.txt"), licenses);
  const wasm = await readFile(path.join(output, "pappice.wasm"));
  const compressed = gzipSync(wasm, { level: 9 });
  await writeFile(path.join(output, "pappice.wasm.gz"), compressed);
  console.log(`Browser demo: ${output}\nWASM: ${(wasm.length / 1048576).toFixed(1)} MiB (${(compressed.length / 1048576).toFixed(1)} MiB gzip)`);
  return output;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await buildBrowser(process.argv[2]);
}
