- **`agentbus mem` needs no project and no broker.** It works on files under the repository
  root (`memory/` package: `check`, `new`, `sync`), so its block in `main()` sits above the
  project-required exit, like `limits` and `version`. AGENTS.md carries a generated copy of
  `docs/memory/INDEX.md` between `<!-- memory-index:begin -->` and `<!-- memory-index:end -->`:
  edit the index, run `agentbus mem sync`; `TestRepositoryPasses` fails `go test ./...` on drift.
