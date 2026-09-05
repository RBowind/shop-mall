#!/usr/bin/env bash
# seed-products.sh — 在本地 admin 跑一遍, 灌一组商品 + 图片作为演示数据。
#
# 依赖: 已 deploy 三容器 (postgres / app / nginx), admin 账号 admin/Admin@ShopMall26#。
# 用法: bash admin/scripts/seed-products.sh
set -euo pipefail

BASE="${BASE:-https://localhost}"
USER="${ADMIN_USER:-admin}"
PASS="${ADMIN_PASS:-Admin@ShopMall26#}"
COOKIE="$(mktemp)"
trap "rm -f $COOKIE" EXIT

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SEED_DIR="$SCRIPT_DIR/seed"

echo "==> login as $USER"
curl -sk -c "$COOKIE" -b "$COOKIE" \
  -H "Content-Type: application/json" \
  -H "Origin: $BASE" \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" \
  "$BASE/api/admin/v1/auth/login" >/dev/null

# Pull the non-HttpOnly csrf_token cookie value (Path=/api/admin/v1) from the
# Netscape cookie jar so write requests can echo it as X-CSRF-Token, which is
# what backend/internal/middleware and services/request.ts expect.
CSRF_TOKEN="$(awk '$6 == "csrf_token" {print $7}' "$COOKIE" | head -1)"
if [[ -z "$CSRF_TOKEN" ]]; then
  echo "FATAL: login did not return csrf_token"
  cat "$COOKIE"
  exit 1
fi
echo "    csrf_token=${CSRF_TOKEN:0:8}…"

upload_image() {
  local file="$1"
  local response
  response="$(curl -sk -b "$COOKIE" \
    -H "Origin: $BASE" \
    -H "X-CSRF-Token: $CSRF_TOKEN" \
    -F "file=@${file}" \
    "$BASE/api/admin/v1/images")"
  echo "$response" | sed -n 's/.*"key":"\([^"]*\)".*/\1/p' | head -1
}

create_product() {
  local name="$1"
  local description="$2"
  local price="$3"
  local stock="$4"
  local status="$5"
  local key="$6"
  curl -sk -b "$COOKIE" \
    -H "Content-Type: application/json" \
    -H "Origin: $BASE" \
    -H "X-CSRF-Token: $CSRF_TOKEN" \
    -d "$(jq -n \
      --arg name "$name" \
      --arg description "$description" \
      --arg price "$price" \
      --argjson stock "$stock" \
      --arg status "$status" \
      --arg key "$key" \
      '{name:$name,description:$description,price_points:$price,stock:$stock,status:$status,main_image:$key}')" \
    "$BASE/api/admin/v1/products" | jq -r '.data.id // .message'
}

echo "==> upload images"
MAC_KEY="$(upload_image "$SEED_DIR/macbook-pro.png")"
echo "    macbook-pro.png  -> $MAC_KEY"

echo "==> create products"
create_product \
  "Apple MacBook Pro 14 (M3)" \
  "Apple M3 芯片 8 核 CPU / 10 核 GPU, 14.2 英寸 Liquid 视网膜 XDR 显示屏, 16GB 统一内存, 512GB SSD。" \
  "18124" \
  12 \
  "on_sale" \
  "$MAC_KEY"

create_product \
  "iPhone 15 Pro 256GB" \
  "钛金属设计, A17 Pro 芯片, ProRAW + 5 倍长焦镜头。" \
  "8999" \
  30 \
  "on_sale" \
  ""

create_product \
  "AirPods Pro 2 (USB-C)" \
  "主动降噪 + 自适应音频, USB-C 充电盒, 单次最长 6 小时聆听。" \
  "1899" \
  80 \
  "on_sale" \
  ""

create_product \
  "Apple Watch Ultra 2" \
  "49mm 钛金属表壳, 双频 GPS, 36 小时常规续航。" \
  "6299" \
  18 \
  "on_sale" \
  ""

create_product \
  "iPad Air 13 (M2) Wi-Fi 256GB" \
  "M2 芯片, 13 英寸 Liquid 视网膜显示屏, 支持 Apple Pencil Pro。" \
  "4799" \
  0 \
  "off_sale" \
  ""

echo "==> verify"
curl -sk -b "$COOKIE" "$BASE/api/admin/v1/products?page=1&page_size=20" \
  | jq -r '.data.list[] | "\(.id)  \(.status)  \(.price_points)积分  \(.name)"'

echo "==> done"