- **`agentbus up` refuses a project folder without Git when missions would run (up-git-repo, 2026-11-16).** The
  missions service needs a Git repository and used to wait for one out of sight (`no Git repository in <dir>: run
  git init there`, seen only in its state). up now checks the template's `working_dir` before starting anything and
  says `run git init <dir>, or agentbus up <p> --no-missions; nothing started`. It never runs `git init` itself. A
  missions service already waiting keeps waiting as before.
