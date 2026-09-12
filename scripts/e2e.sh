#!/usr/bin/env bash
# 端到端：起服务 → 种子 → 员工在非 git 目录 `teamai init --server` → push → 后台批准发布 → pull 拿到 → contribute → 上报进周报。
#
# 依赖：docker 里的 Postgres（make db-up）、node 20、一份打了 contrib/teamai-cli 补丁并 build 过的 teamai-cli。
#   TEAMAI_CLI=/path/to/teamai-cli/dist/index.js   不设则自动 clone + git am + build 到 .cache/teamai-cli
#   E2E_DSN=postgres://…/ith5_e2e                    独立库，脚本会清空它；不要指向开发库或测试库
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
E2E_DSN=${E2E_DSN:-postgres://ith5:ith5@localhost:55432/ith5_e2e?sslmode=disable}
PORT=${E2E_PORT:-18082}
BASE=http://127.0.0.1:$PORT
WORK=$(mktemp -d)
trap 'kill $SERVER_PID 2>/dev/null || true; rm -rf "$WORK"' EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
expect() { # expect <regex> <label> <<< output
  local out; out=$(cat)
  if grep -qE "$1" <<<"$out"; then echo "ok: $2"; else echo "FAIL: $2"; echo "$out"; exit 1; fi
}
jsonq() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

say "teamai-cli"
if [ -z "${TEAMAI_CLI:-}" ]; then
  CACHE=$ROOT/.cache/teamai-cli
  if [ ! -f "$CACHE/dist/index.js" ]; then
    rm -rf "$CACHE"; git clone -q https://github.com/Tencent/teamai-cli "$CACHE"
    (cd "$CACHE" && git checkout -q 34e1cfb && git -c user.name=e2e -c user.email=e2e@local am -q "$ROOT"/contrib/teamai-cli/*.patch && npm ci --silent && npm run build --silent)
  fi
  TEAMAI_CLI=$CACHE/dist/index.js
fi
echo "using $TEAMAI_CLI"

say "server"
docker exec ith5-postgres psql -U ith5 -tc "SELECT 1 FROM pg_database WHERE datname='ith5_e2e'" | grep -q 1 || docker exec ith5-postgres psql -U ith5 -c "CREATE DATABASE ith5_e2e" >/dev/null
docker exec ith5-postgres psql -U ith5 -d ith5_e2e -qc "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" >/dev/null 2>&1
(cd "$ROOT" && go build -o "$WORK/ith5-server" ./cmd/ith5-server)
export ITH5_DATABASE_URL=$E2E_DSN ITH5_JWT_SECRET=e2e-0123456789abcdef0123456789abcdef ITH5_LISTEN_ADDR=127.0.0.1:$PORT ITH5_BASE_URL=$BASE
"$WORK/ith5-server" -seed >/dev/null
"$WORK/ith5-server" >"$WORK/server.log" 2>&1 & SERVER_PID=$!
for _ in $(seq 1 30); do curl -sf "$BASE/healthz" >/dev/null && break; sleep 0.5; done

session() { # email → access token
  local lt; lt=$(curl -s -X POST "$BASE/v1/auth/login" -H 'content-type: application/json' -d "{\"email\":\"$1\",\"password\":\"demo-password\"}")
  local tok uid; tok=$(echo "$lt" | jsonq 'd["login_token"]'); uid=$(echo "$lt" | jsonq 'd["organizations"][0]["user_id"]')
  curl -s -X POST "$BASE/v1/auth/session" -H 'content-type: application/json' -d "{\"login_token\":\"$tok\",\"user_id\":\"$uid\"}" | jsonq 'd["access_token"]'
}
MEMBER=$(session member@example.com); OWNER=$(session demo@example.com)

say "employee: init --server in a directory that is not a git repo"
export HOME=$WORK/home; mkdir -p "$HOME/ws"; cd "$HOME/ws"
node "$TEAMAI_CLI" init --server "$BASE" --agent claude,codex --scope project >"$WORK/init.out" 2>&1 &
INIT_PID=$!
for _ in $(seq 1 40); do grep -q "code:" "$WORK/init.out" && break; sleep 0.5; done
CODE=$(grep -o "code: [A-Z0-9-]*" "$WORK/init.out" | awk '{print $2}')
curl -sf -X POST "$BASE/v1/auth/device/activate" -H "Authorization: Bearer $MEMBER" -H 'content-type: application/json' -d "{\"user_code\":\"$CODE\"}" >/dev/null
wait $INIT_PID
test ! -d .git
test -f .claude/skills/billing-deploy/SKILL.md && test -f .claude/rules/security-baseline.md && test -f .claude/agents/code-reviewer.md
grep -q teamai "$HOME/.claude/settings.json"   # hooks are injected into the tool's user-level settings
echo "ok: claude — skills, rules, agents, hooks installed without git"
test -f .codex/skills/billing-deploy/SKILL.md && test -f .codex/rules/security-baseline.md && test -f .codex/agents/code-reviewer.toml
grep -q teamai "$HOME/.codex/hooks.json"
echo "ok: codex — skills, rules, agents (toml), hooks installed"

say "second pull is a 304"
node "$TEAMAI_CLI" pull 2>&1 | expect "unchanged at" "unchanged"

say "push a new skill → review → publish → pull"
mkdir -p .claude/skills/e2e-skill && printf -- '---\nname: e2e-skill\ndescription: e2e\n---\nbody\n' > .claude/skills/e2e-skill/SKILL.md
node "$TEAMAI_CLI" push --all 2>&1 | expect "Submitted for review" "submitted"
CS=$(curl -s -H "Authorization: Bearer $MEMBER" "$BASE/v1/change-sets?view=mine" | jsonq '[c for c in d["items"] if c["state"]=="in_review"][0]["id"]')
DG=$(curl -s -H "Authorization: Bearer $OWNER" "$BASE/v1/change-sets/$CS" | jsonq 'd["submitted_digest"]')
curl -sf -o /dev/null -X POST "$BASE/v1/change-sets/$CS/reviews" -H "Authorization: Bearer $OWNER" -H 'content-type: application/json' -d "{\"decision\":\"approve\",\"digest\":\"$DG\",\"comment\":\"e2e\"}"
curl -sf -o /dev/null -X POST "$BASE/v1/change-sets/$CS/publish" -H "Authorization: Bearer $OWNER"
node "$TEAMAI_CLI" pull 2>&1 | expect "e2e-skill|all updated" "published skill pulled"
test -f .claude/skills/e2e-skill/SKILL.md && test -f .codex/skills/e2e-skill/SKILL.md
BINDING=$(grep bindingId .teamai/config.yaml | awk '{print $2}')
curl -s -H "Authorization: Bearer $MEMBER" "$BASE/v1/bindings/$BINDING/snapshot" | jsonq '[e["name"] for e in d["resources"] if e["kind"]=="skill"]' | expect "e2e-skill" "snapshot has it"

say "contribute (published) and a secret (rejected)"
printf '# e2e learning\n\nretry the deploy.\n' > "$WORK/l.md"
node "$TEAMAI_CLI" contribute --file "$WORK/l.md" 2>&1 | expect "Contributed" "contributed"
printf '# bad\n\ntoken = "abcdefghijklmnop1234"\n' > "$WORK/bad.md"
{ node "$TEAMAI_CLI" contribute --file "$WORK/bad.md" 2>&1 || true; } | expect "secret|SECRET_DETECTED" "secret rejected"

say "usage report reaches the digest"
printf '{"timestamp":"%s","skill":"billing-deploy","tool":"claude"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$HOME/.teamai/usage.jsonl"
node "$TEAMAI_CLI" pull >/dev/null 2>&1
curl -s -H "Authorization: Bearer $MEMBER" "$BASE/v1/reports/digest" | jsonq '[s["skill"] for s in d["top_skills"]]' | expect "billing-deploy" "digest"

say "all e2e checks passed"
