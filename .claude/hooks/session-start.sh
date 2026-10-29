#!/bin/bash
# Prepares a Claude Code cloud session: frontend dependencies for type-checks
# and builds, and the graphify CLI plus a fresh code graph, which the
# PreToolUse hooks in .claude/settings.json rely on.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
	exit 0
fi

GRAPHIFY_VERSION=0.9.97
cd "${CLAUDE_PROJECT_DIR:-$(pwd)}"

# graphify (Python CLI). uv when present, pip otherwise.
export PATH="$HOME/.local/bin:$PATH"
if ! graphify --version 2>/dev/null | grep -q "$GRAPHIFY_VERSION"; then
	if command -v uv >/dev/null 2>&1; then
		uv tool install --force "graphifyy==$GRAPHIFY_VERSION" >/dev/null
	else
		pip install --quiet --user "graphifyy==$GRAPHIFY_VERSION"
	fi
fi
# Hooks run with the session's base PATH, so expose it system-wide too.
if [ -w /usr/local/bin ] && [ ! -e /usr/local/bin/graphify ]; then
	ln -s "$HOME/.local/bin/graphify" /usr/local/bin/graphify
fi
if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
	echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$CLAUDE_ENV_FILE"
fi

# Frontend dependencies (npm install reuses the cached container state).
(cd frontend && npm install --no-audit --no-fund --silent)

# Code graph: AST only, local, no LLM calls (~10 s).
graphify update . >/dev/null
