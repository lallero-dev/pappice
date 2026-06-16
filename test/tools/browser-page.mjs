function pageTools() {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const waitFor = async (predicate, description, timeoutMs = 8000) => {
    const started = Date.now();
    let lastError = null;
    while (Date.now() - started < timeoutMs) {
      try {
        const value = predicate();
        if (value) return value;
      } catch (error) {
        lastError = error;
      }
      await sleep(50);
    }
    const suffix = lastError ? ` (${lastError.message})` : "";
    throw new Error(`Timed out waiting for ${description}${suffix}`);
  };
  const setValue = (control, value) => {
    if (!control) throw new Error(`Missing form control for value ${value}`);
    control.focus();
    control.value = value;
    control.dispatchEvent(new Event("input", { bubbles: true }));
    control.dispatchEvent(new Event("change", { bubbles: true }));
  };
  const pasteFiles = (target, files) => {
    if (!target) throw new Error("Missing paste target");
    const transfer = new DataTransfer();
    for (const file of files) transfer.items.add(file);
    const event = new Event("paste", { bubbles: true, cancelable: true });
    Object.defineProperty(event, "clipboardData", { value: transfer });
    target.dispatchEvent(event);
  };
  const tinyGifFile = (name) => new File([new Uint8Array([
    0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00,
    0x01, 0x00, 0x80, 0x00, 0x00, 0x00, 0x00, 0x00,
    0xff, 0xff, 0xff, 0x2c, 0x00, 0x00, 0x00, 0x00,
    0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44,
    0x01, 0x00, 0x3b
  ])], name, { type: "image/gif" });
  const modalRoot = () => document.querySelector("#modalHost")?.shadowRoot || null;
  const modalRoots = () => [...document.querySelectorAll("pappice-modal")]
    .map((modal) => modal.shadowRoot)
    .filter(Boolean);
  const openModalRoot = (title = "") => modalRoots().find((root) => {
    const dialog = root.querySelector("dialog[open]");
    const heading = root.querySelector("h2")?.textContent || "";
    return dialog && (!title || heading.includes(title));
  }) || null;
  const submitModal = async (title) => {
    const root = await waitFor(() => {
      const candidate = modalRoot();
      const dialog = candidate?.querySelector("dialog[open]");
      const heading = candidate?.querySelector("h2")?.textContent || "";
      return dialog && (!title || heading.includes(title)) ? candidate : null;
    }, title ? `${title} modal` : "modal");
    root.querySelector("footer .primary").click();
    await waitFor(() => !modalRoot()?.querySelector("dialog[open]"), "modal closed", 12000);
    return true;
  };
  const isScrolledToBottom = (element) => {
    if (!element) return false;
    return Math.abs(element.scrollHeight - element.clientHeight - element.scrollTop) <= 2;
  };
  return { isScrolledToBottom, modalRoot, openModalRoot, pasteFiles, setValue, submitModal, tinyGifFile, waitFor };
}

async function runInPage(cdp, fn, ...args) {
  const expression = `(() => {
    const pageTools = ${pageTools.toString()};
    return (${fn.toString()})(...${JSON.stringify(args)});
  })()`;
  const response = await cdp.send("Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
    userGesture: true
  });
  if (response.exceptionDetails) {
    throw new Error(formatException(response.exceptionDetails));
  }
  return response.result?.value;
}

function formatException(details) {
  const lines = [details.exception?.description || details.text || "browser evaluation failed"];
  for (const frame of details.stackTrace?.callFrames || []) {
    lines.push(`  at ${frame.functionName || "<anonymous>"}:${frame.lineNumber + 1}:${frame.columnNumber + 1}`);
  }
  return lines.join("\n");
}

async function waitForDocumentReady(cdp) {
  await runInPage(cdp, async () => {
    const { waitFor } = pageTools();
    await waitFor(() => document.readyState === "complete", "document ready", 12000);
    return true;
  });
}

export { runInPage, waitForDocumentReady };
