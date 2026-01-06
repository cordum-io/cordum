import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { App } from "./app/App";
import { loadRuntimeConfig } from "./lib/runtime-config";
import { useConfigStore } from "./state/config";
import "./styles/index.css";
import "reactflow/dist/style.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 20_000,
    },
  },
});

async function bootstrap() {
  const runtime = await loadRuntimeConfig();
  useConfigStore.getState().init(runtime);
  const root = ReactDOM.createRoot(document.getElementById("root") as HTMLElement);
  root.render(
    <React.StrictMode>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </React.StrictMode>
  );
}

bootstrap();
