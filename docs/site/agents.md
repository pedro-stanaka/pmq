# For AI Agents

PMQ is designed to be operated by coding agents: predictable commands, consistent JSON errors, and same-origin APIs mean an agent can build, deploy, and verify an application without human configuration work. Two ready-made agent skills ship in the repository.

## pmq-app-builder — build an app

[`skills/pmq-app-builder`](https://github.com/pedro-stanaka/pmq/blob/main/skills/pmq-app-builder/SKILL.md) teaches an agent to build a static frontend for PMQ: vanilla JavaScript and React/Vite/TypeScript starters, the same-origin document/storage/AI API rules, a validation checklist, and an independent end-to-end example. Its acceptance test runs offline:

```sh
./skills/pmq-app-builder/scripts/acceptance.sh
```

## pmq-deploy — deploy and operate

[`skills/pmq-deploy`](https://github.com/pedro-stanaka/pmq/blob/main/skills/pmq-deploy/SKILL.md) teaches an agent to build, deploy, verify, roll back, and troubleshoot apps with the PMQ CLI against an existing authenticated installation — including token hygiene, release checks, and end-to-end verification through the real proxy.

## Install a skill into your agent

Copy the `SKILL.md` (and, for the app-builder, the `assets/`, `references/`, `agents/` directories it references) into your coding agent's skills directory, or point the agent at the raw files in the repository. For example, for an agent that reads AGENTS.md-style instructions:

```text
Read and follow skills/pmq-app-builder/SKILL.md when asked to create
or change an app deployed on PMQ.
```

## Example prompts

- “Build a small notes app with file attachments for PMQ, following the pmq-app-builder skill, and deploy it as `notes`.”
- “Deploy `./dist` to PMQ as `hello-world`, verify the app and its document API through the proxy, then roll back if verification fails.”
- “My PMQ app returns 401 on writes — diagnose using the pmq-deploy skill's troubleshooting rules.”

## API contract agents can rely on

- Errors are always `{"error":{"code":"...","message":"..."}}` with meaningful HTTP status codes.
- All app APIs are relative (`/_pmq/db/*`, `/_pmq/storage/*`, `/_pmq/ai/*`) — no server hostname needed in app code.
- Deployment replaces the whole active file set atomically; documents and objects survive.
- The [HTTP API reference](/reference/api) is the canonical contract; planned features are never documented as shipped.
