import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";

type Link = {
  interfaceIndex: number;
  name: string;
  address: string;
  color: string;
  enabled: boolean;
  downloaded: number;
  speedBps: number;
};

type Network = {
  interfaceIndex: number;
  name: string;
  address: string;
};

type DownloadNetwork = Network & {
  downloaded: number;
  speedBps: number;
};

type Download = {
  id: string;
  name: string;
  state: string;
  location: string;
  size: number;
  downloaded: number;
  speedBps: number;
  progress: number;
  error?: string;
  canResume: boolean;
  networks: DownloadNetwork[];
};

type TorrentDownload = {
  id: string;
  name: string;
  state: string;
  location: string;
  size: number;
  downloaded: number;
  speedBps: number;
  uploadSpeedBps: number;
  progress: number;
  peers: number;
  error?: string;
  canResume: boolean;
  networks: string[];
};

type ProxyAdapter = Network & {
  activeConnections: number;
  bytesUp: number;
  bytesDown: number;
  upRateBps: number;
  downRateBps: number;
  failures: number;
  healthy: boolean;
  lastError?: string;
};

type LocalProxy = {
  running: boolean;
  socksAddress: string;
  httpAddress: string;
  adapters: ProxyAdapter[];
};

type ProxyLaunchResult = {
  started: boolean;
  processId: number;
  executable: string;
  preset?: string;
};

type SystemProxy = {
  supported: boolean;
  enabled: boolean;
  managed: boolean;
  address: string;
  elevated: boolean;
  lastError?: string;
  notice?: string;
};

type SystemAdapterTraffic = Network & {
  downRateBps: number;
  upRateBps: number;
};

type SystemTraffic = {
  supported: boolean;
  adapters: SystemAdapterTraffic[];
  processTrafficSupported?: boolean;
  processTrafficNote?: string;
  processes?: ProcessTraffic[];
};

type ProcessTraffic = {
  processId: number;
  name: string;
  interfaceIndex: number;
  interfaceName: string;
  connections: number;
  downloaded: number;
  uploaded: number;
  downRateBps: number;
  upRateBps: number;
};

type TrafficSample = {
  rates: Record<number, number>;
};

type RouterAdapterLoad = {
  interfaceIndex: number;
  activeFlows: number;
  bytesPerSecond: number;
};

type TunnelStatus = {
  state: string;
  active: boolean;
  fullTunnel: boolean;
  multipathActive: boolean;
  multipathPaths?: string[];
  relayAddr?: string;
  localRouter: {
    active: boolean;
    available: boolean;
    elevated: boolean;
    networks: Network[];
    adapterLoads?: RouterAdapterLoad[];
    adapterRateCaps?: Record<number, number>;
    activeEgress?: number;
    egressLimit?: number;
    tcpFlows?: number;
    udpFlows?: number;
    tcpDialAttempts?: number;
    udpDialAttempts?: number;
    tcpConnected?: number;
    udpConnected?: number;
    lastError?: string;
  };
};

const linkColors = ["#e9a65a", "#69aaa0", "#7d89be", "#c47c7c"];

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.method && init.method.toUpperCase() !== "GET") {
    headers.set("X-Ethify-Request", "1");
  }
  const response = await fetch(path, { ...init, headers });
  const result = await response.json().catch(() => ({} as { error?: string }));
  if (!response.ok) {
    throw new Error(result.error ?? `Request failed (${response.status})`);
  }
  return result as T;
}

function Icon({
  name,
  size = 18,
}: {
  name: "bolt" | "download" | "ethernet" | "link" | "pause" | "play" | "plus" | "settings" | "wifi";
  size?: number;
}) {
  const paths: Record<typeof name, ReactNode> = {
    bolt: <path d="M13 2 4 14h7l-1 8 10-12h-7l1-8Z" />,
    download: (
      <>
        <path d="M12 3v12" />
        <path d="m7 10 5 5 5-5" />
        <path d="M5 21h14" />
      </>
    ),
    ethernet: (
      <>
        <rect x="3" y="5" width="18" height="13" rx="2" />
        <path d="M8 18v3M16 18v3M8 9v3M12 9v3M16 9v3M6 21h12" />
      </>
    ),
    link: (
      <>
        <path d="M10 13a5 5 0 0 0 7.07 0l2-2A5 5 0 0 0 12 3.93l-1.14 1.14" />
        <path d="M14 11a5 5 0 0 0-7.07 0l-2 2A5 5 0 0 0 12 20.07l1.14-1.14" />
      </>
    ),
    pause: <path d="M8 5h3v14H8zM15 5h3v14h-3z" />,
    play: <path d="m7 4 13 8-13 8V4Z" />,
    plus: <path d="M12 5v14M5 12h14" />,
    settings: (
      <>
        <circle cx="12" cy="12" r="3" />
        <path d="m19.4 15 .1.1 1.4 1.1-1.4 2.4-1.7-.7a8 8 0 0 1-1.7 1l-.3 1.8h-2.8l-.3-1.8a8 8 0 0 1-1.7-1l-1.7.7-1.4-2.4L7.3 15a8 8 0 0 1 0-2l-1.4-1.1 1.4-2.4 1.7.7a8 8 0 0 1 1.7-1l.3-1.8h2.8l.3 1.8a8 8 0 0 1 1.7 1l1.7-.7 1.4 2.4-1.4 1.1a8 8 0 0 1-.1 2Z" />
      </>
    ),
    wifi: (
      <>
        <path d="M5 9.5a11 11 0 0 1 14 0" />
        <path d="M8 12.5a6.5 6.5 0 0 1 8 0" />
        <path d="M10.5 15.5a2.5 2.5 0 0 1 3 0" />
        <circle cx="12" cy="18.5" r=".75" fill="currentColor" />
      </>
    ),
  };

  return (
    <svg
      aria-hidden="true"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {paths[name]}
    </svg>
  );
}

function speed(bytesPerSecond: number) {
  if (bytesPerSecond >= 1024 * 1024) return `${(bytesPerSecond / (1024 * 1024)).toFixed(2)} MB/s`;
  return `${(bytesPerSecond / 1024).toFixed(1)} KB/s`;
}

function dashboardSpeed(bytesPerSecond: number) {
  return `${(bytesPerSecond / (1024 * 1024)).toFixed(1)} MB/s`;
}

function trafficPath(values: number[], max: number, closeArea = false) {
  if (values.length === 0) return "";
  const points = values.map((value, index) => {
    const x = values.length === 1 ? 0 : (index / (values.length - 1)) * 100;
    const y = 94 - (Math.min(value, max) / max) * 82;
    return `${index === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`;
  });
  if (closeArea) points.push("L100,100", "L0,100", "Z");
  return points.join(" ");
}

function proxyAdaptersMatch(selected: Link[], adapters: ProxyAdapter[]) {
  if (selected.length !== adapters.length) return false;
  const current = new Map(adapters.map((adapter) => [adapter.interfaceIndex, adapter]));
  return selected.every((link) => {
    const adapter = current.get(link.interfaceIndex);
    return adapter?.name === link.name && adapter.address === link.address;
  });
}

function fileSize(bytes: number) {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(2)} GB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

function estimatedTime(size: number, downloaded: number, bytesPerSecond: number) {
  if (size > 0 && downloaded >= size) return "Complete";
  if (bytesPerSecond <= 0 || size <= 0) return "—";
  let seconds = Math.ceil((size - downloaded) / bytesPerSecond);
  const hours = Math.floor(seconds / 3600);
  seconds %= 3600;
  const minutes = Math.floor(seconds / 60);
  seconds %= 60;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function parentFolder(path: string) {
  const cleanPath = path.replace(/[\\/]+$/, "");
  const separator = Math.max(cleanPath.lastIndexOf("/"), cleanPath.lastIndexOf("\\"));
  return separator > 0 ? cleanPath.slice(0, separator) : "";
}

function App() {
  const [links, setLinks] = useState<Link[]>([]);
  const [networksLoaded, setNetworksLoaded] = useState(false);
  const [networkRefreshing, setNetworkRefreshing] = useState(false);
  const [download, setDownload] = useState<Download>();
  const [downloadTasks, setDownloadTasks] = useState<Download[]>([]);
  const [torrentTasks, setTorrentTasks] = useState<TorrentDownload[]>([]);
  const [selectedTorrentId, setSelectedTorrentId] = useState("");
  const [selectedDownloadId, setSelectedDownloadId] = useState("");
  const [activeTab, setActiveTab] = useState<"system" | "downloads" | "torrents">("system");
  const [tunnel, setTunnel] = useState<TunnelStatus>({
    state: "disconnected",
    active: false,
    fullTunnel: false,
    multipathActive: false,
    localRouter: { active: false, available: false, elevated: false, networks: [] },
  });
  const [tunnelBusy, setTunnelBusy] = useState(false);
  const [rateCapDrafts, setRateCapDrafts] = useState<Record<number, string>>({});
  const [localProxy, setLocalProxy] = useState<LocalProxy>({
    running: false,
    socksAddress: "",
    httpAddress: "",
    adapters: [],
  });
  const [proxyBusy, setProxyBusy] = useState(false);
  const [proxyLaunchTarget, setProxyLaunchTarget] = useState("");
  const [proxyLaunchBusy, setProxyLaunchBusy] = useState(false);
  const [systemProxy, setSystemProxy] = useState<SystemProxy>({
    supported: false,
    enabled: false,
    managed: false,
    address: "127.0.0.1:8080",
    elevated: false,
  });
  const [systemTraffic, setSystemTraffic] = useState<SystemTraffic>({
    supported: false,
    adapters: [],
  });
  const [trafficHistory, setTrafficHistory] = useState<TrafficSample[]>([]);
  const [url, setUrl] = useState("");
  const [torrentSource, setTorrentSource] = useState("");
  const [movingTransfer, setMovingTransfer] = useState<{ kind: "downloads" | "torrents"; id: string }>();
  const [moveDestination, setMoveDestination] = useState("");
  const [notice, setNotice] = useState("");
  const networkRefreshInFlight = useRef(false);
  const networkRefreshMounted = useRef(false);
  const proxySyncInFlight = useRef(false);
  const proxySyncSignature = useRef("");
  const selectedNetworks = links.filter((link) => link.enabled);
  const isTorrentDownloading = torrentTasks.some((task) =>
    task.state === "downloading" || task.state === "metadata" || task.state === "cancelling" || task.state === "resuming",
  );
  const adapterProxySpeed = (index: number) => {
    const adapter = localProxy.adapters.find((item) => item.interfaceIndex === index);
    return (adapter?.upRateBps ?? 0) + (adapter?.downRateBps ?? 0);
  };
  const adapterDownloadSpeed = (index: number) =>
    download?.networks.find((item) => item.interfaceIndex === index)?.speedBps ?? 0;
  const adapterSpeed = (index: number) => adapterProxySpeed(index) + adapterDownloadSpeed(index);
  const totalSpeed = useMemo(
    () => (download?.speedBps ?? 0) + torrentTasks.reduce((total, task) => total + task.speedBps, 0) + localProxy.adapters.reduce(
      (total, adapter) => total + adapter.upRateBps + adapter.downRateBps,
      0,
    ),
    [download, torrentTasks, localProxy.adapters],
  );
  const systemDownloadSpeed = systemTraffic.adapters.reduce((total, adapter) => total + adapter.downRateBps, 0);
  const systemUploadSpeed = systemTraffic.adapters.reduce((total, adapter) => total + adapter.upRateBps, 0);
  const torrentDownloadSpeed = torrentTasks.reduce((total, task) => total + task.speedBps, 0);
  const torrentUploadSpeed = torrentTasks.reduce((total, task) => total + task.uploadSpeedBps, 0);
  const activeTorrentCount = torrentTasks.filter((task) =>
    task.state === "metadata" || task.state === "downloading" || task.state === "cancelling" || task.state === "resuming",
  ).length;
  const totalTorrentDownloaded = torrentTasks.reduce((total, task) => total + task.downloaded, 0);
  const activeDownloadCount = downloadTasks.filter((task) =>
    task.state === "downloading" || task.state === "cancelling" || task.state === "resuming",
  ).length;
  const totalDownloadReceived = downloadTasks.reduce((total, task) => total + task.downloaded, 0);
  const selectedTorrent = torrentTasks.find((task) => task.id === selectedTorrentId) ?? torrentTasks[0];
  const selectedDownload = downloadTasks.find((task) => task.id === selectedDownloadId) ?? downloadTasks[0];

  useEffect(() => {
    if (!systemTraffic.supported) return;
    const rates = Object.fromEntries(
      systemTraffic.adapters.map((adapter) => [adapter.interfaceIndex, adapter.downRateBps]),
    );
    setTrafficHistory((current) => [...current, { rates }].slice(-60));
  }, [systemTraffic]);

  const refreshNetworks = useCallback(async (manual = false) => {
    if (networkRefreshInFlight.current) return;
    networkRefreshInFlight.current = true;
    if (manual) setNetworkRefreshing(true);
    try {
      const networks = await api<Network[]>("/api/download-networks");
      if (networkRefreshMounted.current) {
        setNetworksLoaded(true);
        setLinks((current) =>
          networks.map((network, index) => {
            const existing = current.find((link) => link.interfaceIndex === network.interfaceIndex);
            return {
              ...network,
              color: existing?.color ?? linkColors[index % linkColors.length],
              enabled: existing?.enabled ?? true,
              downloaded: existing?.downloaded ?? 0,
              speedBps: existing?.speedBps ?? 0,
            };
          }),
        );
      }
    } catch (error) {
      if (networkRefreshMounted.current) {
        setNotice(error instanceof Error ? error.message : "Could not read network adapters.");
      }
    } finally {
      networkRefreshInFlight.current = false;
      if (manual && networkRefreshMounted.current) setNetworkRefreshing(false);
    }
  }, []);

  useEffect(() => {
    let active = true;
    networkRefreshMounted.current = true;
    const refreshDownloads = async () => {
      try {
        const downloads = await api<Download[]>("/api/downloads");
        if (active) {
          setDownloadTasks(downloads);
          setNotice("");
          if (downloads.length > 0) {
            setDownload((current) => {
              const inProgress = downloads.find(
                (item) => item.state === "downloading" || item.state === "cancelling",
              );
              return inProgress ||
                (current && downloads.find((item) => item.id === current.id)) ||
                downloads[downloads.length - 1];
            });
          } else {
            setDownload(undefined);
          }
        }
      } catch (error) {
        if (active) setNotice(error instanceof Error ? error.message : "Could not read download status.");
      }
    };
    const refreshTorrents = async () => {
      try {
        const torrents = await api<TorrentDownload[]>("/api/torrents");
        if (active) {
          setTorrentTasks(torrents);
        }
      } catch (error) {
        if (active) setNotice(error instanceof Error ? error.message : "Could not read torrent status.");
      }
    };
    const refreshTunnel = async () => {
      try {
        const status = await api<TunnelStatus>("/api/status");
        if (active) {
          setNotice("");
          setTunnel(status);
        }
      } catch {
        if (active) {
          setTunnel({
            state: "unavailable",
            active: false,
            fullTunnel: false,
            multipathActive: false,
            localRouter: { active: false, available: false, elevated: false, networks: [] },
          });
        }
      }
    };
    const refreshLocalProxy = async () => {
      try {
        const status = await api<LocalProxy>("/api/local-proxy");
        if (active && !proxySyncInFlight.current) setLocalProxy(status);
      } catch (error) {
        if (active) setNotice(error instanceof Error ? error.message : "Could not read local proxy status.");
      }
    };
    const refreshSystemProxy = async () => {
      try {
        const status = await api<SystemProxy>("/api/system-proxy");
        if (active) setSystemProxy(status);
      } catch (error) {
        if (active) setNotice(error instanceof Error ? error.message : "Could not read Windows proxy status.");
      }
    };
    const refreshSystemTraffic = async () => {
      try {
        const traffic = await api<SystemTraffic>("/api/system-traffic");
        if (active) setSystemTraffic(traffic);
      } catch (error) {
        if (active) setNotice(error instanceof Error ? error.message : "Could not read system network speeds.");
      }
    };
    void refreshNetworks();
    void refreshDownloads();
    void refreshTorrents();
    void refreshTunnel();
    void refreshLocalProxy();
    void refreshSystemProxy();
    void refreshSystemTraffic();
    const timer = window.setInterval(() => {
      void refreshDownloads();
      void refreshTorrents();
      void refreshTunnel();
      void refreshLocalProxy();
      void refreshSystemProxy();
      void refreshSystemTraffic();
    }, 1000);
    const networkTimer = window.setInterval(() => void refreshNetworks(), 3000);
    return () => {
      active = false;
      networkRefreshMounted.current = false;
      window.clearInterval(timer);
      window.clearInterval(networkTimer);
    };
  }, [refreshNetworks]);

  useEffect(() => {
    if (!networksLoaded || !localProxy.running || proxyBusy) {
      if (!localProxy.running) proxySyncSignature.current = "";
      return;
    }

    const selected = links.filter((link) => link.enabled);
    if (selected.length === 0 || proxySyncInFlight.current) return;
    if (proxyAdaptersMatch(selected, localProxy.adapters)) {
      proxySyncSignature.current = "";
      return;
    }

    const signature = JSON.stringify(selected.map(({ interfaceIndex, name, address }) => ({
      interfaceIndex,
      name,
      address,
    })));
    if (signature === proxySyncSignature.current) return;
    proxySyncSignature.current = signature;
    proxySyncInFlight.current = true;
    void api<LocalProxy>("/api/local-proxy", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        enabled: true,
        interfaceIndices: selected.map((link) => link.interfaceIndex),
      }),
    })
      .then(setLocalProxy)
      .catch((error) => {
        setNotice(error instanceof Error ? error.message : "Could not refresh proxy adapters.");
      })
      .finally(() => {
        proxySyncInFlight.current = false;
      });
  }, [links, localProxy.adapters, localProxy.running, networksLoaded, proxyBusy]);

  async function queueLink(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = url.trim();
    if (!trimmed) {
      setNotice("Paste a direct HTTP(S) download link.");
      return;
    }
    if (selectedNetworks.length < 2) {
      setNotice("Select both Wi-Fi and Ethernet to combine their bandwidth.");
      return;
    }
    try {
      const created = await api<Download>("/api/downloads", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          url: trimmed,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setDownload(created);
      setSelectedDownloadId(created.id);
      setNotice(`Downloading ${created.name} across ${selectedNetworks.length} network adapters.`);
      setUrl("");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not start the combined download.");
    }
  }

  async function cancelDownload(id = download?.id) {
    if (!id) return;
    try {
      await api<{ success: boolean }>("/api/downloads/cancel", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      });
      setNotice("Pausing download...");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not cancel download.");
    }
  }

  async function resumeDownload(id: string) {
    if (selectedNetworks.length < 2) {
      setNotice("Select at least two active adapters to resume this download.");
      return;
    }
    try {
      const resumed = await api<Download>("/api/downloads/resume", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setDownload(resumed);
      setNotice(`Resuming ${resumed.name} across ${selectedNetworks.length} network adapters.`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not resume download.");
    }
  }

  async function queueTorrent(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const source = torrentSource.trim();
    if (!source) {
      setNotice("Paste a magnet link or a direct HTTP(S) .torrent URL.");
      return;
    }
    if (selectedNetworks.length < 2) {
      setNotice("Select at least two active adapters for a torrent download.");
      return;
    }
    try {
      const created = await api<TorrentDownload>("/api/torrents", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          source,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setSelectedTorrentId(created.id);
      setTorrentSource("");
      setNotice(`Added ${created.name || "torrent"} across ${created.networks.join(" and ")}.`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not start the torrent download.");
    }
  }

  async function cancelTorrent(id: string) {
    try {
      await api<{ success: boolean }>("/api/torrents/cancel", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      });
      setNotice("Pausing torrent download...");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not stop the torrent download.");
    }
  }

  async function resumeTorrent(id: string) {
    if (selectedNetworks.length < 2) {
      setNotice("Select at least two active adapters to resume this torrent.");
      return;
    }
    try {
      const resumed = await api<TorrentDownload>("/api/torrents/resume", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          id,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setNotice(`Resuming ${resumed.name || "torrent"} across ${selectedNetworks.length} network adapters.`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not resume torrent.");
    }
  }

  async function stopTransfer(kind: "downloads" | "torrents", id: string) {
    const isDownloadQueue = kind === "downloads";
    const listPath = `/api/${kind}`;
    const cancelPath = `/api/${kind}/cancel`;
    const discardPath = `/api/${kind}/discard`;
    try {
      const currentItems = isDownloadQueue ? downloadTasks : torrentTasks;
      const current = currentItems.find((item) => item.id === id);
      if (!current) throw new Error("Transfer is no longer in the queue.");
      if (["downloading", "metadata", "cancelling", "resuming"].includes(current.state)) {
        if (current.state !== "cancelling") {
          await api<{ success: boolean }>(cancelPath, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ id }),
          });
        }
        let stopped = false;
        for (let attempt = 0; attempt < 100; attempt++) {
          await new Promise((resolve) => window.setTimeout(resolve, 200));
          const items = await api<(Download | TorrentDownload)[]>(listPath);
          const task = items.find((item) => item.id === id);
          if (!task || !["downloading", "metadata", "cancelling", "resuming"].includes(task.state)) {
            stopped = true;
            break;
          }
        }
        if (!stopped) throw new Error("Transfer did not stop in time.");
      }
      await api<{ success: boolean }>(discardPath, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id }),
      });
      setNotice(isDownloadQueue
        ? "Stopped download and removed its partial file; completed files are kept."
        : "Stopped torrent and removed its resume data; downloaded files are kept.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not stop transfer.");
    }
  }

  async function openTransfer(kind: "downloads" | "torrents", id: string, action: "open" | "open-folder") {
    try {
      await api<{ success: boolean }>(`/api/${kind}/action`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id, action }),
      });
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not open transfer.");
    }
  }

  function beginMove(kind: "downloads" | "torrents", id: string, location: string) {
    setMovingTransfer({ kind, id });
    setMoveDestination(parentFolder(location));
  }

  async function browseMoveDestination() {
    try {
      const result = await api<{ path: string }>("/api/select-transfer-folder", {
        method: "POST",
      });
      setMoveDestination(result.path);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not choose a folder.");
    }
  }

  async function moveTransfer() {
    if (!movingTransfer) return;
    try {
      if (movingTransfer.kind === "downloads") {
        const updated = await api<Download>("/api/downloads/action", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: movingTransfer.id, action: "move", destination: moveDestination }),
        });
        setDownloadTasks((current) => current.map((task) => task.id === updated.id ? updated : task));
        setDownload((current) => current?.id === updated.id ? updated : current);
      } else {
        const updated = await api<TorrentDownload>("/api/torrents/action", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: movingTransfer.id, action: "move", destination: moveDestination }),
        });
        setTorrentTasks((current) => current.map((task) => task.id === updated.id ? updated : task));
      }
      setMovingTransfer(undefined);
      setNotice("Transfer location updated.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not move transfer.");
    }
  }

  async function toggleSystemWide() {
    if (tunnelBusy) return;
    setTunnelBusy(true);
    try {
      const enabled = !tunnel.localRouter.active;
      const status = await api<TunnelStatus["localRouter"]>("/api/local-router", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      setTunnel((current) => ({ ...current, localRouter: status }));
      setNotice(enabled
        ? "Local system router enabled. Separate IPv4 connections are distributed across active adapters."
        : "Local system router disabled.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not change system-wide tunnel state.");
    } finally {
      setTunnelBusy(false);
    }
  }

  async function saveAdapterRateCap(interfaceIndex: number) {
    const raw = rateCapDrafts[interfaceIndex];
    const limitMbps = Number(raw ?? (tunnel.localRouter.adapterRateCaps?.[interfaceIndex] ?? 0) / 125_000);
    if (!Number.isFinite(limitMbps) || limitMbps < 0 || limitMbps > 1_000_000) {
      setNotice("Enter a rate cap from 0 to 1,000,000 Mbps. Use 0 for unlimited.");
      return;
    }
    try {
      const status = await api<TunnelStatus["localRouter"]>("/api/local-router/rate-limit", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ interfaceIndex, limitMbps }),
      });
      setTunnel((current) => ({ ...current, localRouter: status }));
      setRateCapDrafts((current) => {
        const next = { ...current };
        delete next[interfaceIndex];
        return next;
      });
      setNotice(limitMbps === 0
        ? "Adapter rate cap removed."
        : `Adapter capped at ${limitMbps} Mbps.`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not save adapter rate cap.");
    }
  }

  async function toggleLocalProxy() {
    if (proxyBusy) return;
    setProxyBusy(true);
    try {
      const enabled = !localProxy.running;
      const status = await api<LocalProxy>("/api/local-proxy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          enabled,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setLocalProxy(status);
      setNotice(status.running
        ? `Local proxy listening at SOCKS5 ${status.socksAddress} and HTTP ${status.httpAddress}.`
        : "Local multi-adapter proxy stopped.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not change local proxy state.");
    } finally {
      setProxyBusy(false);
    }
  }

  async function toggleWindowsProxy() {
    if (proxyBusy) return;
    setProxyBusy(true);
    try {
      const enabled = !(systemProxy.enabled || systemProxy.managed);
      const status = await api<SystemProxy>("/api/system-proxy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          enabled,
          interfaceIndices: selectedNetworks.map((network) => network.interfaceIndex),
        }),
      });
      setSystemProxy(status);
      setLocalProxy(await api<LocalProxy>("/api/local-proxy"));
      setNotice(status.enabled
        ? `Windows user proxy enabled at ${status.address}.`
        : status.notice || "Windows user proxy restored.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not change Windows proxy settings.");
    } finally {
      setProxyBusy(false);
    }
  }

  async function launchAppViaProxy(preset?: "steam") {
    if (proxyLaunchBusy) return;
    const executable = proxyLaunchTarget.trim();
    if (!preset && !executable) {
      setNotice("Enter the executable path to launch.");
      return;
    }

    setProxyLaunchBusy(true);
    try {
      const result = await api<ProxyLaunchResult>("/api/launch-proxy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(preset ? { preset } : { executable }),
      });
      if (result.preset === "steam") setProxyLaunchTarget(result.executable);
      setNotice(`Started ${result.preset === "steam" ? "Steam" : result.executable} with Ethify proxy settings (PID ${result.processId}).`);
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Could not launch the app through the proxy.");
    } finally {
      setProxyLaunchBusy(false);
    }
  }

  async function copyProxyAddress(address: string) {
    try {
      await navigator.clipboard.writeText(address);
      setNotice(`Copied ${address}.`);
    } catch {
      setNotice("Clipboard access is unavailable in this browser.");
    }
  }

  async function updateAdapterEnabled(index: number, enabled: boolean) {
    const nextLinks = links.map((link) =>
      link.interfaceIndex === index ? { ...link, enabled } : link,
    );
    setLinks(nextLinks);
    if (!localProxy.running) return;
    setProxyBusy(true);
    try {
      const status = await api<LocalProxy>("/api/local-proxy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          enabled: true,
          interfaceIndices: nextLinks.filter((link) => link.enabled).map((link) => link.interfaceIndex),
        }),
      });
      setLocalProxy(status);
    } catch (error) {
      setLinks(links);
      setNotice(error instanceof Error ? error.message : "Could not update proxy adapters.");
    } finally {
      setProxyBusy(false);
    }
  }

  return (
    <main className="app-shell" data-active-tab={activeTab}>
      <header className="topbar">
        <a className="brand" href="#" aria-label="Ethify home">
          <span className="brand-mark" aria-hidden="true">
            <svg viewBox="0 0 64 64" fill="none">
              <path d="M47 13H25a12 12 0 0 0-12 12v14a12 12 0 0 0 12 12h22M14 32h27" />
              <circle cx="43" cy="32" r="3" />
            </svg>
          </span>
          <span>Ethify</span>
        </a>
        <div className="topbar-right">
          <span className={`admin-badge ${tunnel.localRouter.elevated ? "ready" : ""}`}>
            <span className="admin-shield">✓</span>
            {tunnel.localRouter.elevated ? "ADMIN READY" : "ADMIN REQUIRED"}
          </span>
          <span className={`router-badge ${tunnel.localRouter.active ? "active" : ""}`}>
            {tunnel.localRouter.active ? "ACTIVE · WINTUN" : "ROUTER OFF"}
          </span>
        </div>
      </header>

      <div className="dashboard-layout">
        <nav className="sidebar-nav" aria-label="Dashboard sections" role="tablist" aria-orientation="vertical">
          <button className={`sidebar-tab ${activeTab === "system" ? "active" : ""}`} onClick={() => setActiveTab("system")} role="tab" aria-selected={activeTab === "system"} type="button">
            <Icon name="ethernet" size={18} /><span>System-wide</span>
          </button>
          <button className={`sidebar-tab ${activeTab === "downloads" ? "active" : ""}`} onClick={() => setActiveTab("downloads")} role="tab" aria-selected={activeTab === "downloads"} type="button">
            <Icon name="download" size={18} /><span>Downloads</span>
          </button>
          <button className={`sidebar-tab ${activeTab === "torrents" ? "active" : ""}`} onClick={() => setActiveTab("torrents")} role="tab" aria-selected={activeTab === "torrents"} type="button">
            <Icon name="link" size={18} /><span>Torrents</span>
          </button>
        </nav>
        <div className="dashboard-content">
      <div className="system-panel">
      <section className="page-heading" id="system-wide-heading">
        <div>
          <p className="eyebrow">ETHIFI · LOCAL MULTI-WAN</p>
          <h1>System-wide router</h1>
          <p className="subheading">Routes independent IPv4 connections over your active internet adapters.</p>
        </div>
        <div className="network-heading-actions">
          <div className="heading-status"><span className="status-dot" />{links.length} adapters detected</div>
          <button
            className="quiet-button"
            disabled={networkRefreshing}
            onClick={() => void refreshNetworks(true)}
            type="button"
          >
            {networkRefreshing ? "Refreshing..." : "Refresh"}
          </button>
        </div>
      </section>

      <section className="router-dashboard" aria-label="System-wide router dashboard">
        <div className="router-overview">
          <article className={`router-master-card ${tunnel.localRouter.active ? "is-enabled" : ""}`}>
            <div className="router-card-heading">
              <h2>Master Router</h2>
              <span className={`router-mini-state ${tunnel.localRouter.active ? "on" : ""}`}>
                {tunnel.localRouter.active ? "ON" : "OFF"}
              </span>
            </div>
            <button
              aria-checked={tunnel.localRouter.active}
              aria-label="System-wide local router"
              className={`router-power-switch ${tunnel.localRouter.active ? "checked" : ""}`}
              disabled={tunnelBusy || !tunnel.localRouter.available || (tunnel.active && !tunnel.localRouter.active)}
              onClick={() => void toggleSystemWide()}
              role="switch"
              type="button"
            >
              <span>{tunnelBusy ? "..." : tunnel.localRouter.active ? "ON" : "OFF"}</span>
              <i />
            </button>
            <p className="router-master-description">System-Wide Local Router (Wintun)</p>
            <div className="router-flow-count">
              <strong>{(tunnel.localRouter.tcpFlows ?? 0) + (tunnel.localRouter.udpFlows ?? 0)}</strong>
              <span>Flows captured</span>
            </div>
            <p className="router-egress-count">
              Direct egress: {tunnel.localRouter.activeEgress ?? 0} / {tunnel.localRouter.egressLimit ?? 512} active
            </p>
            {!tunnel.localRouter.active && !tunnel.localRouter.elevated && tunnel.localRouter.available && (
              <p className="router-warning">Run Ethify as Administrator to install system routes.</p>
            )}
            {tunnel.active && !tunnel.localRouter.active && (
              <p className="router-warning">Disconnect the relay tunnel before enabling local routing.</p>
            )}
            {tunnel.localRouter.lastError && <p className="router-warning">{tunnel.localRouter.lastError}</p>}
          </article>

          <article className="router-chart-card">
            <div className="router-chart-heading">
              <div>
                <h2>Total Internet Traffic</h2>
                <span>All apps · download speed across connected adapters</span>
              </div>
              <div className="router-speed-total">
                <strong>{dashboardSpeed(systemDownloadSpeed)}</strong>
                <span>{(systemDownloadSpeed * 8 / 1_000_000).toFixed(1)} Mbps</span>
              </div>
            </div>
            <div className="router-chart">
              <svg viewBox="0 0 100 100" preserveAspectRatio="none" role="img" aria-label="Recent download speed by adapter">
                <defs>
                  <linearGradient id="router-fill-0" x1="0" x2="0" y1="0" y2="1">
                    <stop offset="0%" stopColor="#16b7d0" stopOpacity=".42" />
                    <stop offset="100%" stopColor="#16b7d0" stopOpacity=".03" />
                  </linearGradient>
                  <linearGradient id="router-fill-1" x1="0" x2="0" y1="0" y2="1">
                    <stop offset="0%" stopColor="#20d68e" stopOpacity=".42" />
                    <stop offset="100%" stopColor="#20d68e" stopOpacity=".03" />
                  </linearGradient>
                </defs>
                <path className="router-chart-grid" d="M0 20H100M0 45H100M0 70H100M0 95H100" />
                {links.map((link) => {
                  const values = trafficHistory.map((sample) => sample.rates[link.interfaceIndex] ?? 0);
                  const maximum = Math.max(1, ...trafficHistory.flatMap((sample) => Object.values(sample.rates)));
                  const isWiFi = /wi-?fi/i.test(link.name);
                  const color = isWiFi ? "#20d68e" : "#16b7d0";
                  return (
                    <g key={link.interfaceIndex} className="router-chart-series">
                      <path d={trafficPath(values, maximum, true)} fill={`url(#router-fill-${isWiFi ? 1 : 0})`} />
                      <path d={trafficPath(values, maximum)} stroke={color} />
                    </g>
                  );
                })}
              </svg>
            </div>
            <div className="router-chart-legend">
              {links.map((link) => (
                <span key={link.interfaceIndex}>
                  <i className={/wi-?fi/i.test(link.name) ? "wifi-dot" : "ethernet-dot"} />{link.name} download
                </span>
              ))}
              <span>↑ {dashboardSpeed(systemUploadSpeed)} upload total</span>
              <span>Last 60 seconds</span>
            </div>
          </article>
        </div>

        <div className="router-adapter-grid" id="networks">
          {links.map((link, index) => {
            const traffic = systemTraffic.adapters.find((adapter) => adapter.interfaceIndex === link.interfaceIndex);
            const down = traffic?.downRateBps ?? 0;
            const up = traffic?.upRateBps ?? 0;
            const max = Math.max(1, ...systemTraffic.adapters.map((adapter) => adapter.downRateBps));
            const connected = (tunnel.localRouter.networks ?? []).some((network) => network.interfaceIndex === link.interfaceIndex);
            const routerLoad = tunnel.localRouter.adapterLoads?.find((load) => load.interfaceIndex === link.interfaceIndex);
            const color = /wi-?fi/i.test(link.name) ? "wifi" : "ethernet";
            return (
              <article className={`router-adapter-card ${color}`} key={link.interfaceIndex}>
                <div className="router-adapter-heading">
                  <span className="router-adapter-icon"><Icon name={/wi-?fi/i.test(link.name) ? "wifi" : "ethernet"} size={22} /></span>
                  <strong>{link.name}</strong>
                  <span className={`router-address-badge ${color}`}><b>IPv4</b>{link.address}</span>
                </div>
                <div className="router-adapter-details">
                  <div>
                    <span>Live download</span>
                    <strong>{dashboardSpeed(down)}</strong>
                    <small>↑ {dashboardSpeed(up)} upload</small>
                  </div>
                  <span className={`router-link-state ${connected ? "connected" : ""}`}>
                    <i />{connected ? "Router ready" : "Connected"}
                  </span>
                </div>
                {tunnel.localRouter.active && routerLoad && (
                  <p className="router-flow-load">
                    Router egress: {routerLoad.activeFlows} flows · {dashboardSpeed(routerLoad.bytesPerSecond)}
                  </p>
                )}
                <div className="router-adapter-track">
                  <span style={{ width: `${Math.min(100, (100 * down) / max)}%` }} />
                </div>
                <div className="router-rate-cap">
                  <label htmlFor={`rate-cap-${link.interfaceIndex}`}>Router cap (Mbps, upload + download)</label>
                  <input
                    id={`rate-cap-${link.interfaceIndex}`}
                    min="0"
                    max="1000000"
                    onChange={(event) => setRateCapDrafts((current) => ({
                      ...current,
                      [link.interfaceIndex]: event.target.value,
                    }))}
                    placeholder="0 = unlimited"
                    step="0.1"
                    type="number"
                    value={rateCapDrafts[link.interfaceIndex] ?? (
                      (tunnel.localRouter.adapterRateCaps?.[link.interfaceIndex] ?? 0) / 125_000 || ""
                    )}
                  />
                  <button
                    className="quiet-button"
                    disabled={!tunnel.localRouter.available}
                    onClick={() => void saveAdapterRateCap(link.interfaceIndex)}
                    type="button"
                  >
                    Apply
                  </button>
                </div>
              </article>
            );
          })}
          {links.length === 0 && <p className="router-empty">No active IPv4 adapters detected.</p>}
        </div>
        <p className="router-limitation">
          {systemTraffic.supported
            ? "Speeds include all apps and show actual traffic per adapter. Ethify routes separate connections across links; one connection is never split. Each router cap is shared by upload and download on that adapter and only limits traffic routed through Wintun."
            : "System-wide adapter meters are unavailable on this platform."}
        </p>
        {links.length < 2 && (
          <p className="notice" role="status">
            Ethify needs at least two active IPv4 adapters to route traffic across multiple links. Connect another adapter and refresh.
          </p>
        )}
      </section>

      <details className="advanced-tools">
        <summary>Advanced tools <span>Proxy setup and app launch</span></summary>
        <div className="advanced-tools-body">
      <section className={`local-proxy-card ${localProxy.running ? "is-enabled" : ""}`}>
        <div className="system-mode-icon"><Icon name="link" size={18} /></div>
        <div className="local-proxy-copy">
          <div className="system-mode-title">
            <h2>Local Multi-Adapter Proxy (No Relay Needed)</h2>
            <span className="optional-tag">{localProxy.running ? "RUNNING" : "LOCAL TCP"}</span>
          </div>
          <p>Balances new TCP connections across selected adapters. Configure an app to use either proxy; this is not a full tunnel and does not proxy UDP.</p>
          <div className="proxy-addresses">
            <div className="proxy-address">
              <span>SOCKS5</span><code>{localProxy.socksAddress || "127.0.0.1:1080"}</code>
              <button className="quiet-button" onClick={() => void copyProxyAddress(localProxy.socksAddress || "127.0.0.1:1080")} type="button">Copy</button>
            </div>
            <div className="proxy-address">
              <span>HTTP</span><code>{localProxy.httpAddress || "127.0.0.1:8080"}</code>
              <button className="quiet-button" onClick={() => void copyProxyAddress(localProxy.httpAddress || "127.0.0.1:8080")} type="button">Copy</button>
            </div>
          </div>
          <div className="windows-proxy-control">
            <div>
              <strong>Windows user proxy</strong>
              <span>
                {systemProxy.supported
                  ? `Admin ${systemProxy.elevated ? "elevated" : "not elevated"} · admin rights are not required for this toggle`
                  : "Available on Windows only"}
              </span>
            </div>
            <button
              aria-checked={systemProxy.enabled || systemProxy.managed}
              aria-label="Windows user proxy"
              className={`switch-button ${systemProxy.enabled || systemProxy.managed ? "checked" : ""}`}
              disabled={proxyBusy || !systemProxy.supported || (!(systemProxy.enabled || systemProxy.managed) && selectedNetworks.length < 2)}
              onClick={() => void toggleWindowsProxy()}
              role="switch"
              type="button"
            >
              <span />
            </button>
          </div>
          <p className="proxy-scope-note">
            Applies to apps that honor Windows proxy settings. Ethify does not install a packet-divert driver or transparently hook game sockets; direct per-app interception is not supported.
          </p>
          {systemProxy.lastError && <p className="proxy-error">{systemProxy.lastError}</p>}
          {localProxy.running && localProxy.adapters.length > 0 && (
            <div className="proxy-adapter-health">
              {localProxy.adapters.map((adapter) => (
                <span key={adapter.interfaceIndex} className={adapter.healthy ? "healthy" : "degraded"}>
                  {adapter.name}: {adapter.healthy ? "ready" : "retrying"} · {adapter.activeConnections} connections · {adapter.failures} errors
                </span>
              ))}
            </div>
          )}
          {localProxy.running && localProxy.adapters.length < 2 && (
            <p className="proxy-scope-note">
              Only one active adapter is available; the proxy can continue over it, but bandwidth is not being combined.
            </p>
          )}
        </div>
        <span className={`system-mode-state ${localProxy.running ? "on" : ""}`}>{localProxy.running ? "ON" : "OFF"}</span>
        <button
          aria-checked={localProxy.running}
          aria-label="Local multi-adapter proxy"
          className={`switch-button ${localProxy.running ? "checked" : ""}`}
          disabled={proxyBusy || (!localProxy.running && selectedNetworks.length < 2)}
          onClick={() => void toggleLocalProxy()}
          role="switch"
          type="button"
        >
          <span />
        </button>
      </section>

      <section className="proxy-launch-card">
        <div className="system-mode-icon"><Icon name="play" size={17} /></div>
        <div className="proxy-launch-content">
          <div className="system-mode-title">
            <h2>Launch App via Proxy</h2>
            <span className="optional-tag">{localProxy.running ? "READY" : "START PROXY FIRST"}</span>
          </div>
          <p>
            Launches an app with HTTP_PROXY, HTTPS_PROXY, and ALL_PROXY pointed at Ethify.
            Environment proxy settings only work in apps that honor them; Steam may ignore them.
          </p>
          <div className="proxy-launch-controls">
            <input
              aria-label="Target executable path"
              disabled={!localProxy.running || proxyBusy || proxyLaunchBusy}
              onChange={(event) => setProxyLaunchTarget(event.target.value)}
              placeholder="C:\\Path\\to\\app.exe"
              value={proxyLaunchTarget}
            />
            <button
              className="quiet-button"
              disabled={!localProxy.running || proxyBusy || proxyLaunchBusy || !proxyLaunchTarget.trim()}
              onClick={() => void launchAppViaProxy()}
              type="button"
            >
              {proxyLaunchBusy ? "Launching..." : "Launch App via Proxy"}
            </button>
            <button
              className="primary-button"
              disabled={!localProxy.running || proxyBusy || proxyLaunchBusy}
              onClick={() => void launchAppViaProxy("steam")}
              type="button"
            >
              Launch Steam
            </button>
          </div>
        </div>
      </section>
        </div>
      </details>
      </div>

      <section className="page-heading tab-downloads-heading">
        <div>
          <p className="eyebrow">ETHIFI · MULTI-ADAPTER TRANSFERS</p>
          <h1>Downloads</h1>
          <p className="subheading">Resume supported HTTP downloads after pausing or restarting Ethify.</p>
        </div>
      </section>
      <div className="downloads-panel">
      <form className="add-download" onSubmit={queueLink}>
        <span className="input-icon"><Icon name="link" /></span>
        <input
          aria-label="Download URL"
          value={url}
          onChange={(event) => setUrl(event.target.value)}
          placeholder="Paste a direct HTTP(S) download link..."
          type="url"
        />
        <button
          type="submit"
          className="primary-button"
          disabled={selectedNetworks.length < 2}
        >
          <Icon name="plus" size={17} />Add to queue
        </button>
      </form>
      {notice && <p className="notice" role="status">{notice}</p>}
      </div>

      <section className="page-heading tab-torrents-heading">
        <div>
          <p className="eyebrow">ETHIFI · PEER-TO-PEER</p>
          <h1>Torrents</h1>
          <p className="subheading">Resume torrent downloads from saved metadata and downloaded pieces.</p>
        </div>
      </section>
      <div className="torrents-panel">
      <section className="torrent-download-card" aria-label="Torrent downloads">
        <div className="system-mode-title">
          <h2>Torrent queue</h2>
          <span className="optional-tag">{activeTorrentCount} ACTIVE</span>
        </div>
        <p>Manage peer downloads across selected adapters. Ethify does not seed or upload torrent content.</p>
        <div className="transfer-summary-grid">
          <div><small>QUEUE DOWNLOAD</small><strong>{speed(torrentDownloadSpeed)}</strong></div>
          <div><small>QUEUE UPLOAD</small><strong>{speed(torrentUploadSpeed)}</strong></div>
          <div><small>DOWNLOADED</small><strong>{fileSize(totalTorrentDownloaded)}</strong></div>
          <div><small>TOTAL SYSTEM ↓</small><strong>{speed(systemDownloadSpeed)}</strong></div>
          <div><small>TOTAL SYSTEM ↑</small><strong>{speed(systemUploadSpeed)}</strong></div>
        </div>
        <form className="torrent-add" onSubmit={queueTorrent}>
          <input
            aria-label="Magnet link or torrent URL"
            autoComplete="off"
            onChange={(event) => setTorrentSource(event.target.value)}
            placeholder="magnet:?xt=urn:btih:... or https://example.com/file.torrent"
            value={torrentSource}
          />
          <button className="primary-button" disabled={selectedNetworks.length < 2} type="submit">
            <Icon name="plus" size={17} />Add torrent
          </button>
        </form>
        {notice && <p className="notice" role="status">{notice}</p>}
        <div className="transfer-table-wrap">
          <table className="transfer-table torrent-table">
            <thead><tr><th>Name</th><th>Size</th><th>Status</th><th>Progress</th><th>Down speed</th><th>Up speed</th><th>ETA</th><th>Peers</th><th>Actions</th></tr></thead>
            <tbody>
              {torrentTasks.map((task) => {
                const busy = ["metadata", "downloading", "cancelling", "resuming", "moving"].includes(task.state);
                return (
                  <tr className={selectedTorrent?.id === task.id ? "selected" : ""} key={task.id}>
                    <td>
                      <button className="transfer-name" onClick={() => setSelectedTorrentId(task.id)} type="button">{task.name || "Fetching metadata"}</button>
                      {task.error && <small className="download-error">{task.error}</small>}
                    </td>
                    <td>{fileSize(task.size)}</td>
                    <td><span className={`transfer-state ${task.state}`}>{task.state}</span></td>
                    <td>
                      <div className="transfer-progress">
                        <div className="progress-track"><span style={{ width: `${task.progress}%` }} /></div>
                        <small>{task.progress.toFixed(1)}% · {fileSize(task.downloaded)}</small>
                      </div>
                    </td>
                    <td>{speed(task.speedBps)}</td>
                    <td>{speed(task.uploadSpeedBps)}</td>
                    <td>{estimatedTime(task.size, task.downloaded, task.speedBps)}</td>
                    <td>{task.peers}</td>
                    <td>
                      <div className="transfer-actions">
                        {(task.state === "metadata" || task.state === "downloading") && (
                          <button className="quiet-button danger" onClick={() => void cancelTorrent(task.id)} type="button">Pause</button>
                        )}
                        {task.canResume && (
                          <button className="quiet-button" disabled={selectedNetworks.length < 2} onClick={() => void resumeTorrent(task.id)} type="button">Resume</button>
                        )}
                        <button className="quiet-button danger" disabled={task.state === "completed" || task.state === "cancelled" || task.state === "moving"} onClick={() => void stopTransfer("torrents", task.id)} type="button">Stop</button>
                        <button className="quiet-button" disabled={task.downloaded === 0} onClick={() => void openTransfer("torrents", task.id, "open")} type="button">Open</button>
                        <button className="quiet-button" onClick={() => void openTransfer("torrents", task.id, "open-folder")} type="button">Folder</button>
                        <button className="quiet-button" disabled={busy} onClick={() => beginMove("torrents", task.id, task.location)} type="button">Move</button>
                      </div>
                      {movingTransfer?.kind === "torrents" && movingTransfer.id === task.id && (
                        <div className="move-location">
                          <input aria-label="New torrent folder" onChange={(event) => setMoveDestination(event.target.value)} value={moveDestination} />
                          <button className="quiet-button" onClick={() => void browseMoveDestination()} type="button">Browse</button>
                          <button className="quiet-button" onClick={() => void moveTransfer()} type="button">Apply</button>
                          <button className="quiet-button" onClick={() => setMovingTransfer(undefined)} type="button">Cancel</button>
                        </div>
                      )}
                    </td>
                  </tr>
                );
              })}
              {torrentTasks.length === 0 && <tr><td className="transfer-empty" colSpan={9}>No torrents in the queue yet.</td></tr>}
            </tbody>
          </table>
        </div>
        {selectedTorrent && (
          <div className="transfer-details">
            <div><span>DOWNLOADED</span><strong>{fileSize(selectedTorrent.downloaded)} / {fileSize(selectedTorrent.size)}</strong></div>
            <div><span>NETWORKS</span><strong>{selectedTorrent.networks.join(", ") || "Not connected"}</strong></div>
            <div className="transfer-location"><span>SAVE LOCATION</span><strong title={selectedTorrent.location}>{selectedTorrent.location}</strong></div>
          </div>
        )}
      </section>
      </div>

      <div className="downloads-panel">
      <section className="speed-card" aria-label="Ethify managed transfer speed">
        <div className="speed-summary">
          <div className="link-speeds">
            {links.map((link) => (
              <div className={`link-speed ${!link.enabled ? "muted" : ""}`} key={link.interfaceIndex}>
                <span className="link-indicator" style={{ backgroundColor: link.color }} />
                <div>
                  <span className="link-label">{link.name.toUpperCase()}</span>
                  <strong>
                    {link.enabled
                      ? speed(adapterSpeed(link.interfaceIndex))
                      : "Disabled"}
                  </strong>
                </div>
              </div>
            ))}
          </div>
          <div className="total-speed">
            <span className="eyebrow">ETHIFI-MANAGED TRANSFER SPEED</span>
            <strong>{speed(totalSpeed)}</strong>
            <span className="speedup-pill">Ranged downloads and proxy traffic only</span>
          </div>
        </div>
        <div className="chart-wrap">
          <div className="chart-heading"><span>ETHIFI-MANAGED ADAPTER TRAFFIC</span><span>{localProxy.running ? "PROXY + DOWNLOAD" : download?.state.toUpperCase() ?? "IDLE"}</span></div>
          <div className="chart live-chart">
            {links.map((link) => {
              const networkSpeed = adapterSpeed(link.interfaceIndex);
              const width = totalSpeed > 0 ? (100 * networkSpeed) / totalSpeed : 0;
              return (
                <div className="live-bar-row" key={link.interfaceIndex}>
                  <span>{link.name}</span>
                  <div className="live-bar-track">
                    <span style={{ width: `${width}%`, backgroundColor: link.color }} />
                  </div>
                  <strong>{speed(networkSpeed)}</strong>
                </div>
              );
            })}
          </div>
          <div className="chart-time"><span>Does not include unrelated system traffic</span><span>live</span></div>
        </div>
      </section>

      <section className="adapter-usage-card" aria-label="Applications using network adapters">
        <div className="adapter-usage-heading">
          <div>
            <p className="eyebrow">LIVE NETWORK USAGE</p>
            <h2>Apps using Ethernet &amp; Wi-Fi</h2>
          </div>
          <div className="adapter-usage-total">
            <span>Current TCP traffic</span>
            <strong>
              {speed((systemTraffic.processes ?? []).reduce(
                (total, process) => total + process.downRateBps + process.upRateBps,
                0,
              ))}
            </strong>
            <small>Per app and adapter</small>
          </div>
        </div>
        {!systemTraffic.processTrafficSupported ? (
          <p className="adapter-usage-note">
            {systemTraffic.processTrafficNote || "Per-app traffic details are unavailable on this system."}
          </p>
        ) : (systemTraffic.processes ?? []).length === 0 ? (
          <p className="adapter-usage-note">Waiting for active TCP traffic on Ethernet or Wi-Fi.</p>
        ) : (
          <div className="adapter-usage-list">
            {(systemTraffic.processes ?? []).map((process) => {
              const isWiFi = /wi-?fi/i.test(process.interfaceName);
              return (
                <article className="adapter-usage-row" key={`${process.processId}-${process.interfaceIndex}`}>
                  <i className={`adapter-usage-indicator ${isWiFi ? "wifi" : "ethernet"}`} />
                  <div className="adapter-usage-name">
                    <strong>{process.name}</strong>
                    <small>{process.interfaceName} · PID {process.processId} · {process.connections} TCP connections</small>
                  </div>
                  <span className="adapter-usage-speed">
                    ↓ {speed(process.downRateBps)} · ↑ {speed(process.upRateBps)}
                  </span>
                </article>
              );
            })}
          </div>
        )}
        {systemTraffic.processTrafficSupported && (
          <p className="adapter-usage-note">
            {systemTraffic.processTrafficNote || "Rates are sampled once per second; UDP traffic is included in adapter totals but cannot be attributed per app."}
          </p>
        )}
      </section>

      <section className="download-card" aria-label="Current download">
        <div className="download-top">
          <div className="file-icon"><span>FILE</span></div>
          <div className="file-info">
            <div className="file-title-row">
              <h2>{download?.name ?? "No download running"}</h2>
              {download && <span className="sample-tag">{download.state.toUpperCase()}</span>}
            </div>
            <p>
              <strong>{fileSize(download?.downloaded ?? 0)}</strong> of {fileSize(download?.size ?? 0)}
              <span className="separator"> · </span>
              {download ? `${download.progress.toFixed(1)}%` : "Paste a link to start"}
              {download?.error && <span className="download-error"> · {download.error}</span>}
            </p>
          </div>
          <div className="download-actions">
            {download?.state === "downloading" && (
              <button className="quiet-button danger" onClick={() => void cancelDownload()}>Pause</button>
            )}
            <span className="save-location">Saves to Downloads</span>
          </div>
        </div>

        <div className="chunk-section">
          <div className="chunk-caption">
            <span>RANGE DOWNLOAD</span>
            <span>{download ? `${fileSize(download.size)} total` : "Ready"}</span>
            <small>Parallel byte ranges are pinned to each selected adapter</small>
          </div>
          <div
            className="progress-track overall-progress"
            role="progressbar"
            aria-valuenow={download?.progress ?? 0}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label="Download progress"
          >
              <span style={{ width: `${download?.progress ?? 0}%` }} />
          </div>
        </div>

        <div className="network-table" id="download-networks">
          <div className="network-table-head">
            <span>NETWORK</span><span>SHARE</span><span>PROGRESS</span><span>SPEED</span><span>DOWNLOADED</span>
          </div>
          {links.map((link) => {
            const network = download?.networks.find((item) => item.interfaceIndex === link.interfaceIndex);
            const progress = download?.size ? ((network?.downloaded ?? 0) / download.size) * 100 : 0;
            return (
              <div className={`network-row ${!link.enabled ? "disabled" : ""}`} key={link.interfaceIndex}>
                <div className="network-name">
                  <i className="legend-dot" style={{ backgroundColor: link.color }} />
                  <label className="toggle">
                    <input
                      type="checkbox"
                      checked={link.enabled}
                      disabled={activeDownloadCount > 0 || isTorrentDownloading || proxyBusy}
                      onChange={(event) => void updateAdapterEnabled(link.interfaceIndex, event.target.checked)}
                      aria-label={`Use ${link.name}`}
                    />
                    <span />
                  </label>
                  <strong>{link.name}</strong>
                  <small>{link.address}</small>
                </div>
                <div className="share-cell">{link.enabled ? "4 workers" : "Off"}</div>
                <div className="progress-cell"><div className="progress-track"><span style={{ width: `${progress}%`, backgroundColor: link.color }} /></div></div>
                <div className="rate-cell">{link.enabled ? speed(adapterDownloadSpeed(link.interfaceIndex)) : "—"}</div>
                <div className="downloaded-cell">{fileSize(network?.downloaded ?? 0)}</div>
              </div>
            );
          })}
        </div>
        <section className="transfer-queue" aria-label="Download queue">
          <div className="transfer-queue-heading">
            <div><p className="eyebrow">DOWNLOAD MANAGER</p><h2>All downloads</h2></div>
            <span>{activeDownloadCount} active · {downloadTasks.length} total</span>
          </div>
          <div className="transfer-summary-grid">
            <div><small>QUEUE SPEED</small><strong>{speed(downloadTasks.reduce((total, task) => total + task.speedBps, 0))}</strong></div>
            <div><small>DOWNLOADED</small><strong>{fileSize(totalDownloadReceived)}</strong></div>
            <div><small>TOTAL SYSTEM ↓</small><strong>{speed(systemDownloadSpeed)}</strong></div>
            <div><small>TOTAL SYSTEM ↑</small><strong>{speed(systemUploadSpeed)}</strong></div>
          </div>
          <div className="transfer-table-wrap">
            <table className="transfer-table download-table">
              <thead><tr><th>File name</th><th>Size</th><th>Status</th><th>Progress</th><th>Transfer rate</th><th>Time left</th><th>Actions</th></tr></thead>
              <tbody>
                {downloadTasks.map((task) => {
                  const busy = ["downloading", "cancelling", "resuming", "moving"].includes(task.state);
                  return (
                    <tr className={selectedDownload?.id === task.id ? "selected" : ""} key={task.id}>
                      <td>
                        <button className="transfer-name" onClick={() => setSelectedDownloadId(task.id)} type="button">{task.name}</button>
                        {task.error && <small className="download-error">{task.error}</small>}
                      </td>
                      <td>{fileSize(task.size)}</td>
                      <td><span className={`transfer-state ${task.state}`}>{task.state}</span></td>
                      <td>
                        <div className="transfer-progress">
                          <div className="progress-track"><span style={{ width: `${task.progress}%` }} /></div>
                          <small>{task.progress.toFixed(1)}% · {fileSize(task.downloaded)}</small>
                        </div>
                      </td>
                      <td>{speed(task.speedBps)}</td>
                      <td>{estimatedTime(task.size, task.downloaded, task.speedBps)}</td>
                      <td>
                        <div className="transfer-actions">
                          {(task.state === "downloading" || task.state === "cancelling") && (
                            <button className="quiet-button danger" onClick={() => void cancelDownload(task.id)} type="button">Pause</button>
                          )}
                          {task.canResume && (
                            <button className="quiet-button" disabled={selectedNetworks.length < 2} onClick={() => void resumeDownload(task.id)} type="button">Resume</button>
                          )}
                          <button className="quiet-button danger" disabled={task.state === "moving"} onClick={() => void stopTransfer("downloads", task.id)} type="button">Stop</button>
                          <button className="quiet-button" disabled={task.state !== "completed"} onClick={() => void openTransfer("downloads", task.id, "open")} type="button">Open</button>
                          <button className="quiet-button" onClick={() => void openTransfer("downloads", task.id, "open-folder")} type="button">Folder</button>
                          <button className="quiet-button" disabled={busy} onClick={() => beginMove("downloads", task.id, task.location)} type="button">Move</button>
                        </div>
                        {movingTransfer?.kind === "downloads" && movingTransfer.id === task.id && (
                          <div className="move-location">
                            <input aria-label="New download folder" onChange={(event) => setMoveDestination(event.target.value)} value={moveDestination} />
                            <button className="quiet-button" onClick={() => void browseMoveDestination()} type="button">Browse</button>
                            <button className="quiet-button" onClick={() => void moveTransfer()} type="button">Apply</button>
                            <button className="quiet-button" onClick={() => setMovingTransfer(undefined)} type="button">Cancel</button>
                          </div>
                        )}
                      </td>
                    </tr>
                  );
                })}
                {downloadTasks.length === 0 && <tr><td className="transfer-empty" colSpan={7}>No downloads in the queue yet.</td></tr>}
              </tbody>
            </table>
          </div>
          {selectedDownload && (
            <div className="transfer-details">
              <div><span>DOWNLOADED</span><strong>{fileSize(selectedDownload.downloaded)} / {fileSize(selectedDownload.size)}</strong></div>
              <div><span>NETWORKS</span><strong>{selectedDownload.networks.map((network) => network.name).join(", ") || "Not connected"}</strong></div>
              <div className="transfer-location"><span>SAVE LOCATION</span><strong title={selectedDownload.location}>{selectedDownload.location}</strong></div>
            </div>
          )}
        </section>
      </section>
      </div>

      <footer className="app-footer">
        <span>Downloads combine active adapters only when the server supports HTTP byte ranges.</span>
        <span>The local router distributes separate IPv4 flows, not a single Steam connection; IPv6 traffic is not routed through it.</span>
        <span>Ethify · Owned by A2L · Made by A2L</span>
      </footer>
        </div>
      </div>
    </main>
  );
}

export default App;
