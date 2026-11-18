#!/bin/sh
# Deploy một version của TaskFlow (Bài 53), đặt cạnh compose.yaml trên server (VD /opt/taskflow).
#   chạy tay:          ./deploy.sh 0.53.0
#   từ GitHub Actions: key của CI trong ~/.ssh/authorized_keys chỉ được chạy đúng script này:
#     command="/opt/taskflow/deploy.sh",restrict ssh-ed25519 AAAA... github-actions
#   khi đó "ssh deploy@server 0.53.0" không mở shell mà chạy script với SSH_ORIGINAL_COMMAND=0.53.0
set -eu

version=${SSH_ORIGINAL_COMMAND:-${1:-}}
# chỉ nhận dạng 1.2.3 hoặc 1.2.3-rc.1: chuỗi này đi vào lệnh sed và tag image
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$'; then
	echo "version không hợp lệ: '$version'" >&2
	exit 2
fi

cd "$(dirname "$0")"
# biến môi trường được ưu tiên hơn .env khi compose thay ${TASKFLOW_VERSION}
export TASKFLOW_VERSION="$version"

# thứ tự như Bài 39: kéo image, migration trong lúc bản cũ vẫn chạy, rồi mới thay api.
# Bước nào lỗi thì set -e dừng script: bản cũ vẫn phục vụ, .env chưa đổi.
docker compose pull --quiet migrate api notifier
docker compose run --rm migrate
docker compose up -d --wait --wait-timeout 120 # chờ tới khi các service healthy

# ghi lại version đang chạy: lần "docker compose up -d" sau (sửa .env, khởi động lại...) không quay về bản cũ
sed -i "s/^TASKFLOW_VERSION=.*/TASKFLOW_VERSION=$version/" .env
echo "đã deploy $version"
