package report

// Exit codes, the same contract as Helmbulance. Precedence: Error > Incomplete > Findings > Clean.
const (
	ExitClean      = 0
	ExitFindings   = 1
	ExitError      = 2
	ExitIncomplete = 3
)

// ExitCode decides the exit code of a finished scan. failUnder is nil when --fail-under was not
// given. Errors (ExitError) are decided by the caller, which never gets a report in that case.
func ExitCode(r *Report, failUnder *float64) int {
	switch {
	case incompleteRun(r):
		return ExitIncomplete
	case failUnder != nil && r.Overall.Score.IsNull():
		return ExitIncomplete
	case hasFindings(r):
		return ExitFindings
	case failUnder != nil && *r.Overall.Score.Value < *failUnder:
		return ExitFindings
	}
	return ExitClean
}

// incompleteRun reports a run in which a family or a metric did not finish, or no family ran.
func incompleteRun(r *Report) bool {
	if incomplete[r.Overall.Score.Reason] {
		return true
	}
	ran := false
	for _, f := range r.Families {
		if incomplete[f.Reason] || incomplete[f.Score.Reason] {
			return true
		}
		// A family can be measured while one of its metrics crashed or timed out; the run is then
		// still incomplete.
		for _, m := range f.Metrics {
			if incomplete[m.Reason] {
				return true
			}
		}
		// A family that read too little to measure is a finished run of it, not an incomplete one.
		ran = ran || f.Measured || f.Reason == InsufficientInput
	}
	return !ran
}

func hasFindings(r *Report) bool {
	for _, f := range r.Families {
		for _, fd := range f.Findings {
			if !fd.Suppressed {
				return true
			}
		}
	}
	return false
}
