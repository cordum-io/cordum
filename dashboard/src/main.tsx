import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { loadRuntimeConfig } from "./lib/runtime-config";
import "./styles/index.css";

loadRuntimeConfig().then(() => {
  ReactDOM.createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  );
});
