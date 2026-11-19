package mergequeue

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The CI loop's reading of checks (spec docs/superpowers/specs/2026-11-19-ci-loop-design.md §2):
// a PR's statusCheckRollup, and its base's, classified red, still running or
// not red. The log is never read: an entry keeps its name, its URL and
// whether it is a GitHub Actions run, all cleaned.

// Check is one rollup entry as the loop keeps it.
type Check struct {
	Name, URL string
	Actions   bool // a check run of a GitHub Actions workflow: gh run view --log-failed reads it
	Failed    bool
	Running   bool
	At        time.Time // completion, else start (a check run); start or creation (a status)
}

// ghCheck is a rollup entry as gh pr list (statusCheckRollup) or the base's
// graphql read gives it; fields of both kinds, the absent ones empty.
type ghCheck struct {
	Typename     string `json:"__typename"`
	Name         string `json:"name"`
	Context      string `json:"context"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	State        string `json:"state"`
	DetailsURL   string `json:"detailsUrl"`
	TargetURL    string `json:"targetUrl"`
	StartedAt    string `json:"startedAt"`
	CompletedAt  string `json:"completedAt"`
	CreatedAt    string `json:"createdAt"`
	WorkflowName string `json:"workflowName"`
	CheckSuite   *struct {
		WorkflowRun *struct {
			Workflow *struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// parseChecks keeps the entries of the two known kinds, cleaned.
func parseChecks(raw []ghCheck) []Check {
	out := make([]Check, 0, len(raw))
	for _, g := range raw {
		switch g.Typename {
		case "CheckRun":
			at := parseTime(g.CompletedAt)
			if at.IsZero() {
				at = parseTime(g.StartedAt)
			}
			wf := g.WorkflowName
			if s := g.CheckSuite; wf == "" && s != nil && s.WorkflowRun != nil && s.WorkflowRun.Workflow != nil {
				wf = s.WorkflowRun.Workflow.Name
			}
			done := g.Status == "COMPLETED"
			out = append(out, Check{Name: cleanText(g.Name), URL: cleanText(g.DetailsURL), Actions: wf != "", At: at,
				Running: !done,
				Failed:  done && (g.Conclusion == "FAILURE" || g.Conclusion == "TIMED_OUT" || g.Conclusion == "STARTUP_FAILURE")})
		case "StatusContext":
			at := parseTime(g.StartedAt)
			if at.IsZero() {
				at = parseTime(g.CreatedAt)
			}
			out = append(out, Check{Name: cleanText(g.Context), URL: cleanText(g.TargetURL), At: at,
				Running: g.State == "PENDING" || g.State == "EXPECTED",
				Failed:  g.State == "FAILURE" || g.State == "ERROR"})
		}
	}
	return out
}

// Rollup is a classified rollup: whether an entry still runs, and the
// failing entries sorted by name, the latest of each name only.
type Rollup struct {
	Running bool
	Failing []Check
	// Status is each name's latest entry (the base check compares by name).
	Status map[string]Check
}

// Classify keeps each name's latest entry (a re-run that passed is not red;
// a running one wins over any finished one) and classifies them. Nothing runs and Failing empty is "not red", an empty
// rollup included.
func Classify(cs []Check) Rollup {
	latest := map[string]Check{}
	for _, c := range cs {
		p, ok := latest[c.Name]
		switch {
		case !ok:
			latest[c.Name] = c
		case c.Running != p.Running:
			// A re-run still queued may have no time yet: a running entry is
			// never beaten by a finished one of its name (foureyes).
			if c.Running {
				latest[c.Name] = c
			}
		case !c.At.Before(p.At):
			latest[c.Name] = c
		}
	}
	r := Rollup{Status: latest, Failing: []Check{}}
	for _, c := range latest {
		r.Running = r.Running || c.Running
		if c.Failed {
			r.Failing = append(r.Failing, c)
		}
	}
	sort.Slice(r.Failing, func(i, j int) bool { return r.Failing[i].Name < r.Failing[j].Name })
	return r
}

// baseQuery reads the rollup of a branch's latest commit, one page of 100
// entries; a longer rollup is a cut answer (ci-loop §4, "The base").
const baseQuery = `query($owner:String!,$name:String!,$ref:String!){repository(owner:$owner,name:$name){ref(qualifiedName:$ref){target{... on Commit{oid statusCheckRollup{contexts(first:100){pageInfo{hasNextPage} nodes{__typename ... on CheckRun{name status conclusion detailsUrl startedAt completedAt checkSuite{workflowRun{workflow{name}}}} ... on StatusContext{context state targetUrl createdAt}}}}}}}}}`

// Base is a base branch's latest commit and its classified rollup.
type Base struct {
	Head   string
	Rollup Rollup
}

// ReadBaseRollup reads the rollup of branch's latest commit in the
// repository of dir with gh api graphql (gh fills {owner} and {repo} from
// the repository), run like ReadGitHub. Every failure is an error: a base
// that cannot be read is never "not red".
func ReadBaseRollup(ctx context.Context, dir string, env []string, branch string) (Base, error) {
	out, err := run(ctx, dir, env, "gh", "api", "graphql", "-f", "query="+baseQuery,
		"-F", "owner={owner}", "-F", "name={repo}", "-f", "ref=refs/heads/"+branch)
	if err != nil {
		return Base{}, err
	}
	var raw struct {
		Errors []json.RawMessage `json:"errors"`
		Data   *struct {
			Repository *struct {
				Ref *struct {
					Target *struct {
						Oid    string `json:"oid"`
						Rollup *struct {
							Contexts struct {
								PageInfo struct {
									HasNextPage bool `json:"hasNextPage"`
								} `json:"pageInfo"`
								Nodes []ghCheck `json:"nodes"`
							} `json:"contexts"`
						} `json:"statusCheckRollup"`
					} `json:"target"`
				} `json:"ref"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Base{}, errors.New("gh answered something that is not graphql's JSON")
	}
	if len(raw.Errors) > 0 || raw.Data == nil || raw.Data.Repository == nil || raw.Data.Repository.Ref == nil || raw.Data.Repository.Ref.Target == nil {
		return Base{}, errors.New("the base's latest commit is not in gh's answer")
	}
	t := raw.Data.Repository.Ref.Target
	if !bus.ValidHead(t.Oid) {
		return Base{}, errors.New("gh named the base's commit with no valid sha")
	}
	var nodes []ghCheck
	if t.Rollup != nil {
		if t.Rollup.Contexts.PageInfo.HasNextPage {
			return Base{}, errors.New("the base's checks may be cut (more than 100)")
		}
		nodes = t.Rollup.Contexts.Nodes
	}
	return Base{Head: t.Oid, Rollup: Classify(parseChecks(nodes))}, nil
}
