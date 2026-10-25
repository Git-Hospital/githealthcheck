// Package calibration holds the corpus scores are percentiles against: corpus.txt lists the
// repositories and their commits, and corpus.lock, which githealthcheck calibrate writes from it, what
// was measured on each (docs/calibration.md).
package calibration

import _ "embed"

// Lock is corpus.lock, built into the binary so a scan needs nothing else.
//
//go:embed corpus.lock
var Lock []byte
