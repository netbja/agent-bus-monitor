- **The broker's ACL users, part 3 (broker-hardening step 4b): rotation, status, purge.**
  - **`./setup.sh rotate-redis-password` with `broker.acl` on** rotates each ACL user instead of the
    default user's `requirepass`:
    - as `admin` (from its password file), each user in turn: the new password file first (the
      previous kept as `.prev`), then `ACL SETUSER <user> resetpass ><new>`, then a fresh login as
      that user; `admin` last;
    - then `ACL SAVE`, so `users.acl` in the volume holds the new hashes; no recreate is needed;
    - ping's new password copied into the volume (`/data/ping.pass`) for the healthcheck;
    - the running project services restarted.
    - **A user the broker refuses stops it:** that user and the ones after keep their passwords,
      the ones before stay rotated and are saved. A failed `ACL SAVE` says to run it again before
      the broker restarts.
    - Refused while `REDIS_URL`, `REDIS_PASSWORD` or the `broker.acl` variable is exported;
      admin's file that does not log in changes nothing.
  - **`./setup.sh status`** shows `broker.acl` beside whether the broker's volume holds `users.acl`,
    and warns when they disagree (either way the clients are locked out). A stopped broker is not
    probed.
  - **`./setup.sh uninstall --purge`** sets `broker.acl` back to `off` once the volume is removed: a
    new broker has no `users.acl`. A plain stop keeps both.
