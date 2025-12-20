/// <reference types="vite/client" />

declare global {
  interface Window {
    __CORETEXOS_STUDIO_CONFIG__?: {
      apiBase?: string;
      wsBase?: string;
      apiKey?: string;
    };
  }
}

export {};

