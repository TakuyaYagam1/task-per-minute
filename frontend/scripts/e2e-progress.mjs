import { dirname, isAbsolute, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const frontendRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// Keep progress useful even if the job stops before the private JSON report is complete.
export default class ProgressReporter {
  total = 0;
  completed = 0;

  onBegin(_config, suite) {
    this.total = suite.allTests().length;
  }

  location(test) {
    const file = relative(frontendRoot, test.location.file);
    if (!file || file.startsWith("..") || isAbsolute(file)) return "unknown test";
    return `${file}:${test.location.line}`;
  }

  onTestBegin(test, result) {
    process.stderr.write(`e2e worker ${result.workerIndex}: ${this.location(test)} started\n`);
  }

  onTestEnd(test, result) {
    this.completed += 1;
    process.stderr.write(
      `e2e ${this.completed}/${this.total}: ${this.location(test)} ${result.status} (${Math.round(result.duration)}ms)\n`,
    );
  }
}
