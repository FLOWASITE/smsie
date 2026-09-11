# Spec: Operations console preview

## Objective

Create a local, read-only-first operations console for a 32-slot EC20 modem pool. The operator must be able to understand modem health, slot mapping, message state, maintenance schedules, conversations, alerts, audit history, and reports without triggering calls or SMS during review.

## Tech stack

- Go 1.25 backend and SQLite, unchanged for this preview.
- Existing Bootstrap, jQuery, HTML, CSS, and JavaScript frontend.
- Existing `/api/v1/modems` and `/api/v1/sms` endpoints provide live data.
- No new dependency or frontend framework.

## Commands

- Test: `go test -tags nouac ./...`
- Build: `go build -tags nouac -o smsie.exe .`
- JavaScript check: `node --check web/static/js/app.js` and `node --check web/static/js/operations-console.js`
- Run: `./smsie.exe` with the existing local `config.yaml`

## Project structure

- `web/templates/index.html`: semantic view structure and existing operational modals.
- `web/static/css/operations-console.css`: preview design system and responsive layout.
- `web/static/js/operations-console.js`: live read-only aggregation and preview interactions.
- `web/static/i18n/*.json`: localized labels shared with the existing console.
- `internal/`: unchanged during the UI approval gate.

## Code style

```js
function renderSlotMap(modems) {
    const slots = Array.from({ length: 32 }, (_, index) => ({ slot: index + 1 }));
    // Render only observed modem data; never invent ICCIDs or phone numbers.
    return slots.map(renderSlotRow).join('');
}
```

Use small rendering functions, escaped text, semantic status labels, and existing Bootstrap/jQuery primitives. Keep live data separate from preview-only state.

## Testing strategy

- Existing Go tests protect modem and SMS behavior.
- Static JavaScript syntax and JSON parsing checks run before each commit.
- Real-browser verification covers navigation, desktop and mobile layouts, empty states, keyboard focus, and console errors.
- No external SMS or call is sent as part of UI verification.

## Boundaries

- Always: preserve the working SMS flow, use real modem data when available, show explicit preview labels, and keep schedules disabled.
- Ask first: enable automated maintenance, persist slot mapping, change database schema, or send test traffic.
- Never: invent SIM identities, expose credentials, commit local databases/logs/config, or silently retry billable actions.

## Success criteria

- The default page is a polished Vietnamese overview for the 32-slot pool.
- Navigation exposes Overview, SIM slots, Conversations, Maintenance, Alerts, Reports, Audit, API keys, and Users.
- The slot view renders exactly 32 numbered slots plus unassigned live devices.
- Message views visibly distinguish received, sending, sent, delivered, and failed states.
- Maintenance controls are visibly preview-only and cannot trigger billable work.
- Alerts and reports derive from live modem/SMS data and honest empty states.
- Layout remains usable at 1440 px, 1024 px, 768 px, and 320 px.
- Browser console has no errors.

## Open questions after preview

- Which physical slot should COM19 map to?
- Should maintenance prefer a short call, an SMS, or alternate between both?
- What monthly cost cap and approved destination number should automation use?

