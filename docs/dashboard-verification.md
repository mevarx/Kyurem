# Dashboard fixes and verification

Verified on 2026-11-16 against checkout `1ba1cdb0e6ffd352719e1f54c1b8a164a3ef81a0`.
No AGENTS.md instructions were present. This checkout is newer than the
investigation commit `c048201fc198e00ebbc374f706ebc44469f852be`.

## Changes

- Agents, graph nodes and the graph inspector share `verificationTone`.
  The actual gateway constants in `gateway/internal/trust/trust.go` are
  verified (green), unverified (negative), unsigned (warning), and unchecked
  (neutral). Unknown and unfamiliar statuses remain neutral.
- Constrained the shell/page sizing, made grid minimums fit narrow containers,
  retained table-local scrolling and exposed thin table scrollbars. Removed
  the nested Tasks scroll region. Policy review actions wrap at narrow widths.
  The newer checkout already contained some shell sizing fixes: the original
  1,126 px document overflow was not reproduced with its example data.
- Graph metadata and recent-error requests distinguish pending, failure and
  successful empty states. Each failed request has its own retry. Metadata
  failures are visible even before a node is selected; the graph stays usable.
- Time-range changes reset task pagination. Successful live responses that
  invalidate the current page trigger a query for the last valid page.
- Shared API errors preserve gateway messages and status codes, distinguish
  transport failures from HTTP failures, and explain management API/proxy/base
  URL checks. Authentication/authorization errors point to credentials and
  permissions. HTTP 500 alone does not claim a definite connection failure.
  Settings stays accessible and does not report a successful connection while
  its initial request is pending.

## Automated checks

From `dashboard/`: `npm ci`, `npm run build`, `npm run lint`, `npm test`.
All succeeded, including 79 tests in 13 files. Focused coverage includes
verification in all three rendered views, graph loading/failure/empty/retry,
pagination narrowing and live shrinking totals, and transport/401/403/500 errors.
Lint reports eight existing warnings. The production build reports circular
charts/graph chunks and a large chunk warning. Installation reports seven
moderate dependency advisories; dependency upgrades were outside this change.

## Browser checks

Used local Chromium through Playwright with Vite on `127.0.0.1:5173`, the
rebuilt gateway using `config/kyurem.example.yaml`, and rebuilt go-echo,
go-upper and demo-client binaries from the README quickstart. Generated direct,
chained and streaming traffic. Checked Tasks, Agents, Policy, Costs and graph
at 390 px and 1440 px with real gateway data.

The reusable `dashboard/scripts/verify-dashboard.mjs` supports these modes:

```powershell
# Run in dashboard/ with Vite running.
node scripts/verify-dashboard.mjs fixtures
node scripts/verify-dashboard.mjs live
# Stop the management API before this mode.
node scripts/verify-dashboard.mjs down
# Start the gateway with KYUREM_MANAGEMENT_AUTH_TOKEN before this mode.
# Optional VERIFY_API_TOKEN also verifies saving valid credentials in Settings.
node scripts/verify-dashboard.mjs auth
```

Fixtures supply six verification outcomes, 51 tasks, priced usage, policy rules,
a populated caller/callee matrix and decision review cards. They also supply
slow, empty and HTTP 500 graph secondary responses, recovery after retry,
a narrower one-task time range, and a live total shrinking from 51 to one.
All five pages fit at 320, 390 and 1440 px. At 390 px, the document width is
390 px and main/page content does not overflow. Wide table regions support
keyboard arrows and synthesized touch swipes; all remain focusable and labelled.

The real gateway-down check used Vite's actual HTTP 500 proxy response.
The authentication check separately used the real gateway requiring a bearer
token: all data routes retained HTTP 401 and credential guidance, Settings
remained accessible, and saving a valid token recovered real agent data.
No browser runtime errors occurred in the verification runs.

Limitations: mobile checks use Chromium viewport/touch emulation, not a physical
phone. Production reverse proxies and ClickHouse were not exercised. Populated
policy/failure/pagination edge cases use the explicitly identified fixtures;
the shipped example runs policy off and cannot readily generate those states.
