# Operations console preview tasks

- [ ] Add navigation, page shells, and design-system CSS.
  - Acceptance: every P0-P2 area has a reachable semantic view.
  - Verify: JavaScript check, browser navigation, 1440 px screenshot.
  - Files: `index.html`, `operations-console.css`.
- [ ] Render overview and exactly 32 SIM slots from live modem data.
  - Acceptance: COM19 appears as a live unassigned device; no fake ICCID/MSISDN is shown.
  - Verify: browser DOM count and live API comparison.
  - Files: `operations-console.js`, `index.html`.
- [ ] Add conversations, maintenance, alerts, reports, and audit preview interactions.
  - Acceptance: all views have meaningful live/empty states; no billable action is enabled.
  - Verify: browser keyboard navigation and console logs.
  - Files: `operations-console.js`, `index.html`, `operations-console.css`.
- [ ] Run the final local preview and prepare the approval handoff.
  - Acceptance: tests/build pass at current HEAD and the preview remains open in Chrome.
  - Verify: `go test -tags nouac ./...`, build, responsive screenshots.
