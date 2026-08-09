import { ReactNode, Component } from 'react';

interface State {
  hasError: boolean;
  message: string;
}

// 捕获子组件渲染异常，避免整页白屏。
export default class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { hasError: false, message: '' };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, message: error.message };
  }

  componentDidCatch(error: Error): void {
    console.error('Page error:', error);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="main">
          <div className="error-box">
            <strong>页面出错了：</strong>
            <br />
            {this.state.message}
          </div>
          <button className="primary" onClick={() => { this.setState({ hasError: false, message: '' }); window.location.reload(); }}>
            刷新重试
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}
