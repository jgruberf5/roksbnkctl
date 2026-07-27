//go:build windows

package cli

import "syscall"

// On Windows, roksbnkctl's UTF-8 output glyphs (✓ ⚠ ✗ → ─ …) render as cp1252
// mojibake (Γ£ô ΓÜá ΓÇö) because the console's default output code page is the
// legacy OEM/ANSI page, not UTF-8. Setting the console output code page to UTF-8 at
// startup makes the whole surface — doctor, phase progress, box-drawing separators —
// display correctly, without touching a single output string.
//
// Best-effort by design: the call is a harmless no-op when stdout is redirected or
// piped (no attached console), and any failure is ignored — a wrong code page only
// affects cosmetics, never correctness. A UTF-8 console is a strict superset of ASCII,
// so plain-ASCII output (and child processes like terraform/helm) are unaffected.
var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
)

func init() {
	const cpUTF8 = 65001
	_, _, _ = procSetConsoleOutputCP.Call(uintptr(cpUTF8))
}
