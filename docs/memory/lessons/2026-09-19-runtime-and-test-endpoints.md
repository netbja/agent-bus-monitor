---
id: 2026-09-19-runtime-and-test-endpoints
project: agent-bus-monitor
scope: transferable
kind: lesson
status: active
created: 2026-09-19
last_verified: 2026-09-20
author: codex
confidence: verified
---
# Verify the executable and test endpoint actually used

## Finding

A repository revision, a PATH command, a repository-local executable and a running
process can refer to different versions. A successful install into one directory
does not update another copy or an already-running process. An absent presence key
is not proof that no subscriber process exists.

Tests that call the normal application connector can inherit its production
endpoint. Temporary namespaces and cleanup do not make that safe.

## When it applies

Before deployment, record executable paths, hashes and build metadata on each
host, inspect old subscriber processes, and control rearming during a same-agent
cutover. Verify after replacement without assuming all PATH entries resolve alike.
For integration tests use a disposable isolated Redis and an explicit endpoint.
Never substitute the live broker when the isolated one is unavailable.

## Evidence

- [Binary audit and isolated validation](../../coordination-v1-validation.md)
- [Original test connector](https://github.com/netbja/agent-bus-monitor/blob/a2c2fa1/bus/stream_test.go)
- [Connector defaults](https://github.com/netbja/agent-bus-monitor/blob/a2c2fa1/bus/bus.go)

## Limits

At the cited baseline, REDIS_URL is the application's endpoint selection, not a
test-only safeguard. A separate unmerged guard was prepared requiring
AGENTBUS_TEST_REDIS_URL, but must not be assumed installed or merged. Check current
source before choosing the test command. vcs.modified=true prevents certifying a
clean source build solely from its revision field. Do not store credentials or
raw production exports in memory notes.

## Guard follow-up (2026-09-20)

The restored guard now lives in the persistent worktree
`.worktrees/test-isolation`, branch `fix/test-redis-opt-in`. It requires
`AGENTBUS_TEST_REDIS_URL` before test connections, including subscriber child
processes; an ambient `REDIS_URL` cannot opt tests in. See the
[setup and validation record](../../testing-redis.md). Unit/guard checks pass,
and full isolated integration validation was completed on 2026-09-20 against a
fresh disposable Redis 8.6.3 container: with the endpoint configured, the only
remaining skip is the intentional guard probe, and an explicitly configured but
unreachable endpoint fails rather than skipping. Merged as PR #42 and released
in [v0.6.1](../../releases/v0.6.1.md); before that tag, a `go test` run on a host
with the broker up did reach it.
