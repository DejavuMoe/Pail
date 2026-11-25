//go:build race

package updatefmt

// raceDetector is true in a build with -race, which makes Diff about 30
// times slower: time budgets that catch a quadratic Diff do not hold there.
const raceDetector = true
