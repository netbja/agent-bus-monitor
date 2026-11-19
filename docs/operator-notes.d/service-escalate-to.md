- **The dispatch service's escalation target reaches every check (service-escalate-to).** Under
  `agentbus service run dispatch --escalate-to <agent>` (or `AGENT_BUS_ESCALATE_TO` in the project's env
  file), the stall checks (team-stall notice, clear-stall, review-queue, idle-implementer, plan-drift,
  idle-master), the self-repair watcher and the reboot judge now escalate to that agent, as the loop
  did; before, they still went to master. The foreground `agentbus dispatch --escalate-to` was not affected.
