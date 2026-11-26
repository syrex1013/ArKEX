#!/usr/bin/env bash
# Guards reset-password.sh against credential leaks: the DSN password must not
# appear in terminal output (a cancelled reset used to print it before the
# confirmation prompt) nor in psql's argv (visible in ps). It reaches psql only
# through PGPASSWORD, URL-decoded, and the displayed/passed DSN is stripped.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

status=0
fail() { echo "FAIL: $*"; status=1; }

work=$(mktemp -d) || exit 1
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"

# psql stub: records its argv and PGPASSWORD, drains stdin, succeeds.
cat > "$work/bin/psql" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" > "${RECORD_DIR}/argv"
env | grep '^PGPASSWORD=' > "${RECORD_DIR}/pgpassword" || true
cat > /dev/null
exit 0
STUB
chmod +x "$work/bin/psql"

run() {
  local answer="$1" dsn="$2" record="$3"
  mkdir -p "$record"
  printf '%s\n' "$answer" | RECORD_DIR="$record" PATH="$work/bin:$PATH" \
    bash reset-password.sh --dsn "$dsn" -p 'NewPassw0rd!' > "$record/out" 2>&1
}

# URI DSN, percent-encoded password ("se%20cret%40x" decodes to "se cret@x").
DBPASS_ENC='se%20cret%40x'
run y "postgres://artex:${DBPASS_ENC}@127.0.0.1:5432/artex" "$work/c1"
grep -q 'ARTEX admin password has been reset' "$work/c1/out" || fail "reset did not complete (see $(cat "$work/c1/out"))"
grep -qF "$DBPASS_ENC" "$work/c1/out" && fail "encoded password printed"
grep -qF 'se cret@x' "$work/c1/out" && fail "decoded password printed"
[ -e "$work/c1/argv" ] || fail "psql was not invoked"
case "$(cat "$work/c1/argv")" in
  *"se%20cret%40x"*|*"se cret@x"*) fail "password reached psql argv: $(cat "$work/c1/argv")" ;;
  *"postgres://artex@127.0.0.1:5432/artex"*) ;;
  *) fail "argv DSN unexpected: $(cat "$work/c1/argv")" ;;
esac
[ "$(cat "$work/c1/pgpassword" 2>/dev/null)" = "PGPASSWORD=se cret@x" ] \
  || fail "PGPASSWORD wrong: $(cat "$work/c1/pgpassword" 2>/dev/null)"

# Cancelled reset must not print the credential either, and must not run psql.
run n "postgres://artex:${DBPASS_ENC}@127.0.0.1:5432/artex" "$work/c2"
grep -q 'cancelled' "$work/c2/out" || fail "cancel path changed"
grep -qF "$DBPASS_ENC" "$work/c2/out" && fail "password printed on cancel"
[ ! -e "$work/c2/argv" ] || fail "psql ran after cancel"

# Keyword/value form: password moves to PGPASSWORD and out of argv.
run y 'host=127.0.0.1 port=5432 user=artex password=kwpass dbname=artex' "$work/c3"
grep -q 'ARTEX admin password has been reset' "$work/c3/out" || fail "keyword reset did not complete"
[ "$(cat "$work/c3/pgpassword" 2>/dev/null)" = "PGPASSWORD=kwpass" ] \
  || fail "keyword PGPASSWORD wrong: $(cat "$work/c3/pgpassword" 2>/dev/null)"
case "$(cat "$work/c3/argv")" in
  *"password="*|*"kwpass"*) fail "keyword password reached psql argv: $(cat "$work/c3/argv")" ;;
esac

# A password-free DSN is passed through untouched; no PGPASSWORD from the script.
run y 'postgres://artex@127.0.0.1:5432/artex' "$work/c4"
[ ! -s "$work/c4/pgpassword" ] || fail "PGPASSWORD set although the DSN had no password"
case "$(cat "$work/c4/argv")" in
  *"postgres://artex@127.0.0.1:5432/artex"*) ;;
  *) fail "password-free DSN altered: $(cat "$work/c4/argv")" ;;
esac

[ "$status" = 0 ] && echo "ok: reset-password.sh keeps the DSN password out of output and argv"
exit "$status"
