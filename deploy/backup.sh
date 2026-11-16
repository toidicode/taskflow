#!/bin/sh
# Sao lưu database và file upload của TaskFlow vào thư mục backups/ cạnh compose.yaml.
# Chạy tay một lần trước (tạo thư mục backups/ cho file log của cron), rồi thêm cron lúc 3 giờ sáng mỗi ngày
# (crontab -e của user deploy):
#   0 3 * * * /opt/taskflow/backup.sh >> /opt/taskflow/backups/backup.log 2>&1
set -eu
umask 077 # bản sao lưu chứa toàn bộ dữ liệu, kể cả hash mật khẩu: chỉ user deploy đọc được
cd "$(dirname "$0")"

dir=backups
keep_days=${KEEP_DAYS:-14}
stamp=$(date +%Y%m%d-%H%M%S)
mkdir -p "$dir"

# -Fc: định dạng custom (nén sẵn, pg_restore chọn được bảng); -T: không cấp TTY, output nhị phân không bị hỏng.
# Ghi ra file .tmp rồi mới đổi tên: pg_dump hỏng giữa chừng thì không để lại file trông như bản sao lưu tốt.
docker compose exec -T db pg_dump -U taskflow -d taskflow -Fc > "$dir/taskflow-$stamp.dump.tmp"
mv "$dir/taskflow-$stamp.dump.tmp" "$dir/taskflow-$stamp.dump"

# docker compose cp đọc file từ container qua Docker API, image distroless không cần có tar
docker compose cp api:/data/uploads "$dir/uploads-$stamp"
tar -czf "$dir/uploads-$stamp.tar.gz" -C "$dir" "uploads-$stamp"
rm -rf "$dir/uploads-$stamp"

# chỉ giữ bản sao lưu của keep_days ngày gần nhất; xóa file .tmp dở dang của các lần chạy lỗi trước
find "$dir" -name 'taskflow-*.dump.tmp' -delete
find "$dir" -name 'taskflow-*.dump' -mtime +"$keep_days" -delete
find "$dir" -name 'uploads-*.tar.gz' -mtime +"$keep_days" -delete
echo "$(date '+%F %T') đã sao lưu $dir/taskflow-$stamp.dump và $dir/uploads-$stamp.tar.gz"
