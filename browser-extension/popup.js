const enabled = document.querySelector("#enabled");
const extensions = document.querySelector("#extensions");
const save = document.querySelector("#save");
const status = document.querySelector("#status");

chrome.storage.local.get({
  enabled: true,
  extensions: "7z,avi,csv,doc,docx,dmg,exe,flac,gz,m4a,mkv,mov,mp3,mp4,msi,pdf,ppt,pptx,rar,tar,torrent,wav,webm,xls,xlsx,zip",
  lastResult: "",
}).then((settings) => {
  enabled.checked = settings.enabled;
  extensions.value = settings.extensions;
  if (settings.lastResult) status.textContent = settings.lastResult;
});

enabled.addEventListener("change", async () => {
  await chrome.storage.local.set({ enabled: enabled.checked });
  status.textContent = enabled.checked
    ? "Ethify will capture supported downloads."
    : "Browser download capture is off.";
});

save.addEventListener("click", async () => {
  const allowed = extensions.value
    .split(/[,\s]+/)
    .map((extension) => extension.replace(/^\./, "").toLowerCase())
    .filter((extension) => /^[a-z0-9]{1,12}$/.test(extension));
  if (allowed.length === 0) {
    status.textContent = "Enter at least one valid extension, such as zip, pdf, or mp4.";
    return;
  }
  const value = [...new Set(allowed)].join(",");
  await chrome.storage.local.set({ extensions: value });
  extensions.value = value;
  status.textContent = "Extension filter saved.";
});
