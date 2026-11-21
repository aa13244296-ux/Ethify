const ETHIFI_URL = "http://127.0.0.1:18888";

async function setResult(message, failed = false) {
  await chrome.storage.local.set({ lastResult: message });
  await chrome.action.setBadgeText({ text: failed ? "!" : "" });
  if (failed) {
    await chrome.action.setBadgeBackgroundColor({ color: "#b94c43" });
  }
}

async function captureDownload(item) {
  if (!item.url || !/^https?:\/\//i.test(item.url) || item.state !== "in_progress") return;

  const { enabled } = await chrome.storage.local.get({ enabled: true });
  if (!enabled) return;

  try {
    const networksResponse = await fetch(`${ETHIFI_URL}/api/download-networks`);
    if (!networksResponse.ok) throw new Error("Ethify is not available");
    const networks = await networksResponse.json();
    const interfaceIndices = networks.map((network) => network.interfaceIndex);
    if (interfaceIndices.length < 2) throw new Error("Connect at least two active adapters");

    const isTorrent = /\.torrent$/i.test(new URL(item.url).pathname) || /\.torrent$/i.test(item.filename || "");
    const response = await fetch(`${ETHIFI_URL}/api/${isTorrent ? "torrents" : "downloads"}`, {
      method: "POST",
      headers: {
        "Content-Type": "text/plain",
      },
      body: JSON.stringify(isTorrent
        ? { source: item.url, interfaceIndices }
        : { url: item.url, interfaceIndices }),
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "Ethify could not capture this download");

    if (!isTorrent) {
      const task = await waitForEthifyProgress(result.id);
      if (!task) {
        await cancelEthifyDownload(result.id);
        throw new Error("Ethify could not start the transfer");
      }
    }

    const [current] = await chrome.downloads.search({ id: item.id });
    if (current?.state === "in_progress") await chrome.downloads.cancel(item.id);
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

chrome.downloads.onCreated.addListener((item) => {
  void captureDownload(item);
});
