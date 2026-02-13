export const API_BASE_URL =
  import.meta.env.VITE_API_URL || "/api/v1";

export const WS_BASE_URL =
  import.meta.env.VITE_WS_URL ||
  `${window.location.protocol === "https:" ? "wss:" : "ws:"}//${window.location.host}/api/v1/stream`;

export const APP_TITLE =
  import.meta.env.VITE_APP_TITLE || "Cordum Dashboard";
