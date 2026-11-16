- **A swap outside a role's profiles no longer costs its session on resume
  (resume-record-profile).** `agentbus swap --force` may put a role on any defined profile, and its
  SessionStart hook records that profile in `{p}:resume`; `up --resume`, the self-repair and `holder
  drain` used to refuse such a record (the role started fresh, or was not moved) (master lost its context on 2026-11-16). When
  roles.toml defines that profile for the role on the role's own client, the session now resumes, on
  the role's own profile (`rolecfg.ResumeProfile`, which the holder applies to every resume start): the recorded profile is a broker key and still never picks what runs, and
  up says so. A recorded profile of another client is still refused (its session is not this
  client's), as is one roles.toml no longer defines.
