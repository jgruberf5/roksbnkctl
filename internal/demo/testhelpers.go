// Package demo — test infrastructure exported for use by external test packages.
//
// This file is production code (not a _test.go file) so that external packages
// (e.g. internal/cli) can call ResetForTest from their own *_test.go files.
// It is intentionally narrow: one function, clearly named, explicitly documented
// as test-only. Internal/demo tests use the unexported resetRegistry() in
// registry_test.go; this file exists only to serve external test packages.
//
// Do NOT call ResetForTest from non-test files — it is a programmer error to
// wipe the registry in production. The name makes the intent explicit.
package demo

// ResetForTest wipes the demo registry to a nil/empty state.
//
// TEST-ONLY: call this only from *_test.go files. Calling it from production
// code will silently remove all registered demo use-cases for the remainder of
// the process lifetime. The name "ResetForTest" is the guard — if you see this
// called outside a test file, it is a bug.
func ResetForTest() { registry = nil }
