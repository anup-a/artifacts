{{define "task-setup.md"}}# {{.Task.Title}}

> {{.Task.Summary}}

Part of {{.Share}}/agents.

## Steps

1. Install the CLI (Node 18 or later):

   ```sh
   npm install -g byagent
   ```

   Or run any command without installing: `npx byagent <command>`. Never `npx artifacts`, which is an unrelated package.

2. Connect. Never ask the user to paste a key into the chat. One of:

   - The user pastes a prompt from {{.API}}/app/connect. Run the line in it:

     ```sh
     npx -y byagent@latest login --code K7QD-M3XR --json
     ```

   - Or start it yourself. **Stop after the first command**: your next message to the user is its `action_required.message`, word for word, before any other work. The code expires in 10 minutes.

     ```sh
     byagent login --browser --no-wait --json
     byagent login --wait --json      # waits up to 90 s; exit 3 PENDING: run again, EXPIRED: start over
     ```

   Either way the CLI saves a key of its own, named after the agent, to `~/.artifacts/config.json`. A signed-in person approved it; the key itself never passes through the chat. Codes work once and expire in 10 minutes.

   No account and just trying it? Skip to the next page: `byagent publish` with nothing configured gets a guest key and saves it. Guest pages are public, three at most, and stop working after 24 hours. The JSON has `claim_url`: the user opens it, signs in and keeps the pages. Never publish anything private as a guest.

3. For CI or a script, a person creates a key at {{.API}}/app/keys instead, and you store it without printing it:

   ```sh
   echo "$BYAGENT_KEY" | byagent login --api {{.API}}
   ```

   The CLI also reads `ARTIFACTS_TOKEN` and `ARTIFACTS_API` from the environment.

4. Check it:

   ```sh
   byagent whoami --json
   ```

## Done when

`byagent whoami --json` prints your workspace. Never echo the key into chat or logs.

Next: {{.Share}}/agents/tasks/publish.md
{{end}}
