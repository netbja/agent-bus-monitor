## What and why

<!-- One subject per pull request. What changes, and why. Link the issue, spec or decision. -->

## How it was tested

<!-- Commands run and their result. -->

- [ ] `go build ./... && go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] Redis integration cases run against an **isolated, disposable** broker
      (`AGENTBUS_TEST_REDIS_URL=…`), never the production one, or not applicable
- [ ] `go run ./cmd/agentbus mem check`
- [ ] Shell suites in `tests/` run (with `docker`, `herdr` and the service managers stubbed), or not applicable

## Checklist

- [ ] Protocol changes are in the `bus` package, not in the binaries
- [ ] Every test changed to bless new behaviour is named here, with the reason
- [ ] `CHANGELOG.md` has a line under `## Unreleased` for a user-visible change
- [ ] Shared memory: a durable decision, pitfall or open question is recorded in `docs/memory/`
      (and `agentbus mem sync` run after editing the index), or there is none
- [ ] No secret, broker password, token or personal data in the diff

## 4-eyes review

<!-- Reviewer (not the author). On the project's own team: the verdict on the bus,
     `agentbus verdicts --pr N` must read APPROVED, with `--head <sha>` for a delivery. -->
