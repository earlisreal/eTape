import { Component, type ReactNode } from "react";
import { useTheme } from "./ThemeProvider";

interface Props { label: string; children: ReactNode }
interface State { error: Error | null }

// Functional so it can call useTheme(); the class ErrorBoundary can't call hooks directly.
function ErrorFallback({ label, message, onReload }: { label: string; message: string; onReload: () => void }): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ padding: 12, background: palette.surface, color: palette.danger, border: `1px solid ${palette.danger}`, height: "100%", overflow: "auto" }}>
      <strong>{label} failed</strong>
      <pre style={{ whiteSpace: "pre-wrap", fontSize: 12 }}>{message}</pre>
      <button onClick={onReload}>Reload panel</button>
    </div>
  );
}

// Per-panel boundary: one broken panel never takes down the workspace.
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };
  static getDerivedStateFromError(error: Error): State { return { error }; }

  render(): ReactNode {
    if (this.state.error) {
      return (
        <ErrorFallback
          label={this.props.label}
          message={this.state.error.message}
          onReload={() => this.setState({ error: null })}
        />
      );
    }
    return this.props.children;
  }
}
