import { Component, Suspense, type ReactNode } from "react";

type LazySurfaceProps = {
  children: ReactNode;
  loadingText: string;
  errorLabel: string;
  resetKey?: string;
  onDismiss?: () => void;
};

class LazySurfaceBoundary extends Component<LazySurfaceProps, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (this.state.failed) return <div className="v2-module-loading" role="alert">
      <p>{this.props.errorLabel}加载失败。</p>
      <p>请先保存输入，再重新加载页面重试。</p>
      {this.props.onDismiss && <button type="button" onClick={this.props.onDismiss}>关闭</button>}
    </div>;
    return <Suspense fallback={<div className="v2-module-loading" role="status">{this.props.loadingText}</div>}>
      {this.props.children}
    </Suspense>;
  }
}

export function V2LazySurface(props: LazySurfaceProps) {
  return <LazySurfaceBoundary key={props.resetKey} {...props} />;
}
