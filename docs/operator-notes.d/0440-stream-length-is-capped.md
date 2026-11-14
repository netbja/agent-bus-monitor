- **Stream length is capped** at ~1000 entries per stream (XADD MAXLEN ~ in `stream.go`). Older
  entries are trimmed automatically; the busmon backfill on startup replays only the last `--limit`
  of whatever remains (default 25; `--limit 0` for all).
