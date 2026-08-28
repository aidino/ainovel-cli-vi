#!/usr/bin/env bash
# check_no_cjk.sh — rà phần chữ Hán (CJK) còn sót sau khi Việt hóa.
# Dùng: ./scripts/check_no_cjk.sh [scope...]
#   scope mặc định: assets docs README.md config.example.jsonc
#   Thêm "go" để rà cả nguồn Go, "all" cho mọi thứ.
# Đánh dấu file được phép giữ CJK bằng cách liệt kê dưới ALLOW (testdata nhập khẩu tiếng Trung...).

set -uo pipefail

ALLOW=(
  "internal/tools/premise_structure.go" # alias tiếng Trung cố ý giữ để đọc premise bản gốc
  "docs/vi-glossary.md"                 # bảng thuật ngữ có cột tiếng Trung
)

is_allowed() {
  local f="$1"
  for a in "${ALLOW[@]}"; do [[ "$f" == *"$a"* ]] && return 0; done
  return 1
}

scopes=("${@:-}")
if [[ ${#scopes[@]} -eq 0 || " ${scopes[*]} " == *" all "* ]]; then
  targets=(assets docs README.md config.example.jsonc internal cmd evals scripts Dockerfile docker-compose.yml)
elif [[ " ${scopes[*]} " == *" go "* ]]; then
  targets=(internal cmd)
else
  targets=("${scopes[@]}")
fi

fail=0
while IFS= read -r line; do
  f="${line%%:*}"
  if is_allowed "$f"; then continue; fi
  echo "CÒN CJK: $line"
  fail=1
done < <(grep -rnP '[\x{4e00}-\x{9fff}]' ${targets[@]/#/} 2>/dev/null \
           --include='*.go' --include='*.md' --include='*.json' --include='*.jsonc' \
           --include='*.yml' --include='*.sh' --include='*.py' \
           --exclude-dir=.git --exclude-dir=node_modules)

if [[ $fail -eq 0 ]]; then
  echo "OK: không còn chữ Hán trong phạm vi kiểm tra."
else
  echo "Còn chữ Hán — xem danh sách trên."
fi
exit $fail