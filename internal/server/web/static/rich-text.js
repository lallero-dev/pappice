const URL_PATTERN = /\b((?:https?:\/\/|www\.)[^\s<>"']+)/gi;
const SIMPLE_TRAILING_PUNCTUATION = ".,!?;:";
const BALANCED_TRAILING = {
  ")": "(",
  "]": "[",
  "}": "{"
};

export function richTextNodes(value) {
  return tokenizeRichText(value).map((token) => {
    if (token.type !== "link") return document.createTextNode(token.text);
    const anchor = document.createElement("a");
    anchor.className = "message-link";
    anchor.href = token.href;
    anchor.target = "_blank";
    anchor.rel = "noopener noreferrer";
    anchor.textContent = token.text;
    return anchor;
  });
}

export function tokenizeRichText(value) {
  const text = String(value || "");
  const tokens = [];
  let cursor = 0;
  for (const match of text.matchAll(URL_PATTERN)) {
    const raw = match[0];
    const start = match.index || 0;
    const url = trimURL(raw);
    const href = safeHref(url.link);
    if (!href || url.link.length === 0) continue;
    if (start > cursor) tokens.push(textToken(text.slice(cursor, start)));
    tokens.push({ type: "link", text: url.link, href });
    if (url.trailing) tokens.push(textToken(url.trailing));
    cursor = start + raw.length;
  }
  if (cursor < text.length) tokens.push(textToken(text.slice(cursor)));
  return tokens;
}

function textToken(text) {
  return { type: "text", text };
}

function safeHref(value) {
  const candidate = value.startsWith("www.") ? `https://${value}` : value;
  try {
    const url = new URL(candidate);
    if (url.protocol !== "http:" && url.protocol !== "https:") return "";
    return url.href;
  } catch {
    return "";
  }
}

function trimURL(value) {
  let link = value;
  let trailing = "";
  while (link.length > 0) {
    const char = link[link.length - 1];
    if (SIMPLE_TRAILING_PUNCTUATION.includes(char) || isUnbalancedClosing(link, char)) {
      trailing = char + trailing;
      link = link.slice(0, -1);
      continue;
    }
    break;
  }
  return { link, trailing };
}

function isUnbalancedClosing(value, char) {
  const opener = BALANCED_TRAILING[char];
  if (!opener) return false;
  return countChar(value, char) > countChar(value, opener);
}

function countChar(value, char) {
  let count = 0;
  for (const item of value) {
    if (item === char) count += 1;
  }
  return count;
}
