package bus

// CurrentApprovers are h's current independent approvers, in the order of
// their first verdict in vs (one subject's, oldest first): the reviewers
// whose latest verdict, self-authored ones included (reviewer-codex), is an
// approve of another's change naming h. deliver and the merge queue count
// with it (single-approval §4.5): a reviewer's stale or repeated approves
// count once, or not at all.
func CurrentApprovers(vs []Verdict, h string) []string {
	var order []string
	latest := map[string]Verdict{}
	for _, v := range vs {
		if _, seen := latest[v.Reviewer]; !seen {
			order = append(order, v.Reviewer)
		}
		latest[v.Reviewer] = v
	}
	var out []string
	for _, r := range order {
		if v := latest[r]; v.Decision == "approve" && v.Reviewer != v.Author && v.Head == h {
			out = append(out, r)
		}
	}
	return out
}
