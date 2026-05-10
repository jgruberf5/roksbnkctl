// Package remote implements the embedded SSH client and related plumbing
// for roksbnkctl's --on <target> flag (PRD 01).
//
// Subdivisions:
//
//   - ssh.go      — Client wrapper around *ssh.Client; Run, Shell, Close
//   - targets.go  — Target struct + workspace config integration
//   - keys.go     — Key resolution (file, agent, tf-output)
//   - hostkeys.go — known_hosts read/write + TOFU prompt
//   - agent.go    — ssh-agent socket discovery (linux/darwin)
//
// The package is deliberately decoupled from terraform: callers that need
// `tf-output:<name>` keys pass in the resolved outputs map. Keeps the
// dependency direction tf → remote, never the reverse.
package remote
