export interface Job {
    id: string;
    updatedAt?: number;
    state: string;
    traceId?: string;
    resultPtr?: string;
    result?: any;
}

export interface Worker {
    workerId: string;
    type: string;
    cpuLoad: number;
    gpuUtilization: number;
    activeJobs: number;
    pool?: string;
}

export interface BusPacket {
    traceId?: string;
    senderId?: string;
    createdAt?: string;
    trace_id?: string;
    sender_id?: string;
    created_at?: string;
    jobRequest?: any;
    jobResult?: {
        jobId: string;
        status: string;
        resultPtr: string;
        workerId: string;
        executionMs: string; // int64 comes as string in protojson
    };
    workerList?: {
        workers: Worker[];
    };
    payload?: any; 
}

const envApi = (import.meta as any).env?.VITE_API_BASE as string | undefined;
const envWs = (import.meta as any).env?.VITE_WS_BASE as string | undefined;
export const API_KEY = (import.meta as any).env?.VITE_API_KEY as string | undefined;

const inferApiBase = () => {
  if (envApi) return envApi;
  if (typeof window !== "undefined") {
    const { origin } = window.location;
    return origin;
  }
  return "http://localhost:8081";
};

export const API_BASE = inferApiBase();

const inferWsBase = () => {
  if (envWs) return envWs;
  let base = API_BASE;
  try {
    const url = new URL(base);
    url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
    url.pathname = "/api/v1/stream";
    return url.toString();
  } catch {
    return base.replace(/^http/i, "ws") + "/api/v1/stream";
  }
};

export const WS_BASE = inferWsBase();

const authHeaders = () => {
    return API_KEY ? { "X-API-Key": API_KEY } : {};
};

export const apiFetch = (path: string, options: RequestInit = {}) => {
    const headers = {
        ...(options.headers || {}),
        ...authHeaders(),
    } as Record<string, string>;
    return fetch(`${API_BASE}${path}`, { ...options, headers });
};

export const makeWsUrl = () => {
    try {
        const url = new URL(WS_BASE);
        if (API_KEY) {
            url.searchParams.set("api_key", API_KEY);
        }
        return url.toString();
    } catch {
        // Fallback to raw string
        return WS_BASE;
    }
};
