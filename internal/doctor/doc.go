// Package doctor implements the prerequisite checks used by `awsbnkctl doctor`.
// Each check returns a structured result so the same logic can also gate
// `awsbnkctl up` (e.g. refuse to apply if a required host tool is missing).
package doctor
