import { pino, Logger } from "pino";

// Only createLogger is consumed; the executor uses pino directly for any
// step-lifecycle logging it needs.
export function createLogger(opts: { level: string }): Logger {
  return pino({ level: opts.level });
}
