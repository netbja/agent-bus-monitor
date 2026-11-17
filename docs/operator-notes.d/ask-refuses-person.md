- **An ask comes from an agent, an answer from the person.** `agentbus ask <agent>` names the asking
  agent, which must be the caller itself (`AGENT_BUS_AGENT`): another agent's name, registered or
  not, is refused, and so is any name from the person's shell. Also refused: the person's recorded
  name (`identity.as`, unless the caller is an agent of that name) and a name that wrote from
  busmon as a person and holds no agent entry. Nothing is stored, and the refusal says
  `ask <your agent name>; the person is the one who answers`.
  `--withdraw` is not checked, so a stray ask can still be closed. `agentbus answer` is refused
  from an agent (`AGENT_BUS_AGENT`, or inside an agent's terminal), `--as` included: answers are
  typed in busmon or the person's own shell. The dispatcher also skips an entry under the person's
  recorded name that has no terminal (written from the person's own shell).
