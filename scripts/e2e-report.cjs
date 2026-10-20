const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const reporter = require('cucumber-html-reporter');

const root = path.resolve(__dirname, '..');
// Keep each run so a later successful run does not replace failure evidence.
const runName = new Date().toISOString().replace(/[:.]/g, '-');
const reportDir = path.join(root, 'reports', 'e2e', runName);
const jsonFile = path.join(reportDir, 'cucumber.json');
const htmlFile = path.join(reportDir, 'index.html');
fs.mkdirSync(reportDir, { recursive: true });

const result = spawnSync('go', ['test', '-count=1', '-v', './tests/e2e', ...process.argv.slice(2)], {
  cwd: root,
  env: {
    ...process.env,
    ANRA_E2E_REPORT: jsonFile,
    ANRA_E2E_ARTIFACTS: path.join(reportDir, 'artifacts'),
  },
  stdio: 'inherit',
});

if (result.error) {
  console.error(`Could not run the E2E suite: ${result.error.message}`);
  process.exit(1);
}
if (!fs.existsSync(jsonFile)) {
  console.error('No scenario report was produced. Check the Go build and test output above.');
  process.exit(result.status || 1);
}

try {
  reporter.generate({
    theme: 'bootstrap',
    jsonFile,
    output: htmlFile,
    reportSuiteAsScenarios: true,
    scenarioTimestamp: true,
    failedSummaryReport: true,
    displayDuration: true,
    launchReport: false,
    name: 'Anra BDD E2E',
    brandTitle: 'Anra acceptance scenarios',
  });
  // A stable file to open; the report itself and its evidence remain per run.
  fs.writeFileSync(path.join(root, 'reports', 'e2e', 'latest.html'),
    `<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=${runName}/index.html"><a href="${runName}/index.html">Open the latest Anra E2E report</a>\n`);
  console.log(`\nHTML report: ${htmlFile}`);
  console.log(`Failure artifacts, when present: ${path.join(reportDir, 'artifacts')}`);
} catch (error) {
  console.error(`Could not generate the HTML report: ${error.message}`);
  process.exit(result.status || 1);
}

// Generate the report even for failing scenarios, while preserving failure.
process.exitCode = result.status ?? 1;
