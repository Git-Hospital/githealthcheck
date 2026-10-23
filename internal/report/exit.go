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
	measured := 0
	for _, f := range r.Families {
		if incomplete[f.Reason] || incomplete[f.Score.Reason] {
			return ExitIncomplete
		}
		if f.Measured {
			measured++
		}
	}
	if measured == 0 {
		return ExitIncomplete
	}
	if failUnder != nil && r.Overall.Score.IsNull() {
		return ExitIncomplete
	}
	for _, f := range r.Families {
		for _, fd := range f.Findings {
			if !fd.Suppressed {
				return ExitFindings
			}
		}
	}
	if failUnder != nil && *r.Overall.Score.Value < *failUnder {
		return ExitFindings
	}
	return ExitClean
}
