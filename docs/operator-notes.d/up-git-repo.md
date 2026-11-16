- **A project folder without Git starts without missions (up-git-repo, 2026-11-16).** A project needs no forge and no
  repository (the person's answer). Only the missions service (the conductor) needs Git, and it used to wait for
  one out of sight (`no Git repository in <dir>: run git init there`, seen only in its state). `agentbus up` (and
  busmon's picker, which runs it) now checks the template's `working_dir` and, without a repository, starts the
  project without missions and says once: `missions: not started: no Git repository in <dir> … To enable it: git
  init <dir>, then agentbus up <p>`. A missions service an earlier up left waiting is stopped. It never runs
  `git init` itself.
