# askhuman

Disposable local HTTP questionnaire generator and server (`templ`) for structured human-in-the-loop requirement elicitation and architectural arbitration.

Instead of dumping walls of open-ended questions into a chat thread, coding agents generate a strongly-typed JSON specification (`spec.json`), validate it mechanically, spin up a single-use local web UI (`askhuman serve -once`), and hand the user a single clickable `http://127.0.0.1:<port>` link. When the user submits the form, `askhuman` atomically writes both machine-readable JSON answers (`answers.json`) and a structured Markdown decision record (`decisions.md`), then exits with code `0`.

---

## Installation

```sh
go install github.com/hazyhaar/askhuman/cmd/askhuman@latest
```

---

## Drop-in Instructions for `CLAUDE.md` / `AGENTS.md`

Copy and paste the block below into your project's `CLAUDE.md` or `AGENTS.md` so AI coding agents invoke `askhuman` reliably and without schema errors:

```markdown
## Human-in-the-Loop Arbitration Protocol (`askhuman`)

Whenever requirement elicitation, scope clarification, or an architectural trade-off requires user input:
1. **Never ask a list of questions in plain text in the chat.** Always formalize a structured questionnaire JSON file (`spec.json`) and serve it through `askhuman`.
2. **Combine the three question kinds appropriately**:
   - `"qcm_free_text"`: Single-choice (`"multi_select": false`) or multi-choice (`"multi_select": true`) options (`>= 2` options required), with `"recommended": true` and `"default_checked": true` on the recommended option, built-in click-to-deselect ("none of the above" / `explicit_none`), plus an optional free-text amendment field.
   - `"boolean_checklist"`: Yes/No assertion checklists (`>= 1` option required) for validating invariants, constraints, or scope boundaries.
   - `"free_text"`: Open-ended text input for additional user directives.
3. **Execute the 3-step `askhuman` workflow**:
   - Validate the specification first (strict `DisallowUnknownFields` parser):
     `askhuman validate-spec -in spec.json`
   - Start the disposable single-submission HTTP server as a background/daemon task:
     `askhuman serve -once -in spec.json -out-json answers.json -out-md decisions.md`
   - Capture the `http://127.0.0.1:<port>` URL printed on `stdout` (`askhuman listening on http://127.0.0.1:<port>`) and present **only that clickable link** to the user without repeating the questions in the chat.
4. **Resume automatically on exit**:
   - As soon as the user submits the form in the browser, `askhuman serve -once` writes `answers.json` and `decisions.md` atomically and exits with code `0`. Read `answers.json` / `decisions.md` and proceed.
```

---

## Complete `spec.json` Reference Example

The specification parser enforces `json.Decoder.DisallowUnknownFields()`. Every field shown below is supported:

```json
{
  "project": "acme-api",
  "workflow_id": "wf-design",
  "workstep_id": "ws-arbitration",
  "title": "Storage & Rate-Limiting Arbitration",
  "subtitle": "Validate core invariants before implementation",
  "context_summary": "The service ingests webhook events and persists audit logs.",
  "prerequisites": [
    "docs/architecture.md"
  ],
  "sections": [
    {
      "id": "sec-storage",
      "title": "1. Storage Engine & Durability",
      "description": "Select the persistence backend and durability guarantees."
    }
  ],
  "questions": [
    {
      "id": "q-backend",
      "section_id": "sec-storage",
      "axis": "Storage Backend",
      "kind": "qcm_free_text",
      "title": "Which storage backend should be used for the audit log?",
      "context": "Must support single-binary deployment and concurrent readers.",
      "required": true,
      "multi_select": false,
      "options": [
        {
          "id": "opt-sqlite-wal",
          "label": "(Recommended) SQLite in WAL mode with NORMAL synchronous pragma",
          "rationale": "Zero external daemon, high read concurrency, atomic crash recovery.",
          "recommended": true,
          "default_checked": true
        },
        {
          "id": "opt-jsonl",
          "label": "Append-only JSONL file with O_APPEND",
          "rationale": "Simpler write path but requires full scan for indexed queries."
        }
      ],
      "free_text_label": "Optional amendment or custom pragma settings",
      "free_text_placeholder": "Specify any custom retention or pragma constraints..."
    },
    {
      "id": "q-invariants",
      "section_id": "sec-storage",
      "axis": "Safety Invariants",
      "kind": "boolean_checklist",
      "title": "Confirm the mandatory safety gates:",
      "required": true,
      "options": [
        {
          "id": "chk-disallow-unknown",
          "label": "Reject unknown JSON fields on all incoming webhook payloads",
          "recommended": true,
          "default_checked": true
        },
        {
          "id": "chk-rate-limit",
          "label": "Enforce per-client token-bucket rate limiting (<= 10 req/s)",
          "recommended": true,
          "default_checked": true
        }
      ]
    },
    {
      "id": "q-notes",
      "section_id": "sec-storage",
      "axis": "Additional Directives",
      "kind": "free_text",
      "title": "Any additional constraints or edge cases to cover?",
      "required": false,
      "max_free_text_bytes": 16384
    }
  ]
}
```

---

## CLI Reference

```sh
# 1. Validate a questionnaire specification (exits 0 if valid, 2 on schema error)
askhuman validate-spec -in spec.json

# 2. Render a standalone HTML5 file
askhuman render -in spec.json -out questionnaire.html [-endpoint /submit]

# 3. Serve a disposable local HTTP UI and exit upon first valid submission
askhuman serve -once -in spec.json [-addr 127.0.0.1:0] [-out-json answers.json] [-out-md decisions.md] [-timeout 60m]

# 4. Format an existing JSON submission into a Markdown report
askhuman format-md -spec spec.json -answers answers.json -out decisions.md
```

## License

MIT
