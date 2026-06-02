#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWARM_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEFAULT_CMS_ROOT="$(cd "${SWARM_ROOT}/.." && pwd)/nostr-cms"

MODE="prompt"
FORCE="false"
DOMAIN=""
RELAY_NAME=""
RELAY_DESCRIPTION=""
RELAY_PUBKEY=""
CMS_ROOT=""

usage() {
  cat <<'EOF'
Meetup Space installer (single command)

Usage:
  ./setup/install-meetup-space.sh [options]

Modes:
  --mode manual   Print manual install steps only (no files written)
  --mode prompt   Ask interactive questions and write env files (default)
  --mode agent    Non-interactive mode for agents/automation

Required in --mode agent:
  --domain <domain>
  --relay-pubkey <hex-pubkey>

Optional:
  --relay-name <name>            default: Swarm Relay
  --relay-description <text>     default: Meetup Space relay
  --cms-path <absolute/path>     default: ../nostr-cms
  --force                        overwrite existing env files

Examples:
  ./setup/install-meetup-space.sh --mode prompt

  ./setup/install-meetup-space.sh --mode agent \
    --domain meetup.example.com \
    --relay-pubkey <64-char-hex> \
    --relay-name "My Relay" \
    --force
EOF
}

read_with_default() {
  local prompt="$1"
  local default_value="$2"
  local value
  read -r -p "${prompt} [${default_value}]: " value
  if [[ -z "${value}" ]]; then
    value="${default_value}"
  fi
  printf '%s' "${value}"
}

confirm_overwrite() {
  local path="$1"
  if [[ "${FORCE}" == "true" ]]; then
    return 0
  fi

  if [[ -f "${path}" ]]; then
    local answer
    read -r -p "${path} already exists. Overwrite? (y/N): " answer
    if [[ ! "${answer}" =~ ^[Yy]$ ]]; then
      echo "Cancelled"
      exit 0
    fi
  fi
}

is_hex_pubkey() {
  local v="$1"
  [[ "${v}" =~ ^[0-9a-fA-F]{64}$ ]]
}

print_manual_steps() {
  local domain_hint="your-domain.com"
  cat <<EOF
Manual install steps

1) Create swarm env at: ${SWARM_ROOT}/.env
   Required core values:
   - RELAY_NAME="Swarm Relay"
   - RELAY_PUBKEY="<64-char-hex>"
   - TEAM_DOMAIN="${domain_hint}"
   - NPUB_DOMAIN="${domain_hint}"
   - WEBSOCKET_URL="wss://${domain_hint}"
   - BLOSSOM_URL="https://${domain_hint}"

2) Create cms env at: ${DEFAULT_CMS_ROOT}/.env.local
   - VITE_REMOTE_NOSTR_JSON_URL=https://${domain_hint}/.well-known/nostr.json
   - VITE_DEFAULT_RELAY=wss://${domain_hint}
   - VITE_MASTER_PUBKEY=<same as RELAY_PUBKEY>
   - VITE_SWARM_API_URL=https://${domain_hint}/api

3) Start swarm:
   docker compose up -d --build

4) Build/run nostr-cms:
   pnpm install
   pnpm build
   pnpm preview --host --port 4173

5) Configure reverse proxy (see template):
   ${SWARM_ROOT}/setup/nginx-meetup-space.conf

Routing:
- https://${domain_hint}/ -> nostr-cms
- https://${domain_hint}/api/* -> swarm
- wss://${domain_hint}/ -> swarm
- https://${domain_hint}/.well-known/nostr.json -> swarm

Initial super-user bootstrap:
- Set RELAY_PUBKEY to operator key.
- Swarm writes '_' entry in nostr.json.
- First admin login in CMS must use that same Nostr key.
EOF
}

write_env_files() {
  local swarm_env_path="${SWARM_ROOT}/.env"
  local cms_env_path="${CMS_ROOT}/.env.local"

  if [[ ! -d "${CMS_ROOT}" ]]; then
    echo "nostr-cms path does not exist: ${CMS_ROOT}"
    exit 1
  fi

  if [[ ! -f "${CMS_ROOT}/package.json" ]]; then
    echo "nostr-cms package.json not found at: ${CMS_ROOT}"
    exit 1
  fi

  confirm_overwrite "${swarm_env_path}"
  confirm_overwrite "${cms_env_path}"

  cat > "${swarm_env_path}" <<EOF
DOCKER_ENV=true

RELAY_NAME="${RELAY_NAME}"
RELAY_PUBKEY="${RELAY_PUBKEY}"
RELAY_DESCRIPTION="${RELAY_DESCRIPTION}"

TEAM_DOMAIN="${DOMAIN}"
NPUB_DOMAIN="${DOMAIN}"

RELAY_PORT="3334"
WEBSOCKET_URL="wss://${DOMAIN}"
BLOSSOM_URL="https://${DOMAIN}"

DB_ENGINE="badger"
DB_PATH="db/"

BLOSSOM_ENABLED="true"
BLOSSOM_PATH="blossom/"

STORAGE_BACKEND="filesystem"
NIP05_PATH="public/.well-known/nostr.json"
EOF

  cat > "${cms_env_path}" <<EOF
VITE_REMOTE_NOSTR_JSON_URL=https://${DOMAIN}/.well-known/nostr.json
VITE_DEFAULT_RELAY=wss://${DOMAIN}
VITE_MASTER_PUBKEY=${RELAY_PUBKEY}
VITE_SWARM_API_URL=https://${DOMAIN}/api
EOF

  cat <<EOF

Created:
- ${swarm_env_path}
- ${cms_env_path}

Next:
1) Start swarm:
   docker compose up -d --build
2) Build/run nostr-cms:
   pnpm install && pnpm build && pnpm preview --host --port 4173
3) Configure nginx from template:
   ${SWARM_ROOT}/setup/nginx-meetup-space.conf
4) Login at https://${DOMAIN}/admin with the relay super-user key (${RELAY_PUBKEY})
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      MODE="${2:-}"
      shift 2
      ;;
    --domain)
      DOMAIN="${2:-}"
      shift 2
      ;;
    --relay-name)
      RELAY_NAME="${2:-}"
      shift 2
      ;;
    --relay-description)
      RELAY_DESCRIPTION="${2:-}"
      shift 2
      ;;
    --relay-pubkey)
      RELAY_PUBKEY="${2:-}"
      shift 2
      ;;
    --cms-path)
      CMS_ROOT="${2:-}"
      shift 2
      ;;
    --force)
      FORCE="true"
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

if [[ -z "${CMS_ROOT}" ]]; then
  CMS_ROOT="${DEFAULT_CMS_ROOT}"
fi
if [[ -z "${RELAY_NAME}" ]]; then
  RELAY_NAME="Swarm Relay"
fi
if [[ -z "${RELAY_DESCRIPTION}" ]]; then
  RELAY_DESCRIPTION="Meetup Space relay"
fi

case "${MODE}" in
  manual)
    print_manual_steps
    exit 0
    ;;

  prompt)
    echo "Meetup Space installer (prompt mode)"
    DOMAIN="$(read_with_default "Primary domain" "example.com")"
    RELAY_NAME="$(read_with_default "Relay name" "${RELAY_NAME}")"
    RELAY_DESCRIPTION="$(read_with_default "Relay description" "${RELAY_DESCRIPTION}")"
    RELAY_PUBKEY="$(read_with_default "Relay admin pubkey (64-char hex)" "${RELAY_PUBKEY}")"
    CMS_ROOT="$(read_with_default "Path to nostr-cms repo" "${CMS_ROOT}")"

    if ! is_hex_pubkey "${RELAY_PUBKEY}"; then
      echo "Invalid relay pubkey. Expected 64-char hex string."
      exit 1
    fi

    write_env_files
    ;;

  agent)
    if [[ -z "${DOMAIN}" || -z "${RELAY_PUBKEY}" ]]; then
      echo "--mode agent requires --domain and --relay-pubkey"
      usage
      exit 1
    fi

    if ! is_hex_pubkey "${RELAY_PUBKEY}"; then
      echo "Invalid relay pubkey. Expected 64-char hex string."
      exit 1
    fi

    write_env_files
    ;;

  *)
    echo "Invalid mode: ${MODE}. Expected one of: manual, prompt, agent"
    usage
    exit 1
    ;;
esac
