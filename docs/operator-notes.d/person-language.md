- **`person.language` (env `AGENT_BUS_PERSON_LANGUAGE`, default `en`) is read by master's role
  skill only, through `agentbus config`.** Master writes to the person in it (reports to the
  person, ask question text, its pane) and to every agent in English; ask choice labels, peers
  answering the person, and the dispatcher's notices stay English. No code translates anything.
  A value that is not an ISO 639-1 code shows as `invalid` in `agentbus config`, warned
  `master writes English`, and resolves to `en`. A change reaches
  master at its next boot.
