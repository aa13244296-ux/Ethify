const enabled = document.querySelector("#enabled");
const status = document.querySelector("#status");

chrome.storage.local.get({ enabled: true, lastResult: "" }).then((settings) => {
  enabled.checked = settings.enabled;
  if (settings.lastResult) status.textContent = settings.lastResult;
});

enabled.addEventListener("change", async () => {
  await chrome.storage.local.set({ enabled: enabled.checked });
  status.textContent = enabled.checked
    ? "Ethify will capture supported downloads."
    : "Browser download capture is off.";
});
