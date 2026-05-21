import type { Logger } from "pino";

export interface SilenceWatchOptions {
  timeoutMs: number;
  onTimeout: () => void;
  logger: Logger;
}

// Resets on every byte of stdout AND every MCP tool call.
export class SilenceWatch {
  private timer: NodeJS.Timeout | null = null;
  private readonly timeoutMs: number;
  private readonly onTimeout: () => void;
  private readonly logger: Logger;

  constructor(opts: SilenceWatchOptions) {
    this.timeoutMs = opts.timeoutMs;
    this.onTimeout = opts.onTimeout;
    this.logger = opts.logger;
    this.touch();
  }

  touch(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => {
      this.logger.warn({ timeoutMs: this.timeoutMs }, "silence_timeout");
      this.onTimeout();
    }, this.timeoutMs);
  }

  stop(): void {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }
}
