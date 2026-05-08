// Package doctor implements the prerequisite checks used by `roksbnkctl doctor`.
// Each check returns a structured result so the same logic can also gate
// `roksbnkctl up` (e.g. refuse to apply if terraform is missing on PATH).
package doctor
