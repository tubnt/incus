import { Component, type ReactNode } from "react";

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  render() {
    if (this.state.hasError) {
      if (this.props.fallback) return this.props.fallback;

      return (
        <div className="border border-destructive/30 rounded-lg p-6 text-center">
          <h2 className="text-lg font-semibold text-destructive mb-2">
            页面渲染出错
          </h2>
          <p className="text-sm text-muted-foreground mb-4">
            {this.state.error?.message ?? "未知错误"}
          </p>
          <button
            onClick={() => this.setState({ hasError: false, error: null })}
            className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium hover:opacity-90"
          >
            重试
          </button>
        </div>
      );
    }

    return this.props.children;
  }
}
