package mission

import "fmt"

// State is where a mission stands (spec §4). Rejected is not a state the
// machine returns: a rejection is recorded in the attempt history.
type State string

const (
	Pending     State = "pending"
	Ready       State = "ready"
	Requested   State = "requested"
	Accepted    State = "accepted"
	Delivered   State = "delivered"
	Checking    State = "checking"
	InReview    State = "in_review"
	Approved    State = "approved"
	Integrating State = "integrating"
	Integrated  State = "integrated"
	Blocked     State = "blocked"
	Aborted     State = "aborted"
)

// Event is what happened to a mission.
type Event string

const (
	EvDepsIntegrated   Event = "deps_integrated"
	EvRequestSent      Event = "request_sent"
	EvAccepted         Event = "accepted"
	EvAgentBlocked     Event = "agent_blocked"
	EvDelivered        Event = "delivered"
	EvCheckStarted     Event = "check_started"
	EvCheckFailed      Event = "check_failed"
	EvCheckPassed      Event = "check_passed"
	EvApproved         Event = "approved"
	EvReviewRejected   Event = "review_rejected"
	EvIntegrateStarted Event = "integrate_started"
	EvIntegrated       Event = "integrated"
	EvIntegrateFailed  Event = "integrate_failed"
	EvRetry            Event = "retry"
	EvAbort            Event = "abort"
	EvSkip             Event = "skip"
	EvReassign         Event = "reassign"
	EvTimedOut         Event = "timed_out"
)

// Action is a side effect the conductor performs after a transition, in order.
type Action string

const (
	ActSendRequest   Action = "send_request"
	ActSupersede     Action = "supersede_attempt"
	ActOpenReview    Action = "open_review"
	ActResolveReview Action = "resolve_review"
	ActIntegrate     Action = "integrate"
	ActEscalate      Action = "escalate"
)

// AllStates and AllEvents enumerate the machine (for exhaustive tests).
var (
	AllStates = []State{Pending, Ready, Requested, Accepted, Delivered, Checking, InReview, Approved, Integrating, Integrated, Blocked, Aborted}
	AllEvents = []Event{EvDepsIntegrated, EvRequestSent, EvAccepted, EvAgentBlocked, EvDelivered, EvCheckStarted, EvCheckFailed,
		EvCheckPassed, EvApproved, EvReviewRejected, EvIntegrateStarted, EvIntegrated, EvIntegrateFailed, EvRetry, EvAbort, EvSkip, EvReassign, EvTimedOut}
)

// Ctx carries what a transition depends on besides the state. AttemptsLeft is
// max_attempts minus the rejections of this round, counted after the current
// rejection: with max_attempts 3 the first two rejections retry and the third
// blocks.
type Ctx struct{ AttemptsLeft int }

// Next is the whole transition table. A pair it does not list is refused with
// an error and leaves the state unchanged.
func Next(s State, ev Event, c Ctx) (State, []Action, error) {
	retryOr := func(extra ...Action) (State, []Action, error) {
		if c.AttemptsLeft > 0 {
			return Requested, append(extra, ActSupersede, ActSendRequest), nil
		}
		return Blocked, append(extra, ActEscalate), nil
	}
	switch {
	case ev == EvAbort:
		switch s {
		case Integrated, Aborted:
		case Pending:
			return Aborted, nil, nil
		case InReview:
			return Aborted, []Action{ActSupersede, ActResolveReview}, nil
		default:
			return Aborted, []Action{ActSupersede}, nil
		}
	case s == Pending && ev == EvDepsIntegrated:
		return Ready, nil, nil
	case s == Ready && ev == EvRequestSent:
		return Requested, nil, nil
	case s == Requested && ev == EvAccepted:
		return Accepted, nil, nil
	case (s == Requested || s == Accepted) && ev == EvAgentBlocked:
		return Blocked, []Action{ActEscalate}, nil
	case s == Accepted && ev == EvDelivered:
		return Delivered, nil, nil
	case s == Delivered && ev == EvCheckStarted:
		return Checking, nil, nil
	case s == Checking && ev == EvCheckPassed:
		return InReview, []Action{ActOpenReview}, nil
	case s == Checking && ev == EvCheckFailed:
		return retryOr()
	case s == InReview && ev == EvApproved:
		return Approved, []Action{ActResolveReview}, nil
	case s == InReview && ev == EvReviewRejected:
		return retryOr(ActResolveReview)
	case s == Approved && ev == EvIntegrateStarted:
		return Integrating, nil, nil
	case s == Integrating && ev == EvIntegrated:
		return Integrated, nil, nil
	case s == Integrating && ev == EvIntegrateFailed:
		return Blocked, []Action{ActEscalate}, nil
	case s == Blocked && ev == EvRetry:
		return Requested, []Action{ActSupersede, ActSendRequest}, nil
	case ev == EvSkip:
		switch s {
		case Pending, Ready:
			return Integrated, nil, nil
		case Requested, Accepted, Delivered, Checking, Blocked:
			return Integrated, []Action{ActSupersede}, nil
		case InReview:
			return Integrated, []Action{ActSupersede, ActResolveReview}, nil
		}
	case (s == Accepted || s == Blocked) && ev == EvReassign:
		return Requested, []Action{ActSupersede, ActSendRequest}, nil
	case s == Accepted && ev == EvTimedOut:
		return Blocked, []Action{ActEscalate}, nil
	}
	return s, nil, fmt.Errorf("mission: event %s not allowed in state %s", ev, s)
}
