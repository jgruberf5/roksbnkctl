package remote

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// AgentClient connects to $SSH_AUTH_SOCK and returns an agent.Agent.
// Linux/macOS only — Windows ssh-agent uses a named-pipe protocol that
// crypto/ssh/agent's Unix-socket dialer doesn't support.
//
// Returns the underlying net.Conn so callers can manage its lifetime
// (signers from the agent client require the conn to remain open).
func AgentClient() (agent.Agent, net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is unset")
	}
	conn, err := net.Dial("unix", sock) // #nosec G704 -- SSH_AUTH_SOCK is an OS-provided path, not external SSRF surface
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), conn, nil
}
