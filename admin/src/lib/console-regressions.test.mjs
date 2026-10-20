import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const shell = readFileSync(
  new URL("../components/shell/app-shell.tsx", import.meta.url),
  "utf8",
);
const setup = readFileSync(
  new URL("../app/setup/page.tsx", import.meta.url),
  "utf8",
);

const header = shell.slice(shell.indexOf("<header"), shell.indexOf("</header>"));

test("the phone header reflows instead of colliding in one row", () => {
  // Two columns on the base breakpoint: brand and Menu share the first row,
  // the environment and account controls take the second one.
  assert.match(header, /grid-cols-\[minmax\(0,1fr\)_auto\]/);
  assert.match(header, /col-span-2[^"\n]*sm:col-span-1/);
  // From 640px the three controls share one row with the brand again.
  assert.match(header, /sm:grid-cols-\[minmax\(0,1fr\)_auto_auto\]/);
  // Desktop keeps the flush single-row shell and the horizontal section nav.
  assert.match(header, /lg:flex/);
  assert.match(header, /hidden h-full items-center px-4 lg:flex/);
});

test("mobile controls keep 44px targets and desktop stays dense", () => {
  assert.match(header, /min-h-11 items-center gap-2/); // brand link
  assert.match(shell, /h-11 px-3 text-\[13px\] lg:hidden/); // Menu
  assert.match(
    shell,
    /h-11 rounded-\[3px\] px-2 text-\[12px\] font-medium transition-colors lg:h-6/,
  ); // environment choice
  assert.match(shell, /h-11 w-11 gap-2 px-0 lg:h-8 lg:w-auto/); // account trigger
});

test("the account email appears only where the trigger expands", () => {
  assert.match(shell, /hidden[^"\n]*lg:inline/);
  assert.doesNotMatch(shell, /sm:inline/);
  assert.match(shell, /aria-label="Account menu"/);
});

test("the shell keeps its destinations, labels and environment washes", () => {
  assert.match(shell, />\s*Menu\s*</);
  for (const href of [
    "/",
    "/transactions",
    "/webhooks",
    "/merchants",
    "/routing",
    "/providers",
    "/api-keys",
    "/users",
    "/audit",
  ]) {
    assert.ok(shell.includes(`href: "${href}"`), `missing destination ${href}`);
  }
  assert.match(header, /bg-production-wash/);
  assert.match(header, /bg-sandbox-wash/);
});

test("the setup page owns the unload warning, not the merchant form", () => {
  const page = setup.slice(
    setup.indexOf("export default function SetupPage"),
    setup.indexOf("function StepCard"),
  );
  const form = setup.slice(
    setup.indexOf("function MerchantForm"),
    setup.indexOf("function DoneStep"),
  );
  assert.match(
    page,
    /const \[secretsAcknowledged, setSecretsAcknowledged\] = useState\(false\)/,
  );
  assert.match(page, /hasUnacknowledgedSecrets\(merchantSetup, secretsAcknowledged\)/);
  assert.match(page, /addEventListener\("beforeunload"/);
  assert.match(page, /removeEventListener\("beforeunload"/);
  // The form may unmount (Back) or remount (reopen after Skip) at any time.
  assert.doesNotMatch(form, /beforeunload/);
  assert.doesNotMatch(form, /secretsAcknowledged/);
  // Acknowledgement is its own flag, not a reading of the skipped/complete summary.
  assert.match(page, /if \(value !== "skipped"\) setSecretsAcknowledged\(true\)/);
  assert.doesNotMatch(page, /secretsAcknowledged = (merchant|fill)/);
});

test("setup secrets stay in memory", () => {
  assert.doesNotMatch(setup, /localStorage|sessionStorage|document\.cookie/);
});
