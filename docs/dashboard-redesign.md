# Dashboard redesign

The supplied Nexus screenshot sets the direction: compact navigation, quiet
white surfaces, fine borders, purple/teal data visualizations and clear type.
Both light and dark themes remain available; the existing saved/system theme
preference is preserved.

## References reviewed in a browser

- [Watermelon UI](https://ui.watermelon.sh/home): adapted the existing Capitalio
  shell/card/header components already integrated in this repository.
- [devl screens](https://devl.dev/screens): compact workspace navigation and
  dense operational panels; retained and restyled the existing interactive
  diagnostic components.
- [Bencho](https://bencho.dev/): restrained numeric transitions and interaction
  feedback. No paid source was fetched or copied.
- [Vengeance UI](https://www.vengenceui.com/): subtle hover/press transitions,
  keeping reduced-motion preferences and operational readability.

The redesign is implemented locally with the existing React, Recharts,
Base UI and Motion dependencies. It does not embed third-party websites or
introduce a new component-library dependency.

## Implementation

The shell now uses a slimmer navigation rail, compact search/breadcrumb bar,
and real management API connection state. Shared semantic tokens provide
purple brand accents and coordinated chart colors in both themes. Native
article props (including accessible names) are forwarded by DashboardCard.
All existing data pages inherit the same spacing, borders and typography.

Overview has icon metrics, a two-series traffic chart, ranked agents and recent
failures. Diagnostics remain available below. Agents has a compact summary row
and full-width directory. Tasks retains its filters, drawer and pagination in
a denser table layout. Mobile column toolbars no longer treat their field flex
basis as a 150px height. Tables keep independent keyboard/touch scrolling.

Import-graph auditing removed unreachable Avatar and DropdownMenu primitives,
the obsolete lastVisit helper and the now-unused radial gauge. Removed obsolete
CSS rules for unused panel gradients, sparkline placeholders, avatar stacks,
event attributes and the old shell utilities. Reachable components and status,
authentication, error/retry and pagination fixes were retained.

## Verification

Run `npm run build`, `npm run lint`, `npm test` in dashboard. The fixture browser
script `node scripts/verify-dashboard.mjs fixtures` now includes populated
Overview data as well as Tasks, Agents, Costs, Policy and graph. It checks 320,
390 and 1440px widths, table keyboard/touch scrolling, pagination, graph failure
and recovery. Set VERIFY_THEME=dark to repeat with dark tokens; optionally set
VERIFY_SCREENSHOTS to an absolute directory for viewport screenshots.

Fixture data is confined to Playwright request interception and never appears
in the running dashboard without that verification browser. The regular app
continues to use the actual management API. See dashboard-verification.md for
real example gateway and authentication setup details. Existing build chunk
warnings and ESLint warnings remain outside this visual change.

Final validation: production build passed; ESLint passed with the same eight
pre-existing warnings; all 79 tests passed. Populated fixtures passed in light
and dark mode at 320, 390 and 1440px. An independent browser review exercised
Settings, Policy, Costs, graph, Tasks, drawer and full trace against the real
gateway at desktop/mobile widths, with no runtime errors. A final mobile
refinement keeps small decimal cost totals on one line. Mobile checks are
Chromium emulation rather than physical-device testing.
