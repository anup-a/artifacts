{{define "task-setup.md"}}# {{.Task.Title}}

> {{.Task.Summary}}

Part of {{.Share}}/agents.

## Steps

1. Install the CLI (Node 18 or later):

   ```sh
   npm install -g byagent
   ```

   Or run any command without installing: `npx byagent <command>`. Never `npx artifacts`, which is an unrelated package.

2. Get a key. A person signs in at {{.API}}/app/ and creates one at {{.API}}/app/keys. Keys are never created over the API, so if there is no key yet, ask the user for one.

3. Store it without printing it:

   ```sh
   echo "$BYAGENT_KEY" | byagent login --api {{.API}}
   ```

   The CLI also reads `ARTIFACTS_TOKEN` and `ARTIFACTS_API` from the environment, else `~/.artifacts/config.json`.

4. Check it:

   ```sh
   byagent whoami --json
   ```

## Done when

`byagent whoami --json` prints your workspace. Never echo the key into chat or logs.

Next: {{.Share}}/agents/tasks/publish.md
{{end}}
