# Redis integration tests

Plain `go test ./... -count=1` does not connect to Redis: integration cases skip
unless `AGENTBUS_TEST_REDIS_URL` explicitly names an isolated disposable broker.
The application settings `REDIS_URL`, `REDIS_HOST`, `REDIS_PORT` and default port
6380 do not opt tests in. An explicitly configured but unreachable broker fails
the tests rather than silently skipping them.

Example with an independently started, isolated Unix-socket Redis:

```sh
AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1
```

The dedicated setting overrides ambient `REDIS_URL` for tests and their child
processes. It is an operator assertion of isolation, not detection of production:
never point it at an existing project's broker. Do not use `docker compose up`
against a shared installation merely to satisfy tests. Test keys are temporary;
cleanup does not make using a production broker acceptable.

The guard regression intentionally includes one skipped subtest to verify that
an ambient production URL cannot opt tests in. With an isolated broker configured,
all other integration tests must run; do not count a run with skipped integration
cases as full validation.

## Validation of the guard (2026-09-20)

- `go build ./...` and `go vet ./...`: passed with a writable temporary
  `GOCACHE`; no buildvcs override was needed.
- With empty `AGENTBUS_TEST_REDIS_URL` and ambient `REDIS_URL` set to
  `redis://production.invalid:6380`, `go test ./... -count=1` passed:
  157 test/subtest pass events, 71 skip events, zero failures, five packages.
  The skips include integration cases and the intentional guard probe.
- The same no-opt-in suite also passed with `-race` (157 pass events, 71 skips).
- With an explicit unavailable Unix endpoint, `TestRequestLifecycle` failed
  with `explicit test Redis unavailable`, rather than skipping or falling back.
- No live broker was used. Shell integration tests were not changed or run.
- Full isolated integration validation was outstanding at that point: starting a
  local server was refused at Unix socket bind (`Operation not permitted`), and
  the local `redis-server` executable identifies as Valkey 9.0.5, not Redis 8.

## Full isolated integration validation (2026-09-20, Claude)

Run on a **fresh disposable** `redis:8-alpine` container (`redis_version:8.6.3`,
empty on start, bound to `127.0.0.1:6392`, removed afterwards) — never the
configured broker on 6380.

| Run | Configuration | Result |
| --- | --- | --- |
| A | no opt-in, ambient `REDIS_URL=redis://production.invalid:6380` | 157 pass, **71 skip**, 0 fail, 5 packages |
| B | `AGENTBUS_TEST_REDIS_URL` = disposable Redis 8, same ambient URL | 227 pass, **1 skip**, 0 fail |
| C | `AGENTBUS_TEST_REDIS_URL=unix:///nonexistent/redis.sock` | **FAIL** — `explicit test Redis unavailable: dial unix /nonexistent/redis.sock: connect: no such file or directory` |
| D | run B with `-race` | 227 pass, **1 skip**, 0 fail |

Counts include subtests. Run A reproduces the earlier 157/71 exactly, which
cross-checks the counting method. In runs B and D the single remaining skip is
`TestAmbientRedisDoesNotOptIn/requires_explicit_endpoint` — the intentional guard
probe, and nothing else. `go build ./...` and `go vet ./...` pass.

Isolation was verified from the other side as well: after the runs the configured
broker on 6380 still listed only its five real projects, and a `SCAN` for
throwaway `t*:cmd` test projects there returned nothing.

What this does **not** establish: that the operator-supplied endpoint is
disposable — the variable is an assertion, not a detection. It also says nothing
about the shell suites, which neither changed nor ran here.

The environment variable is an explicit operator opt-in, not an automatic proof
that the selected server is disposable. Rollback is a Git revert, but restores
unsafe production-default test connections; keep test execution isolated.
