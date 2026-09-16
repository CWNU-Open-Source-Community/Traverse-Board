import { ConnectionGate } from "./components/connection-gate";
import { useConnectionStore } from "./state/connection";
import { V2WorkbenchEntry } from "./v2";

// Both task views share one shell and one connection lifetime. Legacy resource
// addresses are resolved by its navigation adapter, not by a second app tree.
export default function App() {
  const token = useConnectionStore((state) => state.token);
  return token ? <V2WorkbenchEntry /> : <ConnectionGate />;
}