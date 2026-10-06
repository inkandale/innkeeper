import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider } from "react-router";
import { router } from "./App";
import "./theme/tokens.css";

// The theme loads after the app's CSS, so its rules win.
const theme = document.createElement("link");
theme.rel = "stylesheet";
theme.href = "/api/theme.css";
document.head.append(theme);

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: (n, err) => n < 3 && !("status" in err && err.status === 401) } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
