import { Component } from "react";
import type { ErrorInfo, ReactNode } from "react";
import { Result, Button, Typography } from "antd";

const { Paragraph, Text } = Typography;

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
  errorInfo: ErrorInfo | null;
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, error: null, errorInfo: null };
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    this.setState({ errorInfo });
    console.error("ErrorBoundary caught:", error, errorInfo);
  }

  handleReset = () => {
    this.setState({ hasError: false, error: null, errorInfo: null });
  };

  render() {
    if (this.state.hasError) {
      const isDev = import.meta.env.DEV;

      return (
        <Result
          status="error"
          title="Something went wrong"
          subTitle={this.state.error?.message ?? "An unexpected error occurred."}
          extra={[
            <Button key="retry" type="primary" onClick={this.handleReset}>
              Retry
            </Button>,
          ]}
        >
          {isDev && this.state.error && (
            <div style={{ textAlign: "left" }}>
              <Paragraph>
                <Text strong style={{ fontSize: 16 }}>
                  Error Details (development only):
                </Text>
              </Paragraph>
              <Paragraph>
                <Text code style={{ fontSize: 12 }}>
                  {this.state.error.toString()}
                </Text>
              </Paragraph>
              {this.state.errorInfo?.componentStack && (
                <Paragraph>
                  <Text strong>Component Stack:</Text>
                  <pre
                    style={{
                      fontSize: 11,
                      maxHeight: 300,
                      overflow: "auto",
                      background: "rgba(0,0,0,0.3)",
                      padding: 12,
                      borderRadius: 6,
                      marginTop: 8,
                      whiteSpace: "pre-wrap",
                      wordBreak: "break-word",
                    }}
                  >
                    {this.state.errorInfo.componentStack}
                  </pre>
                </Paragraph>
              )}
            </div>
          )}
        </Result>
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
