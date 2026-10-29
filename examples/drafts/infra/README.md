# Draft: the `infra` pop role

An inert draft (infra-role): Ansible, Terraform/OpenTofu and IT automation, popped by master only
with the person's yes (`approval = "person"`), on a manual-permission profile. Nothing here is in
the manifest until the person adopts it:

```bash
agentbus --project <p> roles adopt infra --ask <ask-id> --from examples/drafts
```

`roles.toml` is the entry, `infra.md` the prompt (copied to `roles/infra.md` at adoption, never over
an existing one). Edit either and the ask's question id changes (`agentbus roles draft` prints it;
adopt names the expected one when it refuses).

**The person approves everything that runs IaC.** Only a short list of exact syntax and lint
invocations runs without a prompt (`fmt -check`, `validate`, `tflint`, `ansible-lint --offline`,
`yamllint .`); everything else prompts in the role's pane, and plans, checks, inits, tests, workspaces, every
`ansible-playbook` run and every change are also ask rules, which keep a prompt even if the profile is loosened (an ask rule refuses
`bypassPermissions`). State and plan files, variable files, vault passwords, cloud and cluster
credentials, and the commands that print them are denied.

These rules match command text, so they are a defence in depth, not a lock: another spelling, a
wrapper or a script can get past a pattern, which is why the profile stays manual and the prompt
asks the person through master before any plan, check or change. `rolecfg/infra_test.go` pins each
class with concrete commands.
