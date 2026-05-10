package remote

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// AgentClient connects to $SSH_AUTH_SOCK and returns an agent.Agent.
// Linux/macOS only — Windows ssh-agent uses a named-pipe protocol that
// crypto/ssh/agent's Unix-socket dialer doesn't support. Out of scope
// for v0.7 per PRD 01.
//
// Returns the underlying net.Conn so callers can manage its lifetime
// (signers from the agent client require the conn to remain open).
func AgentClient() (agent.Agent, net.Conn, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, errors.New("SSH_AUTH_SOCK is unset")
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, err
	}
	return agent.NewClient(conn), conn, nil
}
