#!/bin/sh
set -eu
pass="${NACOS_PASSWORD:-nacos}"
identity_key="${NACOS_AUTH_IDENTITY_KEY:-serverIdentity}"
identity_value="${NACOS_AUTH_IDENTITY_VALUE:-security}"

login() {
  curl -sS -X POST "http://nacos:8848/nacos/v1/auth/login" \
    --data-urlencode "username=nacos" \
    --data-urlencode "password=${pass}" \
    | sed -n 's/.*"accessToken":"\([^"]*\)".*/\1/p'
}

token=$(login || true)
if [ -z "$token" ]; then
  curl -fsS -X POST "http://nacos:8848/nacos/v1/auth/users/admin" \
    -H "${identity_key}: ${identity_value}" \
    --data-urlencode "password=${pass}" >/dev/null
  token=$(login || true)
fi
if [ -z "$token" ]; then
  echo "nacos login failed" >&2
  exit 1
fi
for name in core chatroom gateway webhook admin builder; do
  code=$(curl -s -o /dev/null -w '%{http_code}' \
    "http://nacos:8848/nacos/v1/cs/configs?dataId=${name}.yaml&group=orbikit&accessToken=${token}")
  if [ "$code" = "200" ]; then
    echo "keep ${name}.yaml"
    continue
  fi
  curl -fsS -X POST "http://nacos:8848/nacos/v1/cs/configs?accessToken=${token}" \
    --data-urlencode "dataId=${name}.yaml" \
    --data-urlencode "group=orbikit" \
    --data-urlencode "type=yaml" \
    --data-urlencode "content@/configs/${name}.yaml" >/dev/null
  echo "published ${name}.yaml"
done
