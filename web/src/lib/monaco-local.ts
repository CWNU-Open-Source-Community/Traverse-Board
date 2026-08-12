import { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/editor/editor.worker.js?worker";
import jsonWorker from "monaco-editor/language/json/json.worker.js?worker";
import cssWorker from "monaco-editor/language/css/css.worker.js?worker";
import htmlWorker from "monaco-editor/language/html/html.worker.js?worker";
import tsWorker from "monaco-editor/language/typescript/ts.worker.js?worker";

let configured = false;

// Monaco is loaded only from content-hashed local assets. There is no CDN
// fallback; the UI security policy also limits workers and scripts to self.
export function configureLocalMonaco(): void {
  if (configured) return;
  const runtime = self as typeof self & { MonacoEnvironment: monaco.Environment };
  runtime.MonacoEnvironment = {
    getWorker(_moduleID: string, label: string): Worker {
      if (label === "json") return new jsonWorker();
      if (label === "css" || label === "scss" || label === "less") return new cssWorker();
      if (label === "html" || label === "handlebars" || label === "razor") return new htmlWorker();
      if (label === "typescript" || label === "javascript") return new tsWorker();
      return new editorWorker();
    },
  };
  loader.config({ monaco });
  configured = true;
}
