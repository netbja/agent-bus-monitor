- **Backups hold the broker's snapshot, `broker.rdb`** (broker-hardening step 5). After the logical dump,
  `agentbus backup` (and the schedule) runs `BGSAVE`, waits (2 min at most) for that save to finish
  `ok`, then copies only the RDB it completed: from the container (`<runtime> cp agent-bus-redis:/data/<dbfilename> -`,
  `broker.runtime`) or, for a native broker, the file itself. It holds the keys with a TTL that the
  logical dump skips. The AOF files are never copied. **The copy must be the connected broker's:** before
  the save, a random marker is written to it under its own key (`agentbus:backup:snapshot-mark:<marker>`,
  created only if absent, 10 min TTL, deleted after by that run alone: an existing value is never
  overwritten or removed, and no snapshot is taken then),
  and a copied file that does not hold it is left out (`REDIS_URL` naming another broker than the local
  container, or another broker's file at the same path); a restored broker holds that key until its TTL
  ends. A save that is refused, fails or does not finish, a data directory not on this host, a file
  older than the save asked for, or one without the marker leaves `broker.rdb` out,
  with the reason in the run's output and the manifest's `left_out`; the archive is then logical only.
  - **Restoring it is manual** (the logical restore stays the everyday one):
    1. stop the broker (`<runtime> stop agent-bus-redis`);
    2. in its data volume, remove the AOF directory (`appendonlydir`) and the old RDB, **keep `users.acl`**
       (a broker with `aclfile` set and the file missing does not start, and without it every user is
       gone), and put `broker.rdb` in as `<dbfilename>` (`dump.rdb`);
    3. start it once with `--appendonly no`, so it loads the RDB rather than an AOF;
    4. run `CONFIG SET appendonly yes` (as `admin`), which writes a fresh AOF from memory;
    5. restart it with its normal config (`./setup.sh install`, or `<runtime> compose up -d redis`).
