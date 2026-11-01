package rolecfg

// StateRule is the one rule on an agent's declared state (auto-state spec,
// part 4), written word for word in every role prompt and in the agent-bus
// skill (TestEveryPromptCarriesTheStateRule). The person's answers: waiting is
// lifted by the agent itself, never by a turn; a turn's end sets idle even
// after a declared working (foureyes).
const StateRule = "**Your state.** Declare `working` at the start of each step or fix, `idle` while you wait on " +
	"reviewers or another agent, and `waiting` only when a person must decide. Where your client has turn hooks, your " +
	"state follows your turns: a prompt sets `working` and the turn's end sets `idle`, even after a `working` you " +
	"declared. You still declare `waiting`, `blocked` and `done` yourself, and only you lift `waiting`: declare " +
	"`working` once the person has answered."
