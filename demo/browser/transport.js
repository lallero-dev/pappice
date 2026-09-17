let reportFailure = () => {};
let worker;
const pending = new Map();
let nextID = 0;
let failure = null;
let resolveReady;
let rejectReady;
export const ready = new Promise((resolve, reject) => {
  resolveReady = resolve;
  rejectReady = reject;
});

function fail(message) {
  if (failure) return;
  failure = new Error(message);
  reportFailure(message);
  rejectReady(failure);
  for (const entry of pending.values()) entry.reject(failure);
  pending.clear();
  worker?.terminate();
}

function receive({ data }) {
  if (data.ready) return resolveReady();
  if (!data.id) return fail(data.error || "The demo stopped. Reload to retry.");
  const entry = pending.get(data.id);
  if (!entry) return;
  pending.delete(data.id);
  if (data.error) entry.reject(new Error(data.error));
  else entry.resolve(data.response);
}

export async function localFetch(path, options = {}) {
  await ready;
  if (failure) throw failure;
  const url = new URL(path, location.href);
  const request = new Request(url, options);
  const id = ++nextID;
  const input = {
    method: request.method,
    path: url.pathname + url.search,
    headers: Object.fromEntries(request.headers),
    // Uploads are rejected by the local handler; don't copy files into WASM.
    body: request.headers.get("Content-Type")?.toLowerCase().startsWith("multipart/") ? "" : await request.text()
  };
  if (failure) throw failure;
  const response = JSON.parse(await new Promise((resolve, reject) => {
    pending.set(id, { resolve, reject });
    try {
      worker.postMessage({ id, request: input });
    } catch (error) {
      pending.delete(id);
      reject(error);
    }
  }));
  return new Response([204, 205, 304].includes(response.status) ? null : response.body, {
    status: response.status,
    headers: response.headers
  });
}

export function startBackend(pageURL, onFailure) {
  if (worker || failure) return;
  reportFailure = onFailure;
  try {
    worker = new Worker(new URL("./worker.js", import.meta.url));
    worker.onerror = (event) => fail(event.message || "Could not start the demo. Reload to retry.");
    worker.onmessageerror = () => fail("Could not communicate with the demo. Reload to retry.");
    worker.onmessage = receive;
    worker.postMessage({ init: true, pageURL });
  } catch (error) {
    fail(String(error?.message || error));
  }
}
