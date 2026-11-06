//go:build race

package ledger

// raceEnabled: the race detector multiplies memory and time, so the
// ceilings of the hostile workbook test do not hold under it.
const raceEnabled = true
