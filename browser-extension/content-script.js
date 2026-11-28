(() => {
  const mediaExtensions = /\.(?:m3u8|mp3|mp4)(?:$|[?#])/i;
  const detected = new Set();
  let pill;

  function reportAltState(pressed) {
    void chrome.runtime.sendMessage({ type: "ethify-alt-state", pressed });
  }

  document.addEventListener("keydown", (event) => {
    if (event.key === "Alt" || event.altKey) reportAltState(true);
  }, true);
  document.addEventListener("keyup", (event) => {
    if (event.key === "Alt" || !event.altKey) reportAltState(false);
  }, true);
  window.addEventListener("blur", () => reportAltState(false));
  document.addEventListener("click", (event) => {
    if (event.altKey || !(event.target instanceof Element)) return;
    const link = event.target.closest("a[href]");
    if (!link?.href.startsWith("magnet:")) return;
    event.preventDefault();
    event.stopPropagation();
    void chrome.runtime.sendMessage({
      type: "ethify-magnet",
      url: link.href,
      referer: location.href,
    });
  }, true);

  function addCandidate(rawURL) {
    if (typeof rawURL !== "string" || !mediaExtensions.test(rawURL)) return;
    let url;
    try {
      url = new URL(rawURL, location.href);
    } catch {
      return;
    }
    if (!["http:", "https:"].includes(url.protocol) || detected.has(url.href)) return;
    detected.add(url.href);
    if (!pill) createPill();
    pill.dataset.url = url.href;
    pill.hidden = false;
  }

  function createPill() {
    const host = document.createElement("div");
    host.style.cssText = "position:fixed;right:18px;bottom:18px;z-index:2147483647";
    const shadow = host.attachShadow({ mode: "closed" });
    const style = document.createElement("style");
    style.textContent = `
      button {
        display:flex; align-items:center; gap:9px; border:1px solid #2d665c;
        border-radius:999px; padding:11px 15px; color:#e7fff5;
        background:#10251f; box-shadow:0 10px 28px #0008;
        font:600 13px "Segoe UI",sans-serif; cursor:pointer;
      }
      button:hover { background:#17352c; }
      button:focus-visible { outline:3px solid #45d6a8; outline-offset:3px; }
      span { color:#6de0b0; font-size:16px; }
    `;
    const button = document.createElement("button");
    button.type = "button";
    button.innerHTML = "<span aria-hidden='true'>↓</span> Download with Ethify";
    button.addEventListener("click", () => {
      if (!host.dataset.url) return;
      void chrome.runtime.sendMessage({
        type: "ethify-media-download",
        url: host.dataset.url,
        referer: location.href,
      });
    });
    shadow.append(style, button);
    host.hidden = true;
    host.dataset.url = "";
    document.documentElement.append(host);
    pill = host;
  }

  function inspectMedia() {
    for (const media of document.querySelectorAll("video[src], audio[src], video source[src], audio source[src]")) {
      addCandidate(media.currentSrc || media.src);
    }
    for (const entry of performance.getEntriesByType("resource")) addCandidate(entry.name);
  }

  document.addEventListener("DOMContentLoaded", inspectMedia, { once: true });
  inspectMedia();
  const observer = new MutationObserver(inspectMedia);
  observer.observe(document.documentElement, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["src"],
  });
  window.addEventListener("beforeunload", () => observer.disconnect(), { once: true });
})();
