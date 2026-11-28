- **The automatic update, step 4d: the updater's loop (quiet-update-auto §4.1, §4.2).**
  `agentbus service run updater` is the updater service's process, one per host, no project: every
  5 minutes it reads `updates.auto` and `updates.window` afresh (a change needs no restart) and the
  state file `auto.json`. With `notify` it runs nothing. Inside the window, with no pass finished in
  this occurrence of it, it runs `<bindir>/agentbus update --auto`; outside it, with the last check
  more than 24 hours old, `update --auto --check`, so `updates.max_wait` is counted on a host never
  awake in the window. A window that crosses midnight belongs to the day it starts; a pass or check
  dated in the future counts as none. Each pass is a child in its own process group: stopping the
  loop never kills a pass in flight. A developer build has no updater (it exits 0). Nothing starts
  the loop yet: its unit, written by `agentbus up` and `setup install`, comes with step 4e. Master's
  installed notice now also goes to each project left on the old build (in work at its restart), not
  only to the projects restarted.
