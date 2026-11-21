- **busmon says why something is unknown (project-switch-ux item 2).** The dispatcher computes the
  health checks, the quota readings and the merge queue.
  - **When it does not run,** busmon names the reason instead of a bare `?` or "not computed yet":
    - "project stopped" when its services were stopped;
    - "dispatch down", "dispatch failed" or "dispatch gave-up" otherwise.
  - **Where it shows:**
    - For you's merge line reads "merge queue unknown: <why>";
    - the header's health summary adds it after `?N`, at 80 columns or more;
    - the quota gauges get one "(<why>)" after them when a provider has no reading at all.
  - **When dispatch runs, or its state is unknown,** the old wording stays.
