const ETHIFI_URL = "http://127.0.0.1:18889";
const DEFAULT_EXTENSIONS = ["7z", "avi", "csv", "doc", "docx", "dmg", "exe", "flac", "gz", "m4a", "mkv", "mov", "mp3", "mp4", "msi", "pdf", "ppt", "pptx", "rar", "tar", "torrent", "wav", "webm", "xls", "xlsx", "zip"];
const altPressedTabs = new Set();

async function setResult(message, failed = false) {
  await chrome.storage.local.set({ lastResult: message });
  await chrome.action.setBadgeText({ text: failed ? "!" : "" });
  if (failed) await chrome.action.setBadgeBackgroundColor({ color: "#b94c43" });
}

function extensionOf(value) {
  const name = value.split(/[?#]/, 1)[0].split(/[\\/]/).pop() || "";
  const index = name.lastIndexOf(".");
  return index < 0 ? "" : name.slice(index + 1).toLowerCase();
}

async function captureEnabledFor(value) {
  const settings = await chrome.storage.local.get({
    enabled: true,
    extensions: DEFAULT_EXTENSIONS.join(","),
  });
  if (!settings.enabled) return false;
  const allowed = settings.extensions
    .split(/[,\s]+/)
    .map((extension) => extension.replace(/^\./, "").toLowerCase())
    .filter(Boolean);
  return allowed.includes(extensionOf(value));
}

async function ensureEthify() {
  try {
    const response = await fetch(`${ETHIFI_URL}/api/download-networks`);
    if (response.ok) return;
  } catch {
    // The native host is the only supported way for the extension to start Ethify.
  }

  let hostResult;
  try {
    hostResult = await chrome.runtime.sendNativeMessage("com.ethify.native_host", {
      action: "ensure-running",
    });
  } catch {
    throw new Error("Ethify is not running and its Native Messaging host is unavailable");
  }
  if (!hostResult?.ok) {
    throw new Error(hostResult?.error || "Could not start Ethify");
  }
  for (let attempt = 0; attempt < 30; attempt++) {
    try {
      const response = await fetch(`${ETHIFI_URL}/api/download-networks`);
      if (response.ok) return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }
  throw new Error("Ethify did not become ready");
}

async function browserHeaders(url, referer = "") {
  const cookies = await chrome.cookies.getAll({ url });
  const cookie = cookies.map(({ name, value }) => `${name}=${value}`).join("; ");
  return {
    cookie,
    "user-agent": navigator.userAgent,
    referer,
  };
}

async function captureDownload(item) {
  if (!item.url || !/^https?:\/\//i.test(item.url) || item.state !== "in_progress") return;
  if (altPressedTabs.has(item.tabId) || !(await captureEnabledFor(item.filename || item.url))) return;
  await forwardToEthify(item.url, item.referrer, item);
}

async function forwardToEthify(url, referer = "", browserItem) {
  try {
    await ensureEthify();
    const networksResponse = await fetch(`${ETHIFI_URL}/api/download-networks`);
    if (!networksResponse.ok) throw new Error("Ethify could not read active adapters");
    const networks = await networksResponse.json();
    const interfaceIndices = networks.map((network) => network.interfaceIndex);
    if (interfaceIndices.length < 1) throw new Error("Connect at least one active adapter");

    const isTorrent = /\.torrent(?:$|[?#])/i.test(url) ||
      /\.torrent$/i.test(browserItem?.filename || "");
    const headers = await browserHeaders(url, referer);
    const response = await fetch(`${ETHIFI_URL}/api/${isTorrent ? "torrents" : "downloads"}`, {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: JSON.stringify(isTorrent
        ? { source: url, interfaceIndices }
        : { url, interfaceIndices, headers }),
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "Ethify could not capture this download");

    if (!isTorrent) {
      const started = await waitForEthifyProgress(result.id);
      if (!started) {
        await cancelEthifyDownload(result.id);
        throw new Error("Ethify could not start the transfer");
      }
    }

    if (browserItem) {
      const [current] = await chrome.downloads.search({ id: browserItem.id });
      if (current?.state === "in_progress") await chrome.downloads.cancel(browserItem.id);
    }
    await setResult(`Captured ${result.name || (isTorrent ? "torrent" : "browser download")} in Ethify`);
  } catch (error) {
    const message = error instanceof Error ? error.message : "Could not capture browser download";
    await setResult(`${message}; browser download continues`, true);
  }
}

async function waitForEthifyProgress(id) {
  for (let attempt = 0; attempt < 20; attempt++) {
    const response = await fetch(`${ETHIFI_URL}/api/downloads`);
    if (!response.ok) return false;
    const downloads = await response.json();
    const task = downloads.find((download) => download.id === id);
    if (!task || task.state === "failed" || task.state === "cancelled") return false;
    if (task.downloaded > 0) return true;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  return false;
}

async function cancelEthifyDownload(id) {
  const response = await fetch(`${ETHIFI_URL}/api/downloads/cancel`, {
    method: "POST",
    headers: { "Content-Type": "text/plain" },
    body: JSON.stringify({ id }),
  });
  if (!response.ok && response.status !== 409) {
    throw new Error("Could not stop the temporary Ethify download");
  }
  for (let attempt = 0; attempt < 20; attempt++) {
    const downloadsResponse = await fetch(`${ETHIFI_URL}/api/downloads`);
    if (!downloadsResponse.ok) throw new Error("Could not read Ethify download status");
    const downloads = await downloadsResponse.json();
    const task = downloads.find((download) => download.id === id);
    if (!task || task.state === "completed" || task.state === "cancelled") return;
    if (task.state === "paused" || task.state === "failed") {
      const discardResponse = await fetch(`${ETHIFI_URL}/api/downloads/discard`, {
        method: "POST",
        headers: { "Content-Type": "text/plain" },
        body: JSON.stringify({ id }),
      });
      if (!discardResponse.ok) throw new Error("Could not discard the temporary Ethify download");
      return;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Ethify did not stop the temporary download");
}

chrome.runtime.onMessage.addListener((message, sender) => {
  if (message?.type === "ethify-alt-state" && Number.isInteger(sender.tab?.id)) {
    if (message.pressed) altPressedTabs.add(sender.tab.id);
    else altPressedTabs.delete(sender.tab.id);
  } else if (message?.type === "ethify-media-download" && typeof message.url === "string") {
    void forwardToEthify(message.url, message.referer || "");
  } else if (message?.type === "ethify-magnet" && typeof message.url === "string") {
    void forwardToEthify(message.url, message.referer || "");
  }
});

chrome.downloads.onCreated.addListener((item) => {
  void captureDownload(item);
});

chrome.tabs.onRemoved.addListener((tabId) => altPressedTabs.delete(tabId));
