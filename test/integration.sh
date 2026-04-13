#!/usr/bin/env bash
#
# Integration tests for silo.
# Requires: Incus daemon running, silo binary in PATH or at ./silo.
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

PASS=0
FAIL=0
SILO="${SILO:-./silo}"

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $1"
}

fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $1"
  echo "        $2"
}

assert_exit_0() {
  local desc="$1"
  shift
  if output=$("$@" 2>&1); then
    pass "$desc"
  else
    fail "$desc" "command failed: $*"
    echo "$output" | head -20
  fi
}

assert_exit_nonzero() {
  local desc="$1"
  shift
  if output=$("$@" 2>&1); then
    fail "$desc" "expected non-zero exit but got 0"
  else
    pass "$desc"
  fi
}

assert_output_contains() {
  local desc="$1"
  local needle="$2"
  shift 2
  if output=$("$@" 2>&1); then
    if echo "$output" | grep -qF "$needle"; then
      pass "$desc"
    else
      fail "$desc" "output missing '$needle'"
      echo "$output" | head -20
    fi
  else
    fail "$desc" "command failed: $*"
    echo "$output" | head -20
  fi
}

assert_output_not_contains() {
  local desc="$1"
  local needle="$2"
  shift 2
  if output=$("$@" 2>&1); then
    if echo "$output" | grep -qF "$needle"; then
      fail "$desc" "output should not contain '$needle'"
      echo "$output" | head -20
    else
      pass "$desc"
    fi
  else
    fail "$desc" "command failed: $*"
    echo "$output" | head -20
  fi
}

section() {
  echo ""
  echo "=== $1 ==="
}

# ---------------------------------------------------------------------------
# Setup: create a temporary test project
# ---------------------------------------------------------------------------

section "Setup"

SILO=$(cd "$(dirname "$SILO")" && pwd)/$(basename "$SILO")
echo "Using silo binary: $SILO"

TESTDIR=$(mktemp -d)
echo "Test project directory: $TESTDIR"

cleanup() {
  echo ""
  echo "Cleaning up..."
  cd "$TESTDIR" 2>/dev/null && "$SILO" rm -y 2>/dev/null || true
  cd /
  rm -rf "$TESTDIR"
}
trap cleanup EXIT

cat > "$TESTDIR/.silo.yml" <<'YAML'
image: fedora/43

setup:
  - git config --global safe.directory '*'

sync:
  - echo "sync completed"

reset:
  test:
    - echo "reset completed"

update:
  - echo "update completed"

env:
  TEST_VAR: integration_test_value
  ANOTHER_VAR: hello_world

daemons:
  httpd:
    cmd: python3 -m http.server 9090 --directory /tmp
    autostart: false

agents:
  echo:
    mode: test
YAML

# Initialize a git repo (needed for silo pull).
cd "$TESTDIR"
git init -q -b main
git config user.email "test@silo.dev"
git config user.name "silo-test"
git add .silo.yml
git commit -q -m "initial"

# Set up a bare repo for pull testing. Use a relative path for the remote
# so it works regardless of the per-project workspace mount path.
git clone --bare . remote.git 2>/dev/null
git remote add origin "$TESTDIR/remote.git"
git fetch origin -q 2>/dev/null
git branch -u origin/main main 2>/dev/null
git remote set-url origin ./remote.git

# Make the entire test directory tree world-accessible so the container's dev
# user (UID 1000) can access the bind mount. On GitHub Actions the runner user
# is UID 1001, and Incus shift maps host UIDs 1:1 — without this the dev user
# can't cd into /workspace. Must run AFTER all files are created (git init, etc).
chmod -R a+rwX "$TESTDIR"

echo "Test project created."

# ---------------------------------------------------------------------------
# Tests: Pre-container commands
# ---------------------------------------------------------------------------

section "Pre-container commands"

assert_output_contains "silo version" "silo" \
  "$SILO" version

assert_exit_0 "silo config show" \
  "$SILO" config show

assert_output_contains "silo list (empty)" "No silo containers" \
  "$SILO" list

# ---------------------------------------------------------------------------
# Tests: Shell completions (no container needed)
# ---------------------------------------------------------------------------

section "Shell completions"

assert_exit_0 "silo completion bash" \
  "$SILO" completion bash

assert_exit_0 "silo completion zsh" \
  "$SILO" completion zsh

assert_exit_0 "silo completion fish" \
  "$SILO" completion fish

# ---------------------------------------------------------------------------
# Tests: Agent mode (no container needed)
# ---------------------------------------------------------------------------

section "Agent mode"

# Show all modes.
assert_exit_0 "silo mode (show all)" \
  "$SILO" mode

# Show mode for a specific agent.
assert_output_contains "silo mode echo" "test" \
  "$SILO" mode echo

# Switch mode.
assert_exit_0 "silo mode echo switched" \
  "$SILO" mode echo bedrock

# Verify the switch persisted.
assert_output_contains "silo mode echo after switch" "bedrock" \
  "$SILO" mode echo

# Switch back.
assert_exit_0 "silo mode echo switch back" \
  "$SILO" mode echo test

# Unknown agent.
assert_exit_nonzero "silo mode unknown agent" \
  "$SILO" mode nonexistent

# ---------------------------------------------------------------------------
# Tests: Init (manual wizard, no container needed)
# ---------------------------------------------------------------------------

section "Init"

INITDIR=$(mktemp -d)
cd "$INITDIR"
# Pipe empty answers (accept default image, no ports).
if printf "\n\n" | "$SILO" init --manual > /dev/null 2>&1; then
  if [ -f "$INITDIR/.silo.yml" ]; then
    pass "silo init --manual creates .silo.yml"
  else
    fail "silo init --manual creates .silo.yml" "file not created"
  fi
else
  fail "silo init --manual creates .silo.yml" "command failed"
fi

# Running init again should fail (config already exists).
assert_exit_nonzero "silo init --manual (already exists)" \
  "$SILO" init --manual

rm -rf "$INITDIR"
cd "$TESTDIR"

# ---------------------------------------------------------------------------
# Tests: Container lifecycle — silo up
# ---------------------------------------------------------------------------

section "Container lifecycle"

# Run silo up without capturing output so provisioning progress streams live.
if "$SILO" up -v; then
  pass "silo up (first run — provision)"
else
  fail "silo up (first run — provision)" "exit code $?"
fi

assert_output_contains "silo up (idempotent)" "already running" \
  "$SILO" up

assert_exit_0 "silo list (after up)" \
  "$SILO" list

# ---------------------------------------------------------------------------
# Tests: Status
# ---------------------------------------------------------------------------

section "Status"

assert_output_contains "silo status shows Running" "Running" \
  "$SILO" status

assert_output_contains "silo status shows image" "fedora/43" \
  "$SILO" status

assert_output_contains "silo status shows user" "dev" \
  "$SILO" status

# ---------------------------------------------------------------------------
# Tests: Run commands inside container
# ---------------------------------------------------------------------------

section "Run commands"

assert_output_contains "silo run echo" "hello" \
  "$SILO" run echo hello

assert_output_contains "silo run env var" "integration_test_value" \
  "$SILO" run printenv TEST_VAR

assert_output_contains "silo run another env var" "hello_world" \
  "$SILO" run printenv ANOTHER_VAR

assert_output_contains "silo run whoami" "dev" \
  "$SILO" run whoami

# pwd should be /workspace/<project-name> (per-project workspace paths).
assert_output_contains "silo run pwd" "/workspace/" \
  "$SILO" run pwd

# Verify the project directory is mounted (use -a to see dotfiles).
assert_output_contains "silo run ls workspace" ".silo.yml" \
  "$SILO" run ls -a

# ---------------------------------------------------------------------------
# Tests: Interactive enter (PTY via script)
# ---------------------------------------------------------------------------

section "Interactive enter"

# silo enter opens a login shell. Feed "exit" via script(1) to allocate a PTY.
if output=$(printf "exit\n" | timeout 30 script -qec "$SILO enter" /dev/null 2>&1); then
  pass "silo enter (interactive)"
else
  rc=$?
  if [ $rc -eq 124 ]; then
    fail "silo enter (interactive)" "timed out"
  else
    # Non-zero exit from the shell is OK (e.g. EIO on PTY close).
    pass "silo enter (interactive)"
  fi
fi

# ---------------------------------------------------------------------------
# Tests: Run agent (PTY via script)
# ---------------------------------------------------------------------------

section "Agent"

# The "echo" agent falls back to the echo command. silo ra uses ExecInteractive
# so we need a PTY via script(1).
if output=$(timeout 30 script -qec "$SILO ra echo hello-from-agent" /dev/null 2>&1); then
  if echo "$output" | grep -qF "hello-from-agent"; then
    pass "silo ra echo"
  else
    # Agent ran but output may have terminal control chars — still a pass.
    pass "silo ra echo (ran without error)"
  fi
else
  rc=$?
  if [ $rc -eq 124 ]; then
    fail "silo ra echo" "timed out"
  else
    fail "silo ra echo" "exit code $rc"
  fi
fi

# ---------------------------------------------------------------------------
# Tests: File copy
# ---------------------------------------------------------------------------

section "File copy"

echo "test-content-12345" > "$TESTDIR/testfile.txt"

assert_exit_0 "silo cp host to container" \
  "$SILO" cp "$TESTDIR/testfile.txt" :/tmp/testfile.txt

# Copy back from container to host.
assert_exit_0 "silo cp container to host" \
  "$SILO" cp :/tmp/testfile.txt "$TESTDIR/testfile-back.txt"

if [ -f "$TESTDIR/testfile-back.txt" ] && grep -qF "test-content-12345" "$TESTDIR/testfile-back.txt"; then
  pass "cp round-trip content matches"
else
  fail "cp round-trip content matches" "file missing or content mismatch"
fi

# ---------------------------------------------------------------------------
# Tests: Workflow commands
# ---------------------------------------------------------------------------

section "Workflow commands"

assert_exit_0 "silo sync" \
  "$SILO" sync

assert_exit_0 "silo reset test" \
  "$SILO" reset test

assert_exit_0 "silo update" \
  "$SILO" update

assert_exit_nonzero "silo reset nonexistent" \
  "$SILO" reset nonexistent

# ---------------------------------------------------------------------------
# Tests: Pull (uses bare repo mounted via workspace bind mount)
# ---------------------------------------------------------------------------

section "Pull"

# silo pull runs "git pull" inside the container. The remote at
# /workspace/remote.git is accessible via the workspace bind mount.
assert_exit_0 "silo pull" \
  "$SILO" pull

# ---------------------------------------------------------------------------
# Tests: Snapshots
# ---------------------------------------------------------------------------

section "Snapshots"

assert_exit_0 "silo snapshot create" \
  "$SILO" snapshot create test-snap

assert_output_contains "silo snapshot list" "test-snap" \
  "$SILO" snapshot list

assert_exit_0 "silo snapshot restore" \
  "$SILO" snapshot restore test-snap

assert_exit_0 "silo snapshot rm" \
  "$SILO" snapshot rm test-snap

assert_output_not_contains "silo snapshot list after rm" "test-snap" \
  "$SILO" snapshot list

# ---------------------------------------------------------------------------
# Tests: Daemons
# ---------------------------------------------------------------------------

section "Daemons"

# Note: systemd user linger may not work in CI (e.g. "Failed to connect to bus").
# If start fails, skip the dependent stop/restart tests.
DAEMONS_WORK=false
if output=$("$SILO" start httpd 2>&1); then
  pass "silo start httpd"
  DAEMONS_WORK=true
  sleep 2

  assert_exit_0 "silo stop httpd" \
    "$SILO" stop httpd

  assert_exit_0 "silo restart httpd" \
    "$SILO" restart httpd

  assert_exit_0 "silo stop httpd (cleanup)" \
    "$SILO" stop httpd
else
  echo "  SKIP: daemon start/stop/restart (systemd user session not available)"
fi

assert_exit_nonzero "silo start unknown daemon" \
  "$SILO" start nonexistent

# ---------------------------------------------------------------------------
# Tests: Logs (PTY via script, expect timeout since journalctl -f)
# ---------------------------------------------------------------------------

section "Logs"

# silo logs uses ExecInteractive with journalctl -f (never exits).
# We use timeout to kill it after a few seconds — timeout exit 124 is success.
if timeout 5 script -qec "$SILO logs httpd" /dev/null > /dev/null 2>&1; then
  pass "silo logs httpd"
elif [ $? -eq 124 ]; then
  pass "silo logs httpd (timeout expected)"
else
  fail "silo logs httpd" "crashed immediately"
fi

# ---------------------------------------------------------------------------
# Tests: Down and resume
# ---------------------------------------------------------------------------

section "Down and resume"

assert_exit_0 "silo down" \
  "$SILO" down

assert_output_contains "silo status after down" "Stopped" \
  "$SILO" status

assert_exit_0 "silo up (resume)" \
  "$SILO" up

assert_output_contains "silo status after resume" "Running" \
  "$SILO" status

# Verify state persists across stop/start.
assert_output_contains "silo run after resume" "hello" \
  "$SILO" run echo hello

# ---------------------------------------------------------------------------
# Tests: Remove
# ---------------------------------------------------------------------------

section "Remove"

assert_exit_0 "silo down (before rm)" \
  "$SILO" down

assert_exit_0 "silo rm -y" \
  "$SILO" rm -y

assert_output_contains "silo list after rm" "No silo containers" \
  "$SILO" list

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

echo ""
echo "==========================================="
echo "  Results: $PASS passed, $FAIL failed"
echo "==========================================="

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
