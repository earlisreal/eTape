import { Component, type ReactNode } from "react";

interface Props { label: string; children: ReactNode }
interface State { error: Error | null }

// Per-panel boundary: one broken panel never takes down the workspace.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };
  static getDerivedStateFromError(error: Error): State { return { error }; }

  render(): ReactNode {
    if (this.state.error) {
      return (
        <div style={{ padding: 12, background: "#2a1416", color: "#f5b5b5", height: "100%", overflow: "auto" }}>
          <strong>{this.props.label} failed</strong>
          <pre style={{ whiteSpace: "pre-wrap", fontSize: 12 }}>{this.state.error.message}</pre>
          <button onClick={() => this.setState({ error: null })}>Reload panel</button>
        </div>
      );
    }
    return this.props.children;
  }
}
