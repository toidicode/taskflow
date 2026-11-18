# Image của TaskFlow API và notifier (Bài 50, chạy bằng entrypoint /notifier): build trong image golang,
# chạy trong image distroless.
#   docker build --build-arg VERSION=0.38.0 -t taskflow-api:0.38.0 .

# ---- stage 1: build ----
FROM golang:1.27.4 AS build
WORKDIR /src
# tải module thành một layer riêng (Bài 53): go.mod, go.sum không đổi thì layer này lấy từ cache,
# kể cả cache của GitHub Actions (cache mount bên dưới không được lưu vào cache của CI)
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION
# cache mount: cache biên dịch được giữ lại giữa các lần build trên cùng máy, không nằm trong image
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/ ./cmd/api ./cmd/notifier \
    && mkdir -p /out/data/uploads

# ---- stage 2: chạy ----
# static-debian13 chỉ có CA certificates, tzdata, /etc/passwd và /tmp: không shell, không package manager
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/api /out/notifier /
# thư mục upload thuộc user nonroot (uid 65532), volume mới gắn vào đây sẽ nhận quyền này
COPY --from=build --chown=nonroot:nonroot /out/data /data
ENV TASKFLOW_ADDR=:8080 \
    TASKFLOW_UPLOAD_DIR=/data/uploads
EXPOSE 8080
USER nonroot:nonroot
# không có curl: chính binary gọi /ready
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 CMD ["/api", "-healthcheck"]
# dạng exec (mảng JSON): /api là PID 1 và nhận thẳng SIGTERM của docker stop
ENTRYPOINT ["/api"]
