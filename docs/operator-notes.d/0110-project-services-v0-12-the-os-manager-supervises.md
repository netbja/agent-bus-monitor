- **Project services (v0.12): the OS manager supervises, nothing of ours does.** `service/`
  writes systemd `--user` units / launchd plists (enabled at login only while `services.start_at_login` is on), the 0600 env file the
  services load, the rotating log and the restart budget. Every unit runs `agentbus --project P
  service run <kind>` (`cmd/agentbus/servicerun.go`), whose exit code *is* the restart rule: 0 is
  final (stopped, project gone, another instance, invalid config, budget spent), 1 restarts. Its
  state goes to `{p}:services` through an `EXISTS`-guarded script (`bus/services.go`); without the
  `installed` marker that `up` writes, a service exits and writes nothing, so a deleted project is
  never resurrected. `up` writes that marker only once the broker answers PING (`waitBroker`,
  up-broker-ready: a 10 s deadline on the clock that counts the pings' own time, each ping through
  `bus.ConnectContext` given at most 2 s and never more than is left, 250 ms between them;
  retrying only LOADING and a refused or reset connection (or EOF): a timeout or another dial
  error fails at once, since a starting local container refuses rather than black-holes; a give-up
  cut short by the budget names the broker's last answer, not the deadline). Don't add a supervisor loop, a `nohup` fallback, or a login autostart other than the
  systemd/launchd option decided on 2026-11-09 (off by default, no linger, not built yet:
  `decisions/2026-11-09-self-repair-pane-writer.md`); don't
  put a secret in a unit; tests use fake `systemctl`/`launchctl` (`tests/installer_test.sh`,
  `service/manager_test.go`) and never the user's manager. busmon only reads the state and the
  local log (`F3`), never starts or parents a service. F3 (busmon-f3, `services_cards.go`) is three cards,
  side by side at ≥110 cells: dispatch and missions (state, pid and since from `/proc`, summary, last
  error, log size of `service.LogMax`, `LogKeep` kept) and Redis (where it runs: `docker inspect
  agent-bus-redis` only when docker is on PATH, else the process from INFO; version, uptime, memory,
  `DBSIZE` keys via `Bus.RedisInfo`), all gathered in a command (`gatherServices`, the records read
  fresh through `ServiceStates`), unknown read `?`; while the Services or Redis page is the top page it
  is read again every `svcRefreshEvery` (5 s, busmon-f3-live: each result schedules the next read, so
  reads never overlap, a page under another keeps its chain without reading, a closed one ends it); ↑↓ choose a service card, Enter or `l` opens its log tail
  (`ovServiceLog`, `serviceLogBody`); the Redis card is selectable too, and Enter opens its read-only page
  (`redis_page.go`, `ovRedis`, busmon-redis-detail: `Bus.RedisDetail` = INFO memory/persistence/clients and
  per project its keys and stream lengths, `docker port` for the loopback bind, `bus.LegacyPasswordInUse`,
  a `docker logs --tail 20` bounded to 8 KiB; unknown read `?`); `busmon --delete` removes the local
  units before the keys.
