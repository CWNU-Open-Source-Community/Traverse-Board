import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import { initializePrayuAppearance } from "./lib/appearance";
import { installDesktopNavigationGuard } from "./lib/desktop-navigation";
import { initializePrayuLocale, LocaleProvider } from "./lib/locale";
import "./styles.css";

installDesktopNavigationGuard();
initializePrayuAppearance();
initializePrayuLocale();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 5_000,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <LocaleProvider><App /></LocaleProvider>
    </QueryClientProvider>
  </StrictMode>,
);
