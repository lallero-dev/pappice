importScripts("./wasm_exec.js");

// No upload or backup files exist in this in-memory instance. Let the regular
// maintenance handler inspect that empty namespace instead of failing ENOSYS.
for (const method of ["stat", "lstat", "readdir"]) {
  const fallback = self.fs[method];
  self.fs[method] = (path, callback) => {
    if (String(path).startsWith("/browser/")) {
      callback(Object.assign(new Error("No such file or directory"), { code: "ENOENT" }));
    } else {
      fallback(path, callback);
    }
  };
}

self.onmessage = async ({ data }) => {
  if (data.init) {
    self.pappicePageURL = data.pageURL;
    try {
      const go = new Go();
      const response = await fetch("./pappice.wasm");
      if (!response.ok) throw new Error(`Could not load Pappice (${response.status}).`);
      // Streaming requires application/wasm; arrayBuffer also works on plain hosts.
      const result = response.headers.get("Content-Type")?.split(";")[0] === "application/wasm"
        ? await WebAssembly.instantiateStreaming(response, go.importObject)
        : await WebAssembly.instantiate(await response.arrayBuffer(), go.importObject);
      await go.run(result.instance);
      throw new Error("The demo stopped. Reload to start again.");
    } catch (error) {
      self.postMessage({ error: String(error?.message || error) });
    }
    return;
  }
  try {
    self.pappiceRequest(data.id, JSON.stringify(data.request));
  } catch (error) {
    self.postMessage({ id: data.id, error: String(error?.message || error) });
  }
};
