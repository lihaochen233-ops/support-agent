#!/bin/sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
config_path="$project_root/.env"
if [ -e "$config_path" ]; then
  printf '%s\n' '.env already exists; keeping the existing configuration.'
  exit 0
fi
command -v openssl >/dev/null 2>&1 || {
  printf '%s\n' 'OpenSSL is required to generate credentials.' >&2
  exit 1
}
umask 077
database_password=$(openssl rand -hex 18)
cache_password=$(openssl rand -hex 18)
admin_password=$(openssl rand -hex 18)
agent_password=$(openssl rand -hex 18)
# noclobber also prevents overwriting a configuration created concurrently.
(
  set -C
  sed \
    -e "s/replace_with_a_long_random_password/$database_password/g" \
    -e "s/replace_with_another_random_password/$cache_password/g" \
    -e "s/replace_with_a_password_at_least_12_characters/$admin_password/g" \
    -e "s/replace_with_another_password_at_least_12_characters/$agent_password/g" \
    "$project_root/.env.example" > "$config_path"
)
printf '%s\n' 'Created .env with random credentials. Configure model API keys to enable AI.'
