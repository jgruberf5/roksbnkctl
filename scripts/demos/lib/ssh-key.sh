#!/usr/bin/env bash
# ssh-key.sh — hand ssh a private key it will actually accept.
#
# THE PROBLEM. The bootstrap state dir normally sits beside the repo, and on WSL
# that is very often a Windows drive mounted through DrvFs (/mnt/c, /mnt/d).
# DrvFs does not carry POSIX modes: `chmod 600` reports success and silently
# leaves the file 0777. OpenSSH then refuses the key outright —
#
#   Permissions 0777 for '…/bnk-svc-key' are too open.
#   Load key "…": bad permissions
#   ubuntu@…: Permission denied (publickey).
#
# The last line is the one people read, so it looks like a wrong key or a missing
# authorized_keys entry. It is neither. This cost a whole Harbor bootstrap: the
# CA fetch failed, services.env was never written, and the Argo stage then died on
# an unset SERVICES_VPC — three failures, one file mode.
#
# THE FIX. If the key cannot be made 0600 where it lives, stage a copy on a real
# Linux filesystem and use that.
#
# Usage:  source "$(dirname "$0")/ssh-key.sh"
#         ssh -i "$(ssh_key)" … ;  scp -i "$(ssh_key)" …
#
# WHY THE PATH IS DETERMINISTIC AND THE TRAP IS SET AT SOURCE TIME.
# `$(ssh_key)` runs the function in a COMMAND SUBSTITUTION — a subshell. Anything
# the function does to shell state is discarded when that subshell exits, and any
# `trap … EXIT` it installs fires THERE, at the end of the substitution, before
# ssh has even started. A first cut did exactly that: it staged a key, deleted it
# on subshell exit, and handed ssh a path that no longer existed — which presents
# as the same "Permission denied (publickey)" it was meant to fix, once per retry.
#
# So the staged path is derived from $$ (stable across subshells, unlike $BASHPID)
# and the file is created only when absent. Cleanup is registered once, here, in
# the shell that sources this file.
#
# Reads SSH_KEY_FILE. Safe to source more than once.

if ! declare -F ssh_key >/dev/null 2>&1; then
  __ssh_key_staged_path(){ echo "${TMPDIR:-/tmp}/bnk-sshkey.$(id -u).$$"; }

  ssh_key(){
    local want="${SSH_KEY_FILE:-}"
    [[ -n "$want" && -f "$want" ]] || { echo "${want:-}"; return; }
    chmod 600 "$want" 2>/dev/null || true
    if [[ "$(stat -c %a "$want" 2>/dev/null)" == 600 ]]; then echo "$want"; return; fi

    local staged; staged="$(__ssh_key_staged_path)"
    if [[ ! -s "$staged" ]]; then
      ( umask 077; cat "$want" > "$staged" )
      chmod 600 "$staged" 2>/dev/null || true
      echo "==> ssh key staged to $staged — $want is on a mount that cannot hold mode 0600" >&2
    fi
    echo "$staged"
  }

  # Registered in the SOURCING shell, so it survives every `$(ssh_key)` subshell
  # and fires once, when the script itself exits.
  trap 'rm -f "$(__ssh_key_staged_path)"' EXIT
fi
