import React from "react";
import ReactDOM from "react-dom/client";
import { HashRouter } from "react-router-dom";
import App from "./App";
import { applyTheme, getTheme } from "./lib/theme";
import "./index.css";

applyTheme(getTheme());

// HashRouter avoids server config hassle — every route lives under /ui/#/...
// so the Go file server only ever needs to serve index.html at /ui/*.
ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <HashRouter>
      <App />
    </HashRouter>
  </React.StrictMode>
);
