import { Component, type ErrorInfo, type ReactNode } from "react";

type Props = { children: ReactNode; resetKey: string };
type State = { error?: Error };

export class PageErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("page render failed", error, info);
  }

  componentDidUpdate(previous: Props) {
    if (this.state.error && previous.resetKey !== this.props.resetKey) {
      this.setState({ error: undefined });
    }
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <section className="rounded-xl border border-rose-400/20 bg-rose-400/[0.06] p-6">
        <h1 className="text-xl font-semibold text-slate-100">這個頁面剛剛發生錯誤</h1>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-slate-300">
          其他功能與背景計算不受影響。你可以重新載入目前頁面，或從左側選單前往其他功能。
        </p>
        <button
          className="mt-4 min-h-10 rounded-lg bg-teal-400 px-4 text-sm font-semibold text-slate-950 hover:bg-teal-300"
          onClick={() => window.location.reload()}
        >
          重新載入目前頁面
        </button>
      </section>
    );
  }
}
